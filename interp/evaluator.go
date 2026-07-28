// interp/evaluator.go
package interp

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strconv"
	"strings"
)

// Run executes source with a background context. Hosts running untrusted or
// long-lived code should use RunContext with a deadline instead.
func (vm *Interpreter) Run(src string) error {
	return vm.RunContext(context.Background(), src)
}

// RunContext parses one Go source unit (package main), resolves simple
// imports, and executes main(). Cancellation is checked between evaluator
// operations and while guest code is blocked on channels or select.
func (vm *Interpreter) RunContext(ctx context.Context, src string) (err error) {
	exec, err := vm.beginExecution(ctx)
	if err != nil {
		return err
	}
	vm.emitTrace("run_start", "main", "", nil)
	defer func() {
		// A guest goroutine must not outlive its host invocation. All evaluator
		// and channel waits observe this cancellation and unwind cooperatively.
		exec.cancel()
		exec.wg.Wait()
		message := "ok"
		if err != nil {
			message = err.Error()
		}
		vm.emitTrace("run_end", "main", message, nil)
		vm.endExecution(exec)
	}()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "input.go", src, 0)
	exec.fset = fset
	if err != nil {
		return err
	}
	exec.litCache = buildLitCache(file)
	if file.Name.Name != "main" {
		return NewRuntimeError(`only "package main" is supported`)
	}
	if err := exec.err(); err != nil {
		return err
	}

	global := vm.globals

	// Handle imports (limited curated set).
	for _, decl := range file.Decls {
		if err := exec.err(); err != nil {
			return err
		}
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.IMPORT {
			continue
		}
		for _, sp := range gd.Specs {
			is := sp.(*ast.ImportSpec)
			path := strings.Trim(is.Path.Value, `"`)
			alias := ""
			if is.Name != nil {
				alias = is.Name.Name
			} else {
				// default alias is the last path segment
				parts := strings.Split(path, "/")
				alias = parts[len(parts)-1]
			}
			vm.installImportedPackage(alias, path)
		}
	}

	// Collect top-level declarations.
	for _, decl := range file.Decls {
		if err := exec.err(); err != nil {
			return err
		}
		switch d := decl.(type) {
		case *ast.GenDecl:
			switch d.Tok {
			case token.TYPE:
				for _, spec := range d.Specs {
					ts := spec.(*ast.TypeSpec)
					switch tt := ts.Type.(type) {
					case *ast.StructType:
						td := &TypeDef{Name: ts.Name.Name, Kind: "struct", Fields: []FieldDef{}, Methods: map[string]*Function{}}
						for _, f := range tt.Fields.List {
							ft := typeString(f.Type)
							for _, n := range f.Names {
								td.Fields = append(td.Fields, FieldDef{Name: n.Name, Type: ft})
							}
						}
						vm.types[td.Name] = td
					default:
						// other type decls are ignored in this subset
					}
				}
			case token.CONST, token.VAR:
				for _, spec := range d.Specs {
					vs := spec.(*ast.ValueSpec)
					for i, name := range vs.Names {
						if name.Name == "_" {
							continue
						}
						var val any
						if i < len(vs.Values) {
							v, err := vm.evalExpr(vs.Values[i], global)
							if err != nil {
								return err
							}
							val = v
						} else {
							val = zeroValue(typeString(vs.Type))
						}
						vm.declare(name.Name, val, global)
					}
				}
			}
		case *ast.FuncDecl:
			fn := &Function{Name: d.Name.Name, Body: d.Body, Env: global}
			// Params
			if d.Type.Params != nil {
				for i, f := range d.Type.Params.List {
					for _, n := range f.Names {
						fn.Params = append(fn.Params, n.Name)
					}
					// variadic if last param is *ast.Ellipsis
					if i == len(d.Type.Params.List)-1 {
						if _, ok := f.Type.(*ast.Ellipsis); ok {
							fn.IsVariadic = true
						}
					}
				}
			}
			// Method receiver?
			if d.Recv != nil && len(d.Recv.List) > 0 {
				rcv := d.Recv.List[0]
				fn.RecvName = rcv.Names[0].Name
				fn.RecvType = strings.TrimPrefix(typeString(rcv.Type), "*")
				td := vm.types[fn.RecvType]
				if td == nil {
					td = &TypeDef{Name: fn.RecvType, Kind: "struct", Methods: map[string]*Function{}}
					vm.types[fn.RecvType] = td
				}
				td.Methods[fn.Name] = fn
			} else {
				vm.funcs[fn.Name] = fn
				vm.declare(fn.Name, fn, vm.globals)
			}
		}
	}

	// Execute main()
	mainFn, ok := vm.funcs["main"]
	if !ok {
		return NewRuntimeError("no main() function found")
	}
	_, err = vm.callFunction(mainFn, global, nil, nil)
	if err != nil {
		if executionErr := exec.err(); executionErr != nil {
			return executionErr
		}
		return err
	}
	return exec.err()
}

// ---------------- Expression evaluation ---------------------------

// evalExpr evaluates e and, if it fails, tags the error with e's source
// position — but only the first time, i.e. only if the error doesn't already
// carry one. Errors are created deep inside evalExprNode's switch (an
// undefined identifier, a bad conversion, ...) and then bubble up through
// every enclosing expression's own evalExpr call on their way out; tagging
// unconditionally at each level would keep overwriting the precise failure
// site with each successively coarser enclosing expression. First-write-wins
// keeps it pinned to the innermost (most useful) location instead.
func (vm *Interpreter) evalExpr(e ast.Expr, env *Env) (any, error) {
	v, err := vm.evalExprNode(e, env)
	if err != nil {
		attachRuntimeErrorLocation(err, vm.traceLocation(e.Pos()))
	}
	return v, err
}

func (vm *Interpreter) evalExprNode(e ast.Expr, env *Env) (any, error) {
	if err := vm.executionError(); err != nil {
		return nil, err
	}
	switch ex := e.(type) {
	case *ast.BasicLit:
		switch ex.Kind {
		case token.INT:
			if exec := vm.activeExecution; exec != nil {
				if n, ok := exec.litCache[ex]; ok {
					return n, nil
				}
			}
			// Use strconv to correctly handle 0x, 0o, 0b, and underscored literals.
			// We pass strconv.IntSize as the bitSize so strconv itself returns an
			// error for values that don't fit in the platform's `int` type
			// (notably js/wasm where int is 32-bit). This eliminates the need
			// for a manual narrowing check on the returned int64.
			n, err := strconv.ParseInt(ex.Value, 0, strconv.IntSize)
			if err != nil {
				return 0, NewRuntimeError("invalid integer literal: " + ex.Value)
			}
			return int(n), nil
		case token.FLOAT:
			f, err := strconv.ParseFloat(strings.ReplaceAll(ex.Value, "_", ""), 64)
			if err != nil {
				return 0.0, NewRuntimeError("invalid float literal: " + ex.Value)
			}
			return f, nil
		case token.STRING:
			// Use strconv.Unquote to handle escape sequences (\n, \t, \", \\, \uXXXX, ...)
			// and both interpreted ("...") and raw (`...`) string literals.
			if s, err := strconv.Unquote(ex.Value); err == nil {
				return s, nil
			}
			// Fallback: strip surrounding quotes if present.
			s := ex.Value
			if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
				return s[1 : len(s)-1], nil
			}
			return s, nil
		case token.CHAR:
			// strconv.UnquoteChar requires the leading quote stripped.
			v := ex.Value
			if len(v) < 3 || v[0] != '\'' || v[len(v)-1] != '\'' {
				return 0, NewRuntimeError("invalid character literal")
			}
			r, _, _, err := strconv.UnquoteChar(v[1:len(v)-1], '\'')
			if err != nil {
				return 0, NewRuntimeError("invalid character literal: " + v)
			}
			return int(r), nil
		default:
			return nil, NewRuntimeError(fmt.Sprintf("unsupported basic literal kind: %v", ex.Kind))
		}
	case *ast.Ident:
		switch ex.Name {
		case "true":
			return true, nil
		case "false":
			return false, nil
		case "nil":
			return nil, nil
		}
		if isBuiltinType(ex.Name) {
			return &Function{Name: ex.Name, Native: func(args []any) (any, error) {
				if len(args) == 0 {
					return zeroValue(ex.Name), nil
				}
				return builtinConvert(ex.Name, args[0]), nil
			}}, nil
		}
		if v, ok := vm.get(ex.Name, env); ok {
			return v, nil
		}
		if f, ok := vm.funcs[ex.Name]; ok {
			return f, nil
		}
		if n, ok := vm.natives[ex.Name]; ok {
			return &Function{Name: ex.Name, Native: n}, nil
		}
		if _, ok := vm.types[ex.Name]; ok {
			return ex.Name, nil
		}
		return nil, NewRuntimeError("undefined: " + ex.Name)

	case *ast.UnaryExpr:
		if ex.Op == token.ARROW {
			// Receive from channel: <-ch  (single value; two-value handled in assign)
			v, err := vm.evalExpr(ex.X, env)
			if err != nil {
				return nil, err
			}
			ch, ok := v.(*ChannelVal)
			if !ok || ch == nil {
				return nil, NewRuntimeError("receive on non-channel")
			}
			val, ok2, err := ch.Receive(vm.Context())
			if err != nil {
				return nil, err
			}
			if !ok2 {
				return zeroValue(ch.ElementType), nil
			}
			return val, nil
		}
		v, err := vm.evalExpr(ex.X, env)
		if err != nil {
			return nil, err
		}
		switch ex.Op {
		case token.NOT:
			return !ToBool(v), nil
		case token.SUB:
			if _, ok := v.(float64); ok {
				return -ToFloat(v), nil
			}
			return -ToInt(v), nil
		case token.ADD:
			if _, ok := v.(float64); ok {
				return +ToFloat(v), nil
			}
			return +ToInt(v), nil
		case token.XOR:
			return ^ToInt(v), nil
		case token.AND:
			return v, nil // address-of ignored
		default:
			return nil, NewRuntimeError("unsupported unary op")
		}

	case *ast.BinaryExpr:
		// Keep pure integer arithmetic out of interface{} until the result
		// crosses an actual dynamic-value boundary. The regular evaluator
		// returns an any for every AST node, which makes large integer
		// intermediates escape to the heap. Tight counter/arithmetic loops are
		// therefore allocation-heavy even though all their intermediate values
		// are plain ints. This path preserves the normal checkpoint cadence (one
		// per AST node) and falls back before evaluating anything effectful when
		// an expression is not statically an integer expression.
		if n, ok, err := vm.tryEvalIntExpr(ex, env, false); err != nil {
			return nil, err
		} else if ok {
			return n, nil
		}

		// Integer comparisons are similarly common loop conditions. Evaluating
		// both operands as ints avoids boxing large literal bounds (for example
		// i < 100000) on every iteration.
		if isIntComparison(ex.Op) {
			left, leftOK, err := vm.tryEvalIntExpr(ex.X, env, true)
			if err != nil {
				return nil, err
			}
			if leftOK {
				right, rightOK, err := vm.tryEvalIntExpr(ex.Y, env, true)
				if err != nil {
					return nil, err
				}
				if rightOK {
					switch ex.Op {
					case token.EQL:
						return left == right, nil
					case token.NEQ:
						return left != right, nil
					case token.LSS:
						return left < right, nil
					case token.GTR:
						return left > right, nil
					case token.LEQ:
						return left <= right, nil
					case token.GEQ:
						return left >= right, nil
					}
				}
			}
		}

		l, err := vm.evalExpr(ex.X, env)
		if err != nil {
			return nil, err
		}
		r, err := vm.evalExpr(ex.Y, env)
		if err != nil {
			return nil, err
		}
		return vm.applyBinaryOp(ex.Op, l, r)

	case *ast.CallExpr:
		// Builtins: make, len, cap, append, copy, close, delete, panic
		if id, ok := ex.Fun.(*ast.Ident); ok {
			switch id.Name {
			case "make":
				if len(ex.Args) == 0 {
					return nil, NewRuntimeError("make: missing type")
				}
				tstr := typeString(ex.Args[0])
				var args []any
				for _, a := range ex.Args[1:] {
					v, err := vm.evalExpr(a, env)
					if err != nil {
						return nil, err
					}
					args = append(args, v)
				}
				return vm.builtinMake(tstr, args)
			case "len":
				if len(ex.Args) != 1 {
					return 0, nil
				}
				v, err := vm.evalExpr(ex.Args[0], env)
				if err != nil {
					return nil, err
				}
				return builtinLen(v), nil
			case "cap":
				if len(ex.Args) != 1 {
					return 0, nil
				}
				v, err := vm.evalExpr(ex.Args[0], env)
				if err != nil {
					return nil, err
				}
				return builtinCap(v), nil
			case "append":
				if len(ex.Args) < 1 {
					return nil, NewRuntimeError("append: args")
				}
				s, err := vm.evalExpr(ex.Args[0], env)
				if err != nil {
					return nil, err
				}
				var els []any
				for i, a := range ex.Args[1:] {
					// Support f(slice...) expansion if CallExpr.Ellipsis is set on last arg.
					if ex.Ellipsis != token.NoPos && i == len(ex.Args[1:])-1 {
						v, err := vm.evalExpr(a, env)
						if err != nil {
							return nil, err
						}
						if sv, ok := v.(*SliceVal); ok {
							els = append(els, sv.Data...)
						} else {
							els = append(els, v)
						}
					} else {
						v, err := vm.evalExpr(a, env)
						if err != nil {
							return nil, err
						}
						els = append(els, v)
					}
				}
				return vm.builtinAppend(s, els...)
			case "copy":
				if len(ex.Args) != 2 {
					return 0, nil
				}
				dst, err := vm.evalExpr(ex.Args[0], env)
				if err != nil {
					return nil, err
				}
				src, err := vm.evalExpr(ex.Args[1], env)
				if err != nil {
					return nil, err
				}
				return builtinCopy(dst, src), nil
			case "close":
				if len(ex.Args) != 1 {
					return nil, NewRuntimeError("close: need channel")
				}
				v, err := vm.evalExpr(ex.Args[0], env)
				if err != nil {
					return nil, err
				}
				return builtinClose(v)
			case "delete":
				if len(ex.Args) != 2 {
					return nil, nil
				}
				m, err := vm.evalExpr(ex.Args[0], env)
				if err != nil {
					return nil, err
				}
				k, err := vm.evalExpr(ex.Args[1], env)
				if err != nil {
					return nil, err
				}
				if mm, ok := m.(*MapVal); ok {
					mm.deleteByKey(k)
				}
				return nil, nil
			case "panic":
				if len(ex.Args) == 0 {
					return nil, &panicError{value: "panic"}
				}
				v, err := vm.evalExpr(ex.Args[0], env)
				if err != nil {
					return nil, err
				}
				return nil, &panicError{value: v}
			}
		}

		// Package function call: fmt.Printf, time.Now, ...
		if sel, ok := ex.Fun.(*ast.SelectorExpr); ok {
			if pid, ok := sel.X.(*ast.Ident); ok {
				// Resolve the package identifier starting at the caller's own
				// lexical env (not always vm.globals) so per-package import
				// scopes (see PackageScope) stay isolated from sibling
				// packages. For every program reachable via Run/RunContext,
				// vm.globals is always an ancestor of env, so this is
				// behavior-preserving there.
				if p, ok := vm.get(pid.Name, env); ok {
					if p, ok := p.(*Package); ok {
						if p.Name == "debug" {
							switch sel.Sel.Name {
							case "Q":
								return vm.traceDebugQ(ex, env)
							case "Mark":
								return vm.traceDebugMark(ex, env)
							case "Stack":
								return vm.traceDebugStack(ex, env)
							case "Vars":
								return vm.traceDebugVars(ex, env)
							}
						}
						member, ok2 := vm.resolvePackageSelector(p, sel.Sel.Name)
						if !ok2 {
							return nil, NewRuntimeError("unknown package member: " + pid.Name + "." + sel.Sel.Name)
						}
						fn, ok3 := member.(*Function)
						if !ok3 {
							return nil, NewRuntimeError("package member is not function")
						}
						// Evaluate args (including ... expansion)
						args := make([]any, 0, len(ex.Args))
						if ex.Ellipsis != token.NoPos && len(ex.Args) > 0 {
							for i, a := range ex.Args {
								if i == len(ex.Args)-1 {
									v, err := vm.evalExpr(a, env)
									if err != nil {
										return nil, err
									}
									if sv, ok := v.(*SliceVal); ok {
										args = append(args, sv.Data...)
									} else {
										args = append(args, v)
									}
								} else {
									v, err := vm.evalExpr(a, env)
									if err != nil {
										return nil, err
									}
									args = append(args, v)
								}
							}
						} else {
							for _, a := range ex.Args {
								v, err := vm.evalExpr(a, env)
								if err != nil {
									return nil, err
								}
								args = append(args, v)
							}
						}
						return vm.callFunction(fn, env, nil, args)
					}
				}
			}
		}

		// Method call on struct: obj.M(...)
		if sel, ok := ex.Fun.(*ast.SelectorExpr); ok {
			recv, err := vm.evalExpr(sel.X, env)
			if err != nil {
				return nil, err
			}
			recvType := typeOfValue(vm, recv)
			td := vm.types[recvType]
			if td == nil || td.Methods == nil {
				return nil, NewRuntimeError("unknown method on type " + recvType)
			}
			fn := td.Methods[sel.Sel.Name]
			if fn == nil {
				return nil, NewRuntimeError("method not found: " + recvType + "." + sel.Sel.Name)
			}
			args := make([]any, 1, len(ex.Args)+1)
			args[0] = recv
			// Evaluate args (support last ... expansion)
			if ex.Ellipsis != token.NoPos && len(ex.Args) > 0 {
				for i, a := range ex.Args {
					if i == len(ex.Args)-1 {
						v, err := vm.evalExpr(a, env)
						if err != nil {
							return nil, err
						}
						if sv, ok := v.(*SliceVal); ok {
							args = append(args, sv.Data...)
						} else {
							args = append(args, v)
						}
					} else {
						v, err := vm.evalExpr(a, env)
						if err != nil {
							return nil, err
						}
						args = append(args, v)
					}
				}
			} else {
				for _, a := range ex.Args {
					v, err := vm.evalExpr(a, env)
					if err != nil {
						return nil, err
					}
					args = append(args, v)
				}
			}
			return vm.callFunction(fn, env, &recv, args[1:])
		}

		// Normal function call
		callee, err := vm.evalExpr(ex.Fun, env)
		if err != nil {
			return nil, err
		}
		switch fn := callee.(type) {
		case *Function:
			args := make([]any, 0, len(ex.Args))
			// Handle foo(slice...) expansion
			if ex.Ellipsis != token.NoPos && len(ex.Args) > 0 {
				for i, a := range ex.Args {
					if i == len(ex.Args)-1 {
						v, err := vm.evalExpr(a, env)
						if err != nil {
							return nil, err
						}
						if sv, ok := v.(*SliceVal); ok {
							args = append(args, sv.Data...)
						} else {
							args = append(args, v)
						}
					} else {
						v, err := vm.evalExpr(a, env)
						if err != nil {
							return nil, err
						}
						args = append(args, v)
					}
				}
			} else {
				for _, a := range ex.Args {
					v, err := vm.evalExpr(a, env)
					if err != nil {
						return nil, err
					}
					args = append(args, v)
				}
			}
			return vm.callFunction(fn, env, nil, args)
		default:
			return nil, NewRuntimeError("not a function")
		}

	case *ast.IndexExpr:
		v, err := vm.evalExpr(ex.X, env)
		if err != nil {
			return nil, err
		}
		i, err := vm.evalExpr(ex.Index, env)
		if err != nil {
			return nil, err
		}
		switch t := v.(type) {
		case *SliceVal:
			ii := ToInt(i)
			if ii < 0 || ii >= len(t.Data) {
				return nil, NewRuntimeError("index out of range")
			}
			return t.Data[ii], nil
		case *MapVal:
			val, _ := t.getByKey(i)
			return val, nil
		case string:
			idx := ToInt(i)
			if idx < 0 || idx >= len(t) {
				return nil, NewRuntimeError("index out of range")
			}
			return int(t[idx]), nil
		default:
			return nil, NewRuntimeError("indexing unsupported")
		}

	case *ast.SliceExpr:
		v, err := vm.evalExpr(ex.X, env)
		if err != nil {
			return nil, err
		}
		lo := 0
		hi := -1
		if ex.Low != nil {
			lv, err := vm.evalExpr(ex.Low, env)
			if err != nil {
				return nil, err
			}
			lo = ToInt(lv)
		}
		if ex.High != nil {
			hv, err := vm.evalExpr(ex.High, env)
			if err != nil {
				return nil, err
			}
			hi = ToInt(hv)
		}
		switch s := v.(type) {
		case *SliceVal:
			if hi < 0 || hi > len(s.Data) {
				hi = len(s.Data)
			}
			if lo < 0 || lo > hi {
				return nil, NewRuntimeError("invalid slice indices")
			}
			return &SliceVal{ElementType: s.ElementType, Data: s.Data[lo:hi]}, nil
		case string:
			if hi < 0 || hi > len(s) {
				hi = len(s)
			}
			if lo < 0 || lo > hi {
				return nil, NewRuntimeError("invalid slice indices")
			}
			return s[lo:hi], nil
		default:
			return nil, NewRuntimeError("slice unsupported")
		}

	case *ast.SelectorExpr:
		// Package selector (pkg.Member)
		if id, ok := ex.X.(*ast.Ident); ok {
			// See the matching comment in the CallExpr case above: resolve
			// against the caller's own env, not unconditionally vm.globals.
			if p, ok := vm.get(id.Name, env); ok {
				if p, ok := p.(*Package); ok {
					m, ok2 := vm.resolvePackageSelector(p, ex.Sel.Name)
					if !ok2 {
						return nil, NewRuntimeError("unknown package member: " + id.Name + "." + ex.Sel.Name)
					}
					return m, nil
				}
			}
		}
		// Struct field access is handled when receiver is *StructVal during method calls or via fieldRef in assignments.
		recv, err := vm.evalExpr(ex.X, env)
		if err != nil {
			return nil, err
		}
		sv, ok := recv.(*StructVal)
		if !ok {
			return nil, NewRuntimeError("selector on non-struct")
		}
		return sv.Fields[ex.Sel.Name], nil

	case *ast.CompositeLit:
		// Struct, slice, map literals.
		typ := typeString(ex.Type)
		if strings.HasPrefix(typ, "[]") {
			elem := typ[2:]
			lit := &SliceVal{ElementType: elem, Data: []any{}}
			for _, elt := range ex.Elts {
				v, err := vm.evalExpr(elt, env)
				if err != nil {
					return nil, err
				}
				lit.Data = append(lit.Data, v)
			}
			return lit, nil
		}
		if strings.HasPrefix(typ, "map[") {
			k, v := parseMapType(typ)
			lit := &MapVal{KeyType: k, ElementType: v, Data: map[string]any{}, Keys: map[string]any{}}
			for _, elt := range ex.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, err := vm.evalExpr(kv.Key, env)
				if err != nil {
					return nil, err
				}
				val, err := vm.evalExpr(kv.Value, env)
				if err != nil {
					return nil, err
				}
				lit.setByKey(key, val)
			}
			return lit, nil
		}
		// Struct literal with keyed fields (package prefix reduced by typeString)
		typ = strings.TrimPrefix(typ, "*")
		td := vm.types[typ]
		if td == nil || td.Kind != "struct" {
			return nil, NewRuntimeError("unknown struct type: " + typ)
		}
		obj := &StructVal{TypeName: typ, Fields: map[string]any{}}
		for _, f := range td.Fields {
			obj.Fields[f.Name] = zeroValue(f.Type)
		}
		for _, elt := range ex.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key := kv.Key.(*ast.Ident).Name
			val, err := vm.evalExpr(kv.Value, env)
			if err != nil {
				return nil, err
			}
			obj.Fields[key] = val
		}
		return obj, nil

	case *ast.ParenExpr:
		return vm.evalExpr(ex.X, env)

	case *ast.FuncLit:
		fn := &Function{Name: "<anon>", Body: ex.Body, Env: env}
		if ex.Type.Params != nil {
			for _, f := range ex.Type.Params.List {
				for _, n := range f.Names {
					fn.Params = append(fn.Params, n.Name)
				}
			}
		}
		return fn, nil

	default:
		return nil, NewRuntimeError(fmt.Sprintf("unsupported expr: %T", e))
	}
}

// tryEvalIntExpr evaluates the integer-only subset without allocating an any
// result for every intermediate expression. handled is false when evaluating
// the expression through the ordinary dynamic evaluator is necessary.
//
// The initial call from evalExprNode has already consumed its checkpoint;
// recursive calls must checkpoint their own nodes just as evalExpr would.
func (vm *Interpreter) tryEvalIntExpr(e ast.Expr, env *Env, checkpoint bool) (value int, handled bool, err error) {
	if checkpoint {
		if err := vm.executionError(); err != nil {
			return 0, false, err
		}
	}

	switch ex := e.(type) {
	case *ast.BasicLit:
		if ex.Kind != token.INT {
			return 0, false, nil
		}
		if exec := vm.activeExecution; exec != nil {
			if n, ok := exec.litCache[ex]; ok {
				return n, true, nil
			}
		}
		n, parseErr := strconv.ParseInt(ex.Value, 0, strconv.IntSize)
		if parseErr != nil {
			err := NewRuntimeError("invalid integer literal: " + ex.Value)
			attachRuntimeErrorLocation(err, vm.traceLocation(ex.Pos()))
			return 0, true, err
		}
		return int(n), true, nil

	case *ast.Ident:
		n, ok := vm.getInt(ex.Name, env)
		return n, ok, nil

	case *ast.ParenExpr:
		return vm.tryEvalIntExpr(ex.X, env, true)

	case *ast.UnaryExpr:
		if ex.Op != token.ADD && ex.Op != token.SUB && ex.Op != token.XOR {
			return 0, false, nil
		}
		n, ok, err := vm.tryEvalIntExpr(ex.X, env, true)
		if err != nil || !ok {
			return 0, ok, err
		}
		switch ex.Op {
		case token.ADD:
			return n, true, nil
		case token.SUB:
			return -n, true, nil
		default:
			return ^n, true, nil
		}

	case *ast.BinaryExpr:
		if !isIntArithmetic(ex.Op) {
			return 0, false, nil
		}
		left, leftOK, err := vm.tryEvalIntExpr(ex.X, env, true)
		if err != nil || !leftOK {
			return 0, leftOK, err
		}
		right, rightOK, err := vm.tryEvalIntExpr(ex.Y, env, true)
		if err != nil || !rightOK {
			return 0, rightOK, err
		}
		switch ex.Op {
		case token.ADD:
			return left + right, true, nil
		case token.SUB:
			return left - right, true, nil
		case token.MUL:
			return left * right, true, nil
		case token.REM:
			if right == 0 {
				err := NewRuntimeError("integer divide by zero")
				attachRuntimeErrorLocation(err, vm.traceLocation(ex.Pos()))
				return 0, true, err
			}
			return left % right, true, nil
		case token.SHL:
			return left << uint(right), true, nil
		case token.SHR:
			return left >> uint(right), true, nil
		case token.AND:
			return left & right, true, nil
		case token.OR:
			return left | right, true, nil
		case token.XOR:
			return left ^ right, true, nil
		case token.AND_NOT:
			return left &^ right, true, nil
		}
	}

	return 0, false, nil
}

func isIntArithmetic(op token.Token) bool {
	switch op {
	case token.ADD, token.SUB, token.MUL, token.REM, token.SHL, token.SHR,
		token.AND, token.OR, token.XOR, token.AND_NOT:
		return true
	default:
		return false
	}
}

func isIntComparison(op token.Token) bool {
	switch op {
	case token.EQL, token.NEQ, token.LSS, token.GTR, token.LEQ, token.GEQ:
		return true
	default:
		return false
	}
}

// ---------------- Statement evaluation ----------------------------

type controlKind int

const (
	controlNone controlKind = iota
	controlReturn
	controlBreak
	controlContinue
)

type controlFlow struct {
	kind controlKind
	val  any
}

// blockNeedsOwnScope reports whether any top-level statement in block can
// declare a name directly into whatever env the block itself evaluates in —
// only *ast.AssignStmt with token.DEFINE (:=) and *ast.DeclStmt (var/const)
// do that (see the vm.declare call sites in evalStmtNode's AssignStmt and
// DeclStmt cases). Everything else either doesn't declare at all, or
// declares into a scope it creates for itself (a nested block, an if/for's
// own body, a switch case) — so it makes this same decision independently
// and doesn't affect whether THIS block needs a scope of its own.
//
// Only the block's immediate statement list is inspected, not nested
// blocks: a nested block's own declarations are scoped to itself regardless
// of whether this outer block forked, by the same recursive application of
// this rule when that inner block is evaluated.
func blockNeedsOwnScope(block *ast.BlockStmt) bool {
	for _, s := range block.List {
		switch s := s.(type) {
		case *ast.AssignStmt:
			if s.Tok == token.DEFINE {
				return true
			}
		case *ast.DeclStmt:
			return true
		}
	}
	return false
}

// evalStmt mirrors evalExpr's location tagging (see its comment): the first
// (innermost) statement whose evaluation fails wins the position tag as the
// error bubbles up through enclosing statements' own evalStmt calls.
//
// It also feeds the optional line profiler (see profile.go): every
// statement evaluation — regardless of call depth or which goroutine it
// runs in — counts as one hit on its source line, which is exactly what a
// "how often was this line executed" heatmap wants. The atomic Load keeps
// the cost of having no profiler installed to a single pointer read; only
// resolving the actual line number (traceLocation) is skipped entirely in
// that (default) case.
func (vm *Interpreter) evalStmt(s ast.Stmt, env *Env) (controlFlow, error) {
	if p := vm.lineProfile.Load(); p != nil {
		p.hit(vm.traceLocation(s.Pos()).Line)
	}
	cf, err := vm.evalStmtNode(s, env)
	if err != nil {
		attachRuntimeErrorLocation(err, vm.traceLocation(s.Pos()))
	}
	return cf, err
}

func (vm *Interpreter) evalStmtNode(s ast.Stmt, env *Env) (controlFlow, error) {
	if err := vm.executionError(); err != nil {
		return controlFlow{}, err
	}
	switch st := s.(type) {
	case *ast.ExprStmt:
		_, err := vm.evalExpr(st.X, env)
		return controlFlow{}, err

	case *ast.SendStmt:
		chv, err := vm.evalExpr(st.Chan, env)
		if err != nil {
			return controlFlow{}, err
		}
		val, err := vm.evalExpr(st.Value, env)
		if err != nil {
			return controlFlow{}, err
		}
		ch, ok := chv.(*ChannelVal)
		if !ok || ch == nil {
			return controlFlow{}, NewRuntimeError("send on non-channel")
		}
		return controlFlow{}, ch.Send(vm.Context(), val)

	case *ast.AssignStmt:
		// The common counter/accumulator shapes (i := 0, sum = sum+i, ...) can
		// retain their result in Env.intVars all the way through the assignment.
		// Do this before allocating the generic RHS []any used by the complete
		// assignment implementation below.
		if len(st.Lhs) == 1 && len(st.Rhs) == 1 {
			if id, ok := st.Lhs[0].(*ast.Ident); ok && id.Name != "_" {
				switch st.Tok {
				case token.DEFINE:
					n, intOK, err := vm.tryEvalIntExpr(st.Rhs[0], env, true)
					if err != nil {
						return controlFlow{}, err
					}
					if intOK {
						vm.declareInt(id.Name, n, env)
						return controlFlow{}, nil
					}
				case token.ASSIGN:
					if _, exists := vm.getInt(id.Name, env); exists {
						n, intOK, err := vm.tryEvalIntExpr(st.Rhs[0], env, true)
						if err != nil {
							return controlFlow{}, err
						}
						if intOK && vm.setInt(id.Name, n, env) {
							return controlFlow{}, nil
						}
					}
				}
			}
		}

		// Evaluate RHS first
		rightVals := make([]any, len(st.Rhs))

		// Special case: v, ok := m[k]
		if len(st.Lhs) == 2 && len(st.Rhs) == 1 {
			if ie, ok := st.Rhs[0].(*ast.IndexExpr); ok {
				mv, err := vm.evalExpr(ie.X, env)
				if err != nil {
					return controlFlow{}, err
				}
				if m, ok := mv.(*MapVal); ok {
					key, err := vm.evalExpr(ie.Index, env)
					if err != nil {
						return controlFlow{}, err
					}
					val, ok2 := m.getByKey(key)
					rightVals = []any{val, ok2}
					goto RHS_DONE
				}
			}
		}

		for i, r := range st.Rhs {
			// Special case: two-value receive v, ok := <-ch
			if len(st.Lhs) == 2 {
				if ue, ok := r.(*ast.UnaryExpr); ok && ue.Op == token.ARROW {
					// two-value receive
					cv, err := vm.evalExpr(ue.X, env)
					if err != nil {
						return controlFlow{}, err
					}
					ch, ok := cv.(*ChannelVal)
					if !ok || ch == nil {
						return controlFlow{}, NewRuntimeError("receive on non-channel")
					}
					v, ok2, err := ch.Receive(vm.Context())
					if err != nil {
						return controlFlow{}, err
					}
					rightVals = []any{v, ok2}
					goto RHS_DONE
				}
			}
			// Special case: val, err := someFunc()
			// When there are exactly 2 LHS targets and 1 RHS call expression,
			// capture the call error as the second value rather than propagating it.
			// This enables the idiomatic Go pattern: val, err := pkg.Func().
			if len(st.Lhs) == 2 && len(st.Rhs) == 1 {
				if _, isCall := r.(*ast.CallExpr); isCall {
					v, callErr := vm.evalExpr(r, env)
					if callErr != nil {
						rightVals = []any{v, callErr}
					} else {
						rightVals = []any{v, nil}
					}
					goto RHS_DONE
				}
			}
			v, err := vm.evalExpr(r, env)
			if err != nil {
				return controlFlow{}, err
			}
			rightVals[i] = v
		}
	RHS_DONE:
		// LHS references are resolved per assignment kind below rather than
		// upfront into a []Ref: token.DEFINE never touches a Ref at all (its
		// LHS is always plain identifiers, handled directly via declare),
		// and even for ASSIGN/augmented-assign, a plain identifier target
		// (the overwhelmingly common case: x = ..., x += ...) goes straight
		// through vm.get/vm.set instead of allocating a *varRef to wrap
		// exactly the same two calls behind the Ref interface — resolveRef
		// remains the fallback for index/selector lvalues (a[i] = ...,
		// s.Field = ...), which do need it.
		switch st.Tok {
		case token.DEFINE:
			for i, l := range st.Lhs {
				id, ok := l.(*ast.Ident)
				if !ok {
					return controlFlow{}, NewRuntimeError("invalid := lhs")
				}
				if id.Name == "_" {
					continue
				}
				var v any
				if len(rightVals) == 1 {
					v = rightVals[0]
				} else {
					v = rightVals[i]
				}
				vm.declare(id.Name, v, env)
			}
		case token.ASSIGN:
			for i, l := range st.Lhs {
				var v any
				if len(rightVals) == 1 {
					v = rightVals[0]
				} else {
					v = rightVals[i]
				}
				if id, ok := l.(*ast.Ident); ok {
					vm.set(id.Name, v, env)
					continue
				}
				ref, err := vm.resolveRef(l, env)
				if err != nil {
					return controlFlow{}, err
				}
				if err := ref.Set(v); err != nil {
					return controlFlow{}, err
				}
			}
		default:
			// augmented assignments supported via applyBinaryOp
			if len(st.Lhs) != 1 || len(rightVals) != 1 {
				return controlFlow{}, NewRuntimeError("augmented assignment expects 1 lhs and 1 rhs")
			}
			var base token.Token
			switch st.Tok {
			case token.ADD_ASSIGN:
				base = token.ADD
			case token.SUB_ASSIGN:
				base = token.SUB
			case token.MUL_ASSIGN:
				base = token.MUL
			case token.QUO_ASSIGN:
				base = token.QUO
			case token.REM_ASSIGN:
				base = token.REM
			case token.AND_ASSIGN:
				base = token.AND
			case token.OR_ASSIGN:
				base = token.OR
			case token.XOR_ASSIGN:
				base = token.XOR
			case token.SHL_ASSIGN:
				base = token.SHL
			case token.SHR_ASSIGN:
				base = token.SHR
			case token.AND_NOT_ASSIGN:
				base = token.AND_NOT
			default:
				return controlFlow{}, NewRuntimeError("unsupported assignment token")
			}
			if id, ok := st.Lhs[0].(*ast.Ident); ok {
				cur, _ := vm.get(id.Name, env)
				newVal, err := vm.applyBinaryOp(base, cur, rightVals[0])
				if err != nil {
					return controlFlow{}, err
				}
				vm.set(id.Name, newVal, env)
				return controlFlow{}, nil
			}
			ref, err := vm.resolveRef(st.Lhs[0], env)
			if err != nil {
				return controlFlow{}, err
			}
			newVal, err := vm.applyBinaryOp(base, ref.Get(), rightVals[0])
			if err != nil {
				return controlFlow{}, err
			}
			if err := ref.Set(newVal); err != nil {
				return controlFlow{}, err
			}
		}
		return controlFlow{}, nil

	case *ast.IncDecStmt:
		if id, ok := st.X.(*ast.Ident); ok {
			if cur, ok := vm.getInt(id.Name, env); ok {
				if st.Tok == token.INC {
					vm.setInt(id.Name, cur+1, env)
				} else {
					vm.setInt(id.Name, cur-1, env)
				}
				return controlFlow{}, nil
			}
			v, _ := vm.get(id.Name, env)
			cur := ToInt(v)
			if st.Tok == token.INC {
				vm.set(id.Name, cur+1, env)
			} else {
				vm.set(id.Name, cur-1, env)
			}
			return controlFlow{}, nil
		}
		ref, err := vm.resolveRef(st.X, env)
		if err != nil {
			return controlFlow{}, err
		}
		cur := ToInt(ref.Get())
		if st.Tok == token.INC {
			ref.Set(cur + 1)
		} else {
			ref.Set(cur - 1)
		}
		return controlFlow{}, nil

	case *ast.DeclStmt:
		decl := st.Decl.(*ast.GenDecl)
		switch decl.Tok {
		case token.VAR, token.CONST:
			for _, sp := range decl.Specs {
				vs := sp.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if n.Name == "_" {
						continue
					}
					var val any
					if i < len(vs.Values) {
						v, err := vm.evalExpr(vs.Values[i], env)
						if err != nil {
							return controlFlow{}, err
						}
						if vs.Type != nil {
							v = coerceToType(v, typeString(vs.Type))
						}
						val = v
					} else {
						val = zeroValue(typeString(vs.Type))
					}
					vm.declare(n.Name, val, env)
				}
			}
		}
		return controlFlow{}, nil

	case *ast.BlockStmt:
		// A fresh child scope is only actually needed when this block can
		// declare a name directly into it (:= or var/const — the two
		// statement kinds that call vm.declare with whatever env got passed
		// to them; see blockNeedsOwnScope). A block that only assigns to
		// outer variables or calls functions — the common shape of a loop
		// body or if-body — can evaluate its statements directly in the
		// parent's env instead, skipping an Env allocation (struct + mutex)
		// on every single iteration/entry. Nested statements that manage
		// their own scoping (nested blocks, for/range, switch cases) are
		// unaffected: each makes this same decision independently for
		// itself, so correctness (in particular per-iteration closure
		// isolation for a block that DOES declare) is unchanged either way.
		local := env
		if blockNeedsOwnScope(st) {
			local = NewEnv(env)
		}
		for _, s2 := range st.List {
			c, err := vm.evalStmt(s2, local)
			if err != nil {
				return controlFlow{}, err
			}
			switch c.kind {
			case controlReturn, controlBreak, controlContinue:
				return c, nil
			}
		}
		return controlFlow{}, nil

	case *ast.IfStmt:
		if st.Init != nil {
			if _, err := vm.evalStmt(st.Init, env); err != nil {
				return controlFlow{}, err
			}
		}
		cond, err := vm.evalExpr(st.Cond, env)
		if err != nil {
			return controlFlow{}, err
		}
		if ToBool(cond) {
			return vm.evalStmt(st.Body, env)
		} else if st.Else != nil {
			return vm.evalStmt(st.Else, env)
		}
		return controlFlow{}, nil

	case *ast.ForStmt:
		local := NewEnv(env)
		if st.Init != nil {
			if _, err := vm.evalStmt(st.Init, local); err != nil {
				return controlFlow{}, err
			}
		}
		for {
			cond := true
			if st.Cond != nil {
				v, err := vm.evalExpr(st.Cond, local)
				if err != nil {
					return controlFlow{}, err
				}
				cond = ToBool(v)
			}
			if !cond {
				break
			}
			c, err := vm.evalStmt(st.Body, local)
			if err != nil {
				return controlFlow{}, err
			}
			switch c.kind {
			case controlBreak:
				return controlFlow{}, nil
			case controlReturn:
				return c, nil
			case controlContinue: /* continue */
			}
			if st.Post != nil {
				if _, err := vm.evalStmt(st.Post, local); err != nil {
					return controlFlow{}, err
				}
			}
		}
		return controlFlow{}, nil

	case *ast.RangeStmt:
		local := NewEnv(env)
		x, err := vm.evalExpr(st.X, local)
		if err != nil {
			return controlFlow{}, err
		}
		switch s := x.(type) {
		case *SliceVal:
			for i := 0; i < len(s.Data); i++ {
				if st.Key != nil {
					if id, ok := st.Key.(*ast.Ident); ok && id.Name != "_" {
						vm.set(id.Name, i, local)
					}
				}
				if st.Value != nil {
					if id, ok := st.Value.(*ast.Ident); ok && id.Name != "_" {
						vm.set(id.Name, s.Data[i], local)
					}
				}
				c, err := vm.evalStmt(st.Body, local)
				if err != nil {
					return controlFlow{}, err
				}
				switch c.kind {
				case controlBreak:
					return controlFlow{}, nil
				case controlReturn:
					return c, nil
				case controlContinue:
				}
			}
		case *MapVal:
			for _, hk := range keysOfMap(s) {
				key := s.Keys[hk]
				val := s.Data[hk]
				if st.Key != nil {
					if id, ok := st.Key.(*ast.Ident); ok && id.Name != "_" {
						vm.set(id.Name, key, local)
					}
				}
				if st.Value != nil {
					if id, ok := st.Value.(*ast.Ident); ok && id.Name != "_" {
						vm.set(id.Name, val, local)
					}
				}
				c, err := vm.evalStmt(st.Body, local)
				if err != nil {
					return controlFlow{}, err
				}
				switch c.kind {
				case controlBreak:
					return controlFlow{}, nil
				case controlReturn:
					return c, nil
				case controlContinue:
				}
			}
		case string:
			for i := 0; i < len(s); i++ {
				if st.Key != nil {
					if id, ok := st.Key.(*ast.Ident); ok && id.Name != "_" {
						vm.set(id.Name, i, local)
					}
				}
				if st.Value != nil {
					if id, ok := st.Value.(*ast.Ident); ok && id.Name != "_" {
						vm.set(id.Name, int(s[i]), local)
					}
				}
				c, err := vm.evalStmt(st.Body, local)
				if err != nil {
					return controlFlow{}, err
				}
				switch c.kind {
				case controlBreak:
					return controlFlow{}, nil
				case controlReturn:
					return c, nil
				case controlContinue:
				}
			}
		case *ChannelVal:
			for {
				v, open, err := s.Receive(vm.Context())
				if err != nil {
					return controlFlow{}, err
				}
				if !open {
					break
				}
				if st.Key != nil {
					if id, ok := st.Key.(*ast.Ident); ok && id.Name != "_" {
						vm.set(id.Name, v, local)
					}
				}
				c, err := vm.evalStmt(st.Body, local)
				if err != nil {
					return controlFlow{}, err
				}
				switch c.kind {
				case controlBreak:
					return controlFlow{}, nil
				case controlReturn:
					return c, nil
				case controlContinue:
				}
			}
		default:
			return controlFlow{}, NewRuntimeError("range over unsupported type")
		}
		return controlFlow{}, nil

	case *ast.SwitchStmt:
		local := NewEnv(env)
		if st.Init != nil {
			if _, err := vm.evalStmt(st.Init, local); err != nil {
				return controlFlow{}, err
			}
		}
		var tag any
		var err error
		if st.Tag != nil {
			tag, err = vm.evalExpr(st.Tag, local)
			if err != nil {
				return controlFlow{}, err
			}
		}
		matched := false
		for _, clause := range st.Body.List {
			cc := clause.(*ast.CaseClause)
			if cc.List == nil {
				if !matched {
					return vm.evalStmt(&ast.BlockStmt{List: cc.Body}, local)
				}
				continue
			}
			if matched {
				continue
			}
			for _, ce := range cc.List {
				val, err := vm.evalExpr(ce, local)
				if err != nil {
					return controlFlow{}, err
				}
				if st.Tag == nil {
					if ToBool(val) {
						matched = true
						break
					}
				} else {
					if equals(tag, val) {
						matched = true
						break
					}
				}
			}
			if matched {
				return vm.evalStmt(&ast.BlockStmt{List: cc.Body}, local)
			}
		}
		return controlFlow{}, nil

	case *ast.DeferStmt:
		// Capture callable and its arguments NOW, but execute on function return/panic.
		fn, recv, args, err := vm.prepareCall(st.Call, env)
		if err != nil {
			return controlFlow{}, err
		}
		frame := env.frame
		if frame == nil {
			return controlFlow{}, NewRuntimeError("defer outside of function")
		}
		frame.defers = append(frame.defers, func() {
			_, _ = vm.callFunction(fn, env, recv, args)
		})
		return controlFlow{}, nil

	case *ast.SelectStmt:
		var rcases []reflect.SelectCase
		type selectChoice struct {
			clause     *ast.CommClause
			closed     bool
			sendClosed bool
			cancel     bool
		}
		var choices []selectChoice
		appendCase := func(rcase reflect.SelectCase, choice selectChoice) {
			rcases = append(rcases, rcase)
			choices = append(choices, choice)
		}
		for _, s2 := range st.Body.List {
			cc, ok := s2.(*ast.CommClause)
			if !ok {
				continue
			}
			if cc.Comm == nil {
				appendCase(reflect.SelectCase{Dir: reflect.SelectDefault}, selectChoice{clause: cc})
				continue
			}
			switch comm := cc.Comm.(type) {
			case *ast.ExprStmt:
				// <-ch (receive and discard)
				ue, ok2 := comm.X.(*ast.UnaryExpr)
				if !ok2 || ue.Op != token.ARROW {
					return controlFlow{}, NewRuntimeError("invalid select case expression")
				}
				chv, err := vm.evalExpr(ue.X, env)
				if err != nil {
					return controlFlow{}, err
				}
				ch, ok3 := chv.(*ChannelVal)
				if !ok3 {
					return controlFlow{}, NewRuntimeError("receive on non-channel in select")
				}
				if ch.direction == channelSendOnly {
					return controlFlow{}, NewRuntimeError("receive on send-only host channel")
				}
				appendCase(reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch.C)}, selectChoice{clause: cc})
				if ch.done != nil {
					appendCase(reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch.done)}, selectChoice{clause: cc, closed: true})
				}
			case *ast.AssignStmt:
				// v := <-ch  or  v, ok := <-ch
				if len(comm.Rhs) != 1 {
					return controlFlow{}, NewRuntimeError("invalid select assign case")
				}
				ue, ok2 := comm.Rhs[0].(*ast.UnaryExpr)
				if !ok2 || ue.Op != token.ARROW {
					return controlFlow{}, NewRuntimeError("invalid select assign case: expected <-ch")
				}
				chv, err := vm.evalExpr(ue.X, env)
				if err != nil {
					return controlFlow{}, err
				}
				ch, ok3 := chv.(*ChannelVal)
				if !ok3 {
					return controlFlow{}, NewRuntimeError("receive on non-channel in select")
				}
				if ch.direction == channelSendOnly {
					return controlFlow{}, NewRuntimeError("receive on send-only host channel")
				}
				appendCase(reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch.C)}, selectChoice{clause: cc})
				if ch.done != nil {
					appendCase(reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch.done)}, selectChoice{clause: cc, closed: true})
				}
			case *ast.SendStmt:
				// ch <- v
				chv, err := vm.evalExpr(comm.Chan, env)
				if err != nil {
					return controlFlow{}, err
				}
				val, err := vm.evalExpr(comm.Value, env)
				if err != nil {
					return controlFlow{}, err
				}
				ch, ok2 := chv.(*ChannelVal)
				if !ok2 {
					return controlFlow{}, NewRuntimeError("send on non-channel in select")
				}
				if ch.direction == channelReceiveOnly {
					return controlFlow{}, NewRuntimeError("send on receive-only host channel")
				}
				v := val // capture for reflect
				appendCase(reflect.SelectCase{
					Dir:  reflect.SelectSend,
					Chan: reflect.ValueOf(ch.C),
					Send: reflect.ValueOf(&v).Elem(), // wrap as interface{} for chan any
				}, selectChoice{clause: cc})
				if ch.done != nil {
					appendCase(reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch.done)}, selectChoice{clause: cc, sendClosed: true})
				}
			default:
				return controlFlow{}, NewRuntimeError(fmt.Sprintf("unsupported select comm: %T", comm))
			}
		}
		if len(rcases) == 0 {
			return controlFlow{}, nil
		}
		// Cancellation is an additional select arm, so a program blocked in
		// select observes a deadline or Kill immediately.
		appendCase(reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(vm.Context().Done())}, selectChoice{cancel: true})
		chosen, recvVal, recvOK, selectErr := safeReflectSelect(rcases)
		if selectErr != nil {
			return controlFlow{}, selectErr
		}
		choice := choices[chosen]
		if choice.cancel {
			return controlFlow{}, vm.executionError()
		}
		if choice.sendClosed {
			return controlFlow{}, NewRuntimeError("send on closed host channel")
		}
		cc := choice.clause
		if choice.closed {
			recvVal = reflect.Value{}
			recvOK = false
		}
		caseEnv := NewEnv(env)
		// Bind received value(s) if the chosen case was a receive assignment.
		if assign, ok := cc.Comm.(*ast.AssignStmt); ok {
			var rv any
			if recvVal.IsValid() {
				rv = recvVal.Interface()
			}
			bindVar := func(name string, val any) {
				if name == "_" {
					return
				}
				if assign.Tok == token.DEFINE {
					vm.declare(name, val, caseEnv)
				} else {
					vm.set(name, val, caseEnv)
				}
			}
			if len(assign.Lhs) >= 1 {
				if id, ok2 := assign.Lhs[0].(*ast.Ident); ok2 {
					bindVar(id.Name, rv)
				}
			}
			if len(assign.Lhs) >= 2 {
				if id, ok2 := assign.Lhs[1].(*ast.Ident); ok2 {
					bindVar(id.Name, recvOK)
				}
			}
		}
		for _, s2 := range cc.Body {
			c, err := vm.evalStmt(s2, caseEnv)
			if err != nil {
				return controlFlow{}, err
			}
			switch c.kind {
			case controlReturn, controlBreak, controlContinue:
				return c, nil
			}
		}
		return controlFlow{}, nil

	case *ast.GoStmt:
		fn, recv, args, err := vm.prepareCall(st.Call, env)
		if err != nil {
			return controlFlow{}, err
		}
		exec := vm.execution.Load()
		if exec == nil {
			return controlFlow{}, NewRuntimeError("go outside execution")
		}
		if err := exec.reserveGoroutine(); err != nil {
			return controlFlow{}, err
		}
		exec.wg.Add(1)
		go func() {
			defer exec.wg.Done()
			defer exec.releaseGoroutine()
			vm.emitTrace("goroutine_start", fn.Name, "", st)
			defer vm.emitTrace("goroutine_end", fn.Name, "", st)
			_, _ = vm.callFunction(fn, vm.globals, recv, args)
		}()
		return controlFlow{}, nil

	case *ast.ReturnStmt:
		if len(st.Results) == 0 {
			return controlFlow{kind: controlReturn, val: nil}, nil
		}
		v, err := vm.evalExpr(st.Results[0], env)
		if err != nil {
			return controlFlow{}, err
		}
		return controlFlow{kind: controlReturn, val: v}, nil

	case *ast.BranchStmt:
		switch st.Tok {
		case token.BREAK:
			return controlFlow{kind: controlBreak}, nil
		case token.CONTINUE:
			return controlFlow{kind: controlContinue}, nil
		}
		return controlFlow{}, nil

	default:
		return controlFlow{}, NewRuntimeError(fmt.Sprintf("unsupported stmt: %T", s))
	}
}

func keysOfMap(m *MapVal) []string {
	out := make([]string, 0, len(m.Keys))
	for k := range m.Keys {
		out = append(out, k)
	}
	return out
}

func (vm *Interpreter) resolveRef(l ast.Expr, env *Env) (Ref, error) {
	switch ee := l.(type) {
	case *ast.Ident:
		return &varRef{vm: vm, env: env, name: ee.Name}, nil
	case *ast.IndexExpr:
		x, err := vm.evalExpr(ee.X, env)
		if err != nil {
			return nil, err
		}
		i, err := vm.evalExpr(ee.Index, env)
		if err != nil {
			return nil, err
		}
		switch s := x.(type) {
		case *SliceVal:
			ii := ToInt(i)
			if ii < 0 || ii >= len(s.Data) {
				return nil, NewRuntimeError("index out of range")
			}
			return &sliceIndexRef{s: s, i: ii}, nil
		case *MapVal:
			return &mapIndexRef{m: s, k: i}, nil
		default:
			return nil, NewRuntimeError("index assign unsupported")
		}
	case *ast.SelectorExpr:
		recv, err := vm.evalExpr(ee.X, env)
		if err != nil {
			return nil, err
		}
		sv, ok := recv.(*StructVal)
		if !ok {
			return nil, NewRuntimeError("selector assign unsupported")
		}
		return &fieldRef{s: sv, name: ee.Sel.Name}, nil
	default:
		return nil, NewRuntimeError("invalid lvalue")
	}
}

func (vm *Interpreter) callFunction(fn *Function, env *Env, recv *any, args []any) (ret any, err error) {
	if err := vm.executionError(); err != nil {
		return nil, err
	}
	vm.emitTrace("call_start", fn.Name, "", nil)
	// Run defers in LIFO order on exit; also handle panic unwinding.
	// caller: env.frame is the call site's own active frame (nil at the
	// outermost call), letting debug.Stack() walk this chain later.
	frame := &callFrame{defers: []func(){}, funcName: fn.Name, caller: env.frame}
	defer func() {
		// Execute defers in reverse order
		for i := len(frame.defers) - 1; i >= 0; i-- {
			frame.defers[i]()
		}
		if r := recover(); r != nil {
			if pe, ok := r.(*panicError); ok {
				// Convert to error so callers can see panic
				err = pe
			}
		}
		message := "ok"
		if err != nil {
			message = err.Error()
		}
		vm.emitTrace("call_end", fn.Name, message, nil)
	}()

	// Native function?
	if fn.Native != nil || fn.NativeContext != nil {
		var a []any
		if recv != nil {
			a = append(a, *recv)
		}
		a = append(a, args...)
		if fn.NativeContext != nil {
			return fn.NativeContext(vm.Context(), a)
		}
		return fn.Native(a)
	}

	// User-defined function
	local := NewEnv(fn.Env)
	local.frame = frame
	argIndex := 0
	if fn.RecvName != "" && recv != nil {
		vm.declare(fn.RecvName, *recv, local)
	}
	if fn.IsVariadic && len(fn.Params) > 0 {
		// All args before the last param are regular; the rest packed into a slice.
		for i := 0; i < len(fn.Params)-1; i++ {
			if argIndex >= len(args) {
				vm.declare(fn.Params[i], nil, local)
			} else {
				vm.declare(fn.Params[i], args[argIndex], local)
			}
			argIndex++
		}
		var rest []any
		for argIndex < len(args) {
			rest = append(rest, args[argIndex])
			argIndex++
		}
		vm.declare(fn.Params[len(fn.Params)-1], &SliceVal{ElementType: "any", Data: rest}, local)
	} else {
		for _, p := range fn.Params {
			if argIndex >= len(args) {
				vm.declare(p, nil, local)
			} else {
				vm.declare(p, args[argIndex], local)
			}
			argIndex++
		}
	}

	for _, st := range fn.Body.(*ast.BlockStmt).List {
		c, err := vm.evalStmt(st, local)
		if err != nil {
			// If err is panicError, re-panic to trigger unwinding of outer defers.
			if _, ok := err.(*panicError); ok {
				panic(err)
			}
			return nil, err
		}
		switch c.kind {
		case controlReturn:
			return c.val, nil
		case controlBreak, controlContinue:
			return nil, NewRuntimeError("break/continue outside loop")
		}
	}
	return nil, nil
}

// prepareCall evaluates a CallExpr into callee and concrete argument list without invoking it.
func (vm *Interpreter) prepareCall(call *ast.CallExpr, env *Env) (*Function, *any, []any, error) {
	// Method / package / function cases similar to evalExpr(CallExpr) but do not call.
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		// Package function?
		if pid, ok := sel.X.(*ast.Ident); ok {
			// Resolve against the caller's own env, matching evalExpr's
			// CallExpr/SelectorExpr handling above.
			if v, ok := vm.get(pid.Name, env); ok {
				if p, ok := v.(*Package); ok {
					m, ok2 := vm.resolvePackageSelector(p, sel.Sel.Name)
					if !ok2 {
						return nil, nil, nil, NewRuntimeError("unknown package member")
					}
					fn, ok3 := m.(*Function)
					if !ok3 {
						return nil, nil, nil, NewRuntimeError("member not function")
					}
					args := make([]any, 0, len(call.Args))
					for _, a := range call.Args {
						v, err := vm.evalExpr(a, env)
						if err != nil {
							return nil, nil, nil, err
						}
						args = append(args, v)
					}
					return fn, nil, args, nil
				}
			}
		}
		// Method call on struct
		recv, err := vm.evalExpr(sel.X, env)
		if err != nil {
			return nil, nil, nil, err
		}
		recvType := typeOfValue(vm, recv)
		td := vm.types[recvType]
		if td == nil || td.Methods == nil {
			return nil, nil, nil, NewRuntimeError("unknown method")
		}
		fn := td.Methods[sel.Sel.Name]
		if fn == nil {
			return nil, nil, nil, NewRuntimeError("method not found")
		}
		args := make([]any, 0, len(call.Args))
		for _, a := range call.Args {
			v, err := vm.evalExpr(a, env)
			if err != nil {
				return nil, nil, nil, err
			}
			args = append(args, v)
		}
		return fn, &recv, args, nil
	}

	callee, err := vm.evalExpr(call.Fun, env)
	if err != nil {
		return nil, nil, nil, err
	}
	fn, ok := callee.(*Function)
	if !ok {
		return nil, nil, nil, NewRuntimeError("not a function")
	}
	args := make([]any, 0, len(call.Args))
	for _, a := range call.Args {
		v, err := vm.evalExpr(a, env)
		if err != nil {
			return nil, nil, nil, err
		}
		args = append(args, v)
	}
	return fn, nil, args, nil
}

// ---------------- Helpers ----------------------------------------

func (vm *Interpreter) applyBinaryOp(op token.Token, left, right any) (any, error) {
	switch op {
	case token.ADD:
		if _, ok := left.(string); ok {
			return ToString(left) + ToString(right), nil
		}
		if _, ok := right.(string); ok {
			return ToString(left) + ToString(right), nil
		}
		if _, ok := left.(float64); ok || isFloat(right) {
			return ToFloat(left) + ToFloat(right), nil
		}
		return ToInt(left) + ToInt(right), nil
	case token.SUB:
		if _, ok := left.(float64); ok || isFloat(right) {
			return ToFloat(left) - ToFloat(right), nil
		}
		return ToInt(left) - ToInt(right), nil
	case token.MUL:
		if _, ok := left.(float64); ok || isFloat(right) {
			return ToFloat(left) * ToFloat(right), nil
		}
		return ToInt(left) * ToInt(right), nil
	case token.QUO:
		if _, ok := left.(float64); ok || isFloat(right) {
			if ToFloat(right) == 0 {
				return nil, NewRuntimeError("division by zero")
			}
			return ToFloat(left) / ToFloat(right), nil
		}
		if ToInt(right) == 0 {
			return nil, NewRuntimeError("integer divide by zero")
		}
		return ToInt(left) / ToInt(right), nil
	case token.REM:
		if ToInt(right) == 0 {
			return nil, NewRuntimeError("integer divide by zero")
		}
		return ToInt(left) % ToInt(right), nil
	case token.SHL:
		return ToInt(left) << uint(ToInt(right)), nil
	case token.SHR:
		return ToInt(left) >> uint(ToInt(right)), nil
	case token.AND:
		return ToInt(left) & ToInt(right), nil
	case token.OR:
		return ToInt(left) | ToInt(right), nil
	case token.XOR:
		return ToInt(left) ^ ToInt(right), nil
	case token.AND_NOT:
		return ToInt(left) &^ ToInt(right), nil
	case token.LAND:
		return ToBool(left) && ToBool(right), nil
	case token.LOR:
		return ToBool(left) || ToBool(right), nil
	case token.EQL:
		return equals(left, right), nil
	case token.NEQ:
		return !equals(left, right), nil
	case token.LSS:
		if _, ok := left.(float64); ok || isFloat(right) {
			return ToFloat(left) < ToFloat(right), nil
		}
		return ToInt(left) < ToInt(right), nil
	case token.GTR:
		if _, ok := left.(float64); ok || isFloat(right) {
			return ToFloat(left) > ToFloat(right), nil
		}
		return ToInt(left) > ToInt(right), nil
	case token.LEQ:
		if _, ok := left.(float64); ok || isFloat(right) {
			return ToFloat(left) <= ToFloat(right), nil
		}
		return ToInt(left) <= ToInt(right), nil
	case token.GEQ:
		if _, ok := left.(float64); ok || isFloat(right) {
			return ToFloat(left) >= ToFloat(right), nil
		}
		return ToInt(left) >= ToInt(right), nil
	default:
		return nil, NewRuntimeError("unsupported binary op")
	}
}

func isFloat(v any) bool { _, ok := v.(float64); return ok }

func typeOfValue(vm *Interpreter, v any) string {
	switch x := v.(type) {
	case *StructVal:
		return x.TypeName
	case *SliceVal:
		return "[]" + x.ElementType
	case *MapVal:
		return "map"
	case *ChannelVal:
		return "chan " + x.ElementType
	case int:
		return "int"
	case float64:
		return "float64"
	case bool:
		return "bool"
	case string:
		return "string"
	case *Function:
		return "func"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func equals(a, b any) bool {
	// Handle nil explicitly to avoid surprises with typed nils.
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	switch x := a.(type) {
	case int:
		return x == ToInt(b)
	case float64:
		return x == ToFloat(b)
	case bool:
		return x == ToBool(b)
	case string:
		return x == ToString(b)
	case *StructVal:
		y, ok := b.(*StructVal)
		if !ok {
			return false
		}
		// Pointer equality first, then structural via hash key.
		if x == y {
			return true
		}
		return hashKey(a) == hashKey(b)
	case *SliceVal:
		// Slices are not comparable in Go (except to nil); use pointer equality.
		y, ok := b.(*SliceVal)
		return ok && x == y
	case *MapVal:
		y, ok := b.(*MapVal)
		return ok && x == y
	case *ChannelVal:
		y, ok := b.(*ChannelVal)
		return ok && x == y
	case *Function:
		y, ok := b.(*Function)
		return ok && x == y
	default:
		// Guard against uncomparable types (slice/map/func reaching here)
		// which would otherwise panic on the `==` operator. The reflect-based
		// comparability check below handles the common case, but we still
		// defer-recover defensively because user code could in theory smuggle
		// in values whose runtime kind disagrees with reported comparability
		// (e.g. interface boxing of a struct that embeds a slice). For this
		// interpreter the semantically correct answer in those edge cases is
		// "not equal" rather than aborting the user's program. Any panic
		// shape that *isn't* a comparison panic would still be unusual here,
		// but swallowing it is preferable to crashing the playground.
		defer func() { _ = recover() }()
		ra, rb := reflect.ValueOf(a), reflect.ValueOf(b)
		if !ra.IsValid() || !rb.IsValid() {
			return false
		}
		if !ra.Type().Comparable() || !rb.Type().Comparable() {
			return false
		}
		return a == b
	}
}
