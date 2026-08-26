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
		exec.finish()
		exec.wg.Wait()
		err = exec.finalError(err)
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
					case *ast.InterfaceType:
						// No implementation to store here — see
						// TypeDef.InterfaceMethods — so a type assertion
						// against this name checks a candidate value's own
						// TypeDef.Methods instead (evalTypeAssert).
						vm.types[ts.Name.Name] = &TypeDef{Name: ts.Name.Name, Kind: "interface", InterfaceMethods: interfaceMethodNames(tt)}
					default:
						underlying := typeString(tt)
						if isBuiltinType(underlying) {
							// Keep named scalar types useful for firmware-style code
							// without introducing a second runtime representation.
							vm.types[ts.Name.Name] = &TypeDef{Name: ts.Name.Name, Kind: "alias", Underlying: underlying}
						}
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
							val = vm.zeroValueForType(typeString(vs.Type))
						}
						vm.declare(name.Name, val, global)
						if vm.trackingVariables() {
							vm.recordVariable(name.Name, val, name, global)
						}
					}
				}
			}
		case *ast.FuncDecl:
			frameFree, needsFrames, envReusable := analyzeFunctionMetadata(d.Body)
			if needsFrames {
				vm.stackFramesRequired.Store(true)
				if exec.fastCallsAllowed {
					exec.fastCallsAllowed = false
				}
			}
			fn := &Function{Name: d.Name.Name, Body: d.Body, Env: global, frameFree: frameFree, envReusable: envReusable}
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
			fn.Results = namedResults(d.Type.Results)
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
			if n, ok := parseFastDecimalInt(ex.Value); ok {
				return n, nil
			}
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
		if td, ok := vm.types[ex.Name]; ok {
			if td.Kind == "alias" {
				return &Function{Name: ex.Name, Native: func(args []any) (any, error) {
					if len(args) == 0 {
						return vm.zeroValueForType(ex.Name), nil
					}
					return vm.coerceToType(args[0], ex.Name), nil
				}}, nil
			}
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
			case "recover":
				// Matches Go's "recover must be called directly by a
				// deferred function": env.frame.caller is only the
				// panicking frame when THIS call is itself running as one
				// of that frame's deferred calls (see callFunction and the
				// DeferStmt case) — a helper function called from within
				// the deferred function has its own, non-panicking caller,
				// so recover() there correctly sees nothing to recover.
				if cur := env.frame; cur != nil && cur.caller != nil && cur.caller.panicking {
					v := cur.caller.panicVal
					cur.caller.panicking = false
					cur.caller.panicVal = nil
					return v, nil
				}
				return nil, nil
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
							args = make([]any, len(ex.Args))
							for i, a := range ex.Args {
								v, err := vm.evalExpr(a, env)
								if err != nil {
									return nil, err
								}
								args[i] = v
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
			args := make([]any, 1+len(ex.Args))
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
						args[i+1] = v
					}
				}
			} else {
				for i, a := range ex.Args {
					v, err := vm.evalExpr(a, env)
					if err != nil {
						return nil, err
					}
					args[i+1] = v
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
				args = make([]any, len(ex.Args))
				for i, a := range ex.Args {
					v, err := vm.evalExpr(a, env)
					if err != nil {
						return nil, err
					}
					args[i] = v
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
				// A real Go index panic is recoverable, so this must go
				// through the same *panicError channel as a guest panic()
				// call — unlike "indexing unsupported" below, which
				// reflects an operation Go's type checker would reject at
				// compile time and so stays a plain, non-recoverable error.
				return nil, &panicError{value: fmt.Sprintf("runtime error: index out of range [%d] with length %d", ii, len(t.Data))}
			}
			return t.Data[ii], nil
		case *MapVal:
			val, _ := t.getByKey(i)
			return val, nil
		case string:
			idx := ToInt(i)
			if idx < 0 || idx >= len(t) {
				return nil, &panicError{value: fmt.Sprintf("runtime error: index out of range [%d] with length %d", idx, len(t))}
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
				return nil, &panicError{value: fmt.Sprintf("runtime error: slice bounds out of range [%d:%d]", lo, hi)}
			}
			return &SliceVal{ElementType: s.ElementType, Data: s.Data[lo:hi]}, nil
		case string:
			if hi < 0 || hi > len(s) {
				hi = len(s)
			}
			if lo < 0 || lo > hi {
				return nil, &panicError{value: fmt.Sprintf("runtime error: slice bounds out of range [%d:%d]", lo, hi)}
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
		if strings.HasPrefix(typ, "[]") || strings.HasPrefix(typ, "[") {
			elem := ""
			length := 0
			fixed := false
			if strings.HasPrefix(typ, "[]") {
				elem = typ[2:]
			} else {
				var ok bool
				length, elem, ok = parseArrayType(typ)
				if !ok {
					return nil, NewRuntimeError("array literal requires a constant length")
				}
				fixed = true
			}
			data := make([]any, 0, len(ex.Elts))
			if fixed {
				data = make([]any, length)
				for i := range data {
					data[i] = vm.zeroValueForType(elem)
				}
			}
			next := 0
			for _, elt := range ex.Elts {
				index := next
				valueExpr := elt
				if keyed, ok := elt.(*ast.KeyValueExpr); ok {
					indexValue, err := vm.evalExpr(keyed.Key, env)
					if err != nil {
						return nil, err
					}
					index = ToInt(indexValue)
					valueExpr = keyed.Value
				}
				v, err := vm.evalExpr(valueExpr, env)
				if err != nil {
					return nil, err
				}
				if fixed {
					if index < 0 || index >= len(data) {
						return nil, NewRuntimeError("array literal index out of bounds")
					}
					data[index] = vm.coerceToType(v, elem)
				} else {
					for len(data) <= index {
						data = append(data, vm.zeroValueForType(elem))
					}
					data[index] = vm.coerceToType(v, elem)
				}
				next = index + 1
			}
			return &SliceVal{ElementType: elem, Data: data, Fixed: fixed}, nil
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
			obj.Fields[f.Name] = vm.zeroValueForType(f.Type)
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

	case *ast.TypeAssertExpr:
		// ex.Type is nil only for the bare `x.(type)` guard of a type-switch
		// statement, which never reaches here: nanoGo has no TypeSwitchStmt
		// evaluator case, so that AST shape is never evaluated as a plain
		// expression.
		v, dyn, ok, err := vm.evalTypeAssert(ex, env)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, &panicError{value: interfaceConversionMessage(vm, ex.Type, dyn)}
		}
		return v, nil

	case *ast.FuncLit:
		frameFree, _, envReusable := analyzeFunctionMetadata(ex.Body)
		fn := &Function{Name: "<anon>", Body: ex.Body, Env: env, frameFree: frameFree, envReusable: envReusable}
		if ex.Type.Params != nil {
			for _, f := range ex.Type.Params.List {
				for _, n := range f.Names {
					fn.Params = append(fn.Params, n.Name)
				}
			}
		}
		fn.Results = namedResults(ex.Type.Results)
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
		if n, ok := parseFastDecimalInt(ex.Value); ok {
			return n, true, nil
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
				// A recoverable Go runtime panic, like applyBinaryOp's QUO/REM
				// cases below — no attachRuntimeErrorLocation call here since
				// *panicError carries no source location, matching every
				// other guest panic (a plain panic("msg") has none either).
				return 0, true, &panicError{value: "runtime error: integer divide by zero"}
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

// parseFastDecimalInt handles the ordinary non-zero-prefixed decimal form
// without a map lookup or strconv call. The parser has already validated the
// token; the conservative fallback preserves strconv's exact handling for
// bases, separators, leading-zero literals, and overflow diagnostics.
func parseFastDecimalInt(s string) (int, bool) {
	if len(s) == 0 || (len(s) > 1 && s[0] == '0') {
		return 0, false
	}
	maxInt := int(^uint(0) >> 1)
	n := 0
	for i := 0; i < len(s); i++ {
		digit := s[i] - '0'
		if digit > 9 || n > (maxInt-int(digit))/10 {
			return 0, false
		}
		n = n*10 + int(digit)
	}
	return n, true
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
	controlFallthrough
	controlGoto
)

// label carries the target of a labeled break/continue (matched against a
// loop/switch/select's own label, see evalForStmt et al.) or, for
// controlGoto, the name of the label being jumped to (matched against
// *ast.LabeledStmt entries by execStmtList). It is empty for the common
// unlabeled case.
type controlFlow struct {
	kind  controlKind
	val   any
	label string
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
//
// A label doesn't change where its statement declares into — `Loop: x :=
// i` still declares x into this block, not some scope of Loop's own (labels
// aren't scopes) — so it unwraps through unwrapLabel first.
func blockNeedsOwnScope(block *ast.BlockStmt) bool {
	for _, s := range block.List {
		switch s := unwrapLabel(s).(type) {
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

// unwrapLabel peels off any (possibly stacked) *ast.LabeledStmt wrappers
// and returns the real statement underneath, since a label changes nothing
// about what its statement does or where it declares.
func unwrapLabel(s ast.Stmt) ast.Stmt {
	for {
		ls, ok := s.(*ast.LabeledStmt)
		if !ok {
			return s
		}
		s = ls.Stmt
	}
}

// execStmtList runs stmts in order within env and is the single place that
// understands goto: Go only allows a goto to target a label in the same or
// an enclosing block, so resolving one is a matter of each statement list
// checking, on its own top-level statements, whether it defines the label a
// stray controlGoto is looking for. If it does, execution restarts at that
// label's index (this also covers backward jumps and re-entering a loop
// from before it); if not, the controlGoto is handed to the caller
// unchanged so an enclosing execStmtList (or callFunction, for the
// outermost function-body list) gets the same chance.
//
// findLabel only runs on the goto path, so a statement list with no gotos
// pays nothing beyond the plain sequential loop it already needed.
func (vm *Interpreter) execStmtList(stmts []ast.Stmt, env *Env) (controlFlow, error) {
	i := 0
	for i < len(stmts) {
		c, err := vm.evalStmt(stmts[i], env)
		if err != nil {
			return controlFlow{}, err
		}
		if c.kind == controlGoto {
			if idx, ok := findLabel(stmts, c.label); ok {
				// The restarted region (from the label to wherever
				// execution goes next, so conservatively everything from
				// here to the end of this list) reuses env rather than
				// getting a fresh scope the way a real loop iteration
				// would (see blockNeedsOwnScope) — so any name it
				// redeclares via := or var/const must first forget its
				// previous binding, or validateShortDecl sees it as
				// already bound and rejects the legitimate redeclaration.
				for _, name := range declaredNames(stmts[idx:]) {
					vm.undeclare(name, env)
				}
				i = idx
				continue
			}
			return c, nil
		}
		switch c.kind {
		case controlReturn, controlBreak, controlContinue, controlFallthrough:
			return c, nil
		}
		i++
	}
	return controlFlow{}, nil
}

// declaredNames collects the names that stmts would bind directly via :=
// or var/const — the same statement kinds and scope blockNeedsOwnScope
// inspects (top-level only; a nested block's own declarations are none of
// this list's concern), but returning what they declare instead of just
// whether any of them do.
func declaredNames(stmts []ast.Stmt) []string {
	var names []string
	for _, raw := range stmts {
		switch s := unwrapLabel(raw).(type) {
		case *ast.AssignStmt:
			if s.Tok == token.DEFINE {
				for _, l := range s.Lhs {
					if id, ok := l.(*ast.Ident); ok && id.Name != "_" {
						names = append(names, id.Name)
					}
				}
			}
		case *ast.DeclStmt:
			gd, ok := s.Decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, n := range vs.Names {
					if n.Name != "_" {
						names = append(names, n.Name)
					}
				}
			}
		}
	}
	return names
}

// findLabel returns the index within stmts of the *ast.LabeledStmt named
// label, if stmts declares one directly (not inside a nested block).
func findLabel(stmts []ast.Stmt, label string) (int, bool) {
	for i, s := range stmts {
		if ls, ok := s.(*ast.LabeledStmt); ok && ls.Label.Name == label {
			return i, true
		}
	}
	return 0, false
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
	if set := vm.breakpoints.Load(); set != nil {
		if line := vm.traceLocation(s.Pos()).Line; line > 0 {
			if _, ok := set.lines[line]; ok {
				function := "program"
				if env != nil && env.frame != nil && env.frame.funcName != "" {
					function = env.frame.funcName
				}
				vm.emitTrace("breakpoint", function, "breakpoint hit", s)
			}
		}
	}
	if p := vm.lineProfile.Load(); p != nil {
		p.hit(vm.traceLocation(s.Pos()).Line)
	}
	if dc := vm.debugController.Load(); dc != nil {
		if err := dc.checkpoint(vm, s, env); err != nil {
			return controlFlow{}, err
		}
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
		// Go's short declaration reuses names already present in the current
		// scope, shadows names from outer scopes, and requires at least one new
		// non-blank name. Validate before evaluating the RHS so an invalid
		// declaration has the same no-side-effects behaviour as a Go compiler
		// rejection.
		if st.Tok == token.DEFINE {
			if err := vm.validateShortDecl(st.Lhs, env); err != nil {
				return controlFlow{}, err
			}
		}

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
						if vm.trackingVariables() {
							vm.recordVariable(id.Name, n, id, env)
						}
						return controlFlow{}, nil
					}
				case token.ASSIGN:
					n, intOK, err := vm.tryEvalIntExpr(st.Rhs[0], env, true)
					if err != nil {
						return controlFlow{}, err
					}
					if intOK && vm.setInt(id.Name, n, env) {
						if vm.trackingVariables() {
							vm.recordVariable(id.Name, n, id, env)
						}
						return controlFlow{}, nil
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
			// Special case: v, ok := x.(T)
			// The comma-ok form never panics on a failed assertion — that
			// only happens for the single-result form, handled directly in
			// evalExprNode's *ast.TypeAssertExpr case.
			if len(st.Lhs) == 2 && len(st.Rhs) == 1 {
				if tae, isAssert := r.(*ast.TypeAssertExpr); isAssert {
					v, _, ok2, err := vm.evalTypeAssert(tae, env)
					if err != nil {
						return controlFlow{}, err
					}
					rightVals = []any{v, ok2}
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
				if vm.trackingVariables() {
					vm.recordVariable(id.Name, v, id, env)
				}
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
					if vm.trackingVariables() {
						vm.recordVariable(id.Name, v, id, env)
					}
					continue
				}
				ref, err := vm.resolveRef(l, env)
				if err != nil {
					return controlFlow{}, err
				}
				if err := ref.Set(v); err != nil {
					return controlFlow{}, err
				}
				if vm.trackingVariables() {
					vm.recordAssignedExpression(l, v, env)
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
				if vm.trackingVariables() {
					vm.recordVariable(id.Name, newVal, id, env)
				}
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
			if vm.trackingVariables() {
				vm.recordAssignedExpression(st.Lhs[0], newVal, env)
			}
		}
		return controlFlow{}, nil

	case *ast.IncDecStmt:
		if id, ok := st.X.(*ast.Ident); ok {
			delta := -1
			if st.Tok == token.INC {
				delta = 1
			}
			if cur, ok := vm.addInt(id.Name, delta, env); ok {
				if vm.trackingVariables() {
					vm.recordVariable(id.Name, cur, id, env)
				}
				return controlFlow{}, nil
			}
			v, _ := vm.get(id.Name, env)
			cur := ToInt(v)
			if st.Tok == token.INC {
				vm.set(id.Name, cur+1, env)
				if vm.trackingVariables() {
					vm.recordVariable(id.Name, cur+1, id, env)
				}
			} else {
				vm.set(id.Name, cur-1, env)
				if vm.trackingVariables() {
					vm.recordVariable(id.Name, cur-1, id, env)
				}
			}
			return controlFlow{}, nil
		}
		ref, err := vm.resolveRef(st.X, env)
		if err != nil {
			return controlFlow{}, err
		}
		cur := ToInt(ref.Get())
		if st.Tok == token.INC {
			if err := ref.Set(cur + 1); err != nil {
				return controlFlow{}, err
			}
			if vm.trackingVariables() {
				vm.recordAssignedExpression(st.X, cur+1, env)
			}
		} else {
			if err := ref.Set(cur - 1); err != nil {
				return controlFlow{}, err
			}
			if vm.trackingVariables() {
				vm.recordAssignedExpression(st.X, cur-1, env)
			}
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
							v = vm.coerceToType(v, typeString(vs.Type))
						}
						val = v
					} else {
						val = vm.zeroValueForType(typeString(vs.Type))
					}
					vm.declare(n.Name, val, env)
					if vm.trackingVariables() {
						vm.recordVariable(n.Name, val, n, env)
					}
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
		return vm.execStmtList(st.List, local)

	case *ast.LabeledStmt:
		// Stacked labels on the same loop (`A:\nB:\nfor {...}`) are valid Go
		// but vanishingly rare in practice: only the innermost label ends up
		// recognized by evalForStmt et al. for break/continue in that case.
		// goto is unaffected either way — execStmtList's findLabel matches
		// any *ast.LabeledStmt in the list regardless of nesting depth.
		switch inner := st.Stmt.(type) {
		case *ast.ForStmt:
			return vm.evalForStmt(inner, env, st.Label.Name)
		case *ast.RangeStmt:
			return vm.evalRangeStmt(inner, env, st.Label.Name)
		case *ast.SwitchStmt:
			return vm.evalSwitchStmt(inner, env, st.Label.Name)
		case *ast.SelectStmt:
			return vm.evalSelectStmt(inner, env, st.Label.Name)
		default:
			// A label on a plain statement is only meaningful as a goto
			// target; execStmtList already finds it by scanning the
			// enclosing list, so evaluating it here just runs the statement.
			return vm.evalStmt(st.Stmt, env)
		}

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
		return vm.evalForStmt(st, env, "")

	case *ast.RangeStmt:
		return vm.evalRangeStmt(st, env, "")

	case *ast.SwitchStmt:
		return vm.evalSwitchStmt(st, env, "")

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
			// callFunction never lets a panic escape natively (see its own
			// defer/recover): a deferred call that itself panics without
			// recovering comes back as a *panicError here, which becomes
			// (or replaces) frame's active panic — exactly like a panic
			// raised by a deferred function in real Go.
			if _, derr := vm.callFunction(fn, env, recv, args); derr != nil {
				if pe, ok := derr.(*panicError); ok {
					frame.panicking = true
					frame.panicVal = pe.value
				}
			}
		})
		return controlFlow{}, nil

	case *ast.SelectStmt:
		return vm.evalSelectStmt(st, env, "")

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
			_, workerErr := vm.callFunction(fn, vm.globals, recv, args)
			exec.recordWorkerError(workerErr)
		}()
		return controlFlow{}, nil

	case *ast.ReturnStmt:
		// namedResult is "" for a function with zero or 2+ named results —
		// see callFrame.namedResult — in which case this behaves exactly
		// as before.
		namedResult := ""
		if env.frame != nil {
			namedResult = env.frame.namedResult
		}
		if len(st.Results) == 0 {
			// A naked return with a named result yields whatever that
			// variable currently holds (Go: `return` is `return name`).
			var v any
			if namedResult != "" {
				v, _ = vm.get(namedResult, env)
			}
			return controlFlow{kind: controlReturn, val: v}, nil
		}
		v, err := vm.evalExpr(st.Results[0], env)
		if err != nil {
			return controlFlow{}, err
		}
		if namedResult != "" {
			// `return expr` with a named result is `name = expr; return`:
			// the assignment matters even though c.val already carries expr's
			// value, because a defer running afterward (callFunction's
			// closure) re-reads name as the final word on what's returned,
			// and would otherwise clobber this with name's stale prior value.
			vm.set(namedResult, v, env)
		}
		return controlFlow{kind: controlReturn, val: v}, nil

	case *ast.BranchStmt:
		label := ""
		if st.Label != nil {
			label = st.Label.Name
		}
		switch st.Tok {
		case token.BREAK:
			return controlFlow{kind: controlBreak, label: label}, nil
		case token.CONTINUE:
			return controlFlow{kind: controlContinue, label: label}, nil
		case token.FALLTHROUGH:
			return controlFlow{kind: controlFallthrough}, nil
		case token.GOTO:
			return controlFlow{kind: controlGoto, label: label}, nil
		}
		return controlFlow{}, nil

	case *ast.EmptyStmt:
		// A label with nothing after it — `done:\n}` — parses as a
		// LabeledStmt wrapping an EmptyStmt (Go's grammar requires a
		// statement after a label, and an empty one satisfies that). This
		// is the common goto-to-exit idiom's trailing target, so it must
		// be a legitimate no-op rather than an error.
		return controlFlow{}, nil

	default:
		return controlFlow{}, NewRuntimeError(fmt.Sprintf("unsupported stmt: %T", s))
	}
}

// evalForStmt evaluates a (possibly labeled) for statement. label is ""
// when the loop has no label. A labeled break/continue whose label doesn't
// match this loop's own must not be treated as targeting it: the loop
// terminates (as if broken) and re-propagates the same controlFlow so an
// enclosing loop bearing that label can act on it — see the package
// comment on controlFlow.label.
func (vm *Interpreter) evalForStmt(st *ast.ForStmt, env *Env, label string) (controlFlow, error) {
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
			if c.label == "" || c.label == label {
				return controlFlow{}, nil
			}
			return c, nil
		case controlReturn, controlGoto:
			return c, nil
		case controlContinue:
			if c.label != "" && c.label != label {
				return c, nil
			}
			// ours (or unlabeled): fall through to run Post and loop again
		}
		if st.Post != nil {
			if _, err := vm.evalStmt(st.Post, local); err != nil {
				return controlFlow{}, err
			}
		}
	}
	return controlFlow{}, nil
}

// evalRangeStmt evaluates a (possibly labeled) range statement; see
// evalForStmt for the label semantics, which are identical here.
func (vm *Interpreter) evalRangeStmt(st *ast.RangeStmt, env *Env, label string) (controlFlow, error) {
	local := NewEnv(env)
	x, err := vm.evalExpr(st.X, local)
	if err != nil {
		return controlFlow{}, err
	}
	// handleBody runs the loop body and translates its controlFlow into what
	// the range loop over any of the container kinds below should do:
	// "stop=true" means the whole range statement is done evaluating (its
	// own return value is c/err), "stop=false" means keep ranging.
	handleBody := func(body *ast.BlockStmt) (c controlFlow, err error, stop bool) {
		c, err = vm.evalStmt(body, local)
		if err != nil {
			return controlFlow{}, err, true
		}
		switch c.kind {
		case controlBreak:
			if c.label == "" || c.label == label {
				return controlFlow{}, nil, true
			}
			return c, nil, true
		case controlReturn, controlGoto:
			return c, nil, true
		case controlContinue:
			if c.label != "" && c.label != label {
				return c, nil, true
			}
		}
		return controlFlow{}, nil, false
	}
	switch s := x.(type) {
	case *SliceVal:
		for i := 0; i < len(s.Data); i++ {
			if st.Key != nil {
				if id, ok := st.Key.(*ast.Ident); ok && id.Name != "_" {
					vm.set(id.Name, i, local)
					if vm.trackingVariables() {
						vm.recordVariable(id.Name, i, id, local)
					}
				}
			}
			if st.Value != nil {
				if id, ok := st.Value.(*ast.Ident); ok && id.Name != "_" {
					vm.set(id.Name, s.Data[i], local)
					if vm.trackingVariables() {
						vm.recordVariable(id.Name, s.Data[i], id, local)
					}
				}
			}
			if c, err, stop := handleBody(st.Body); stop {
				return c, err
			}
		}
	case *MapVal:
		for hk, key := range s.Keys {
			val := s.Data[hk]
			if st.Key != nil {
				if id, ok := st.Key.(*ast.Ident); ok && id.Name != "_" {
					vm.set(id.Name, key, local)
					if vm.trackingVariables() {
						vm.recordVariable(id.Name, key, id, local)
					}
				}
			}
			if st.Value != nil {
				if id, ok := st.Value.(*ast.Ident); ok && id.Name != "_" {
					vm.set(id.Name, val, local)
					if vm.trackingVariables() {
						vm.recordVariable(id.Name, val, id, local)
					}
				}
			}
			if c, err, stop := handleBody(st.Body); stop {
				return c, err
			}
		}
	case string:
		// Go ranges over UTF-8 strings by byte offset and decoded rune, not
		// by individual bytes. This matters for the Unicode-heavy display
		// and serial protocols commonly used by TinyGo targets as well.
		for i, r := range s {
			if st.Key != nil {
				if id, ok := st.Key.(*ast.Ident); ok && id.Name != "_" {
					vm.set(id.Name, i, local)
					if vm.trackingVariables() {
						vm.recordVariable(id.Name, i, id, local)
					}
				}
			}
			if st.Value != nil {
				if id, ok := st.Value.(*ast.Ident); ok && id.Name != "_" {
					vm.set(id.Name, int(r), local)
					if vm.trackingVariables() {
						vm.recordVariable(id.Name, int(r), id, local)
					}
				}
			}
			if c, err, stop := handleBody(st.Body); stop {
				return c, err
			}
		}
	case int:
		// Go 1.22 added `for i := range n`. It is a compact, allocation-free
		// loop form that maps well to firmware-style TinyGo code.
		for i := 0; i < s; i++ {
			if st.Key != nil {
				if id, ok := st.Key.(*ast.Ident); ok && id.Name != "_" {
					vm.set(id.Name, i, local)
					if vm.trackingVariables() {
						vm.recordVariable(id.Name, i, id, local)
					}
				}
			}
			if c, err, stop := handleBody(st.Body); stop {
				return c, err
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
					if vm.trackingVariables() {
						vm.recordVariable(id.Name, v, id, local)
					}
				}
			}
			if c, err, stop := handleBody(st.Body); stop {
				return c, err
			}
		}
	default:
		return controlFlow{}, NewRuntimeError("range over unsupported type")
	}
	return controlFlow{}, nil
}

// evalSwitchStmt evaluates a (possibly labeled) switch statement; only
// break (not continue) can target a switch's own label, matching Go.
func (vm *Interpreter) evalSwitchStmt(st *ast.SwitchStmt, env *Env, label string) (controlFlow, error) {
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
	// A default clause is considered only after every explicit case, even
	// when it appears before a matching case in source order.
	defaultIndex := -1
	matched := false
	for i, clause := range st.Body.List {
		cc := clause.(*ast.CaseClause)
		if !matched {
			if cc.List == nil {
				defaultIndex = i
				continue
			}
			for _, ce := range cc.List {
				val, err := vm.evalExpr(ce, local)
				if err != nil {
					return controlFlow{}, err
				}
				if (st.Tag == nil && ToBool(val)) || (st.Tag != nil && equals(tag, val)) {
					matched = true
					break
				}
			}
			if !matched {
				continue
			}
		}

		c, err := vm.evalStmt(&ast.BlockStmt{List: cc.Body}, local)
		if err != nil {
			return controlFlow{}, err
		}
		switch c.kind {
		case controlFallthrough:
			if i == len(st.Body.List)-1 {
				return controlFlow{}, NewRuntimeError("cannot fallthrough final case")
			}
			// Keep matched true: the following body runs without evaluating
			// its case expression, exactly as Go's fallthrough specifies.
			continue
		case controlBreak:
			// break exits this switch, rather than leaking to an enclosing for,
			// unless it's labeled for some other enclosing statement.
			if c.label == "" || c.label == label {
				return controlFlow{}, nil
			}
			return c, nil
		case controlReturn, controlContinue, controlGoto:
			return c, nil
		default:
			return controlFlow{}, nil
		}
	}
	if !matched && defaultIndex >= 0 {
		c, err := vm.evalStmt(&ast.BlockStmt{List: st.Body.List[defaultIndex].(*ast.CaseClause).Body}, local)
		if err != nil {
			return controlFlow{}, err
		}
		if c.kind == controlBreak && (c.label == "" || c.label == label) {
			return controlFlow{}, nil
		}
		return c, nil
	}
	return controlFlow{}, nil
}

// evalSelectStmt evaluates a (possibly labeled — label is "" when not)
// select statement. Split out of evalStmtNode so a *ast.LabeledStmt
// wrapping a select can pass its label through for `break Label`.
func (vm *Interpreter) evalSelectStmt(st *ast.SelectStmt, env *Env, label string) (controlFlow, error) {
	var rcases []reflect.SelectCase
	type selectChoice struct {
		clause     *ast.CommClause
		channel    *ChannelVal
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
				appendCase(reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch.done)}, selectChoice{clause: cc, channel: ch, closed: true})
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
				appendCase(reflect.SelectCase{Dir: reflect.SelectRecv, Chan: reflect.ValueOf(ch.done)}, selectChoice{clause: cc, channel: ch, closed: true})
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
			if channelDone(ch.done) {
				return controlFlow{}, NewRuntimeError("send on closed host channel")
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
		// Go's empty select blocks forever. A cancellable execution still
		// needs a way out so deadlines and Kill do not leak the host call.
		<-vm.Context().Done()
		return controlFlow{}, vm.cancellationError()
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
		return controlFlow{}, &panicError{value: "send on closed channel"}
	}
	cc := choice.clause
	if choice.closed {
		// HostChannel signals closure separately so the host never races a raw
		// channel close. If buffered input and done are both ready, prefer one
		// queued value to preserve ordinary Go channel receive semantics.
		select {
		case value := <-choice.channel.C:
			recvVal = reflect.ValueOf(value)
			recvOK = true
		default:
			recvVal = reflect.Value{}
			recvOK = false
		}
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
	c, err := vm.execStmtList(cc.Body, caseEnv)
	if err != nil {
		return controlFlow{}, err
	}
	switch c.kind {
	case controlBreak:
		if c.label == "" || c.label == label {
			return controlFlow{}, nil
		}
		return c, nil
	default:
		return c, nil
	}
}

// validateShortDecl implements the scope-sensitive rules for :=. The parser
// accepts constructs such as `x := 1; x := 2`; Go's type checker rejects the
// second one because it introduces no new variable, so nanoGo must reject it
// before the evaluator mutates any state.
func (vm *Interpreter) validateShortDecl(lhs []ast.Expr, env *Env) error {
	newName := false
	var seen map[string]struct{}
	if len(lhs) > 1 {
		seen = make(map[string]struct{}, len(lhs))
	}
	for _, expr := range lhs {
		id, ok := expr.(*ast.Ident)
		if !ok {
			return NewRuntimeError("invalid := lhs")
		}
		if id.Name == "_" {
			continue
		}
		if _, duplicate := seen[id.Name]; duplicate {
			return NewRuntimeError("duplicate variable in := declaration: " + id.Name)
		}
		if seen != nil {
			seen[id.Name] = struct{}{}
		}
		if !vm.hasLocalBinding(id.Name, env) {
			newName = true
		}
	}
	if !newName {
		return NewRuntimeError("no new variables on left side of :=")
	}
	return nil
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
				return nil, &panicError{value: fmt.Sprintf("runtime error: index out of range [%d] with length %d", ii, len(s.Data))}
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
	if vm.canFastCall(fn) {
		return vm.callFrameFreeFunction(fn, recv, args)
	}
	vm.emitTrace("call_start", fn.Name, "", nil)
	// Run defers in LIFO order on exit; also handle panic unwinding.
	// caller: env.frame is the call site's own active frame (nil at the
	// outermost call), letting debug.Stack() walk this chain later. It also
	// doubles as the recover() target check: a deferred call's own frame
	// has caller set to exactly this frame (see below and the DeferStmt
	// case), which is what lets recover() tell "called directly by a
	// deferred function of the panicking frame" apart from "called by
	// something that function itself called".
	frameDepth := 0
	if env.frame != nil {
		frameDepth = env.frame.depth + 1
	}
	frame := &callFrame{funcName: fn.Name, caller: env.frame, depth: frameDepth}
	if len(fn.Results) == 1 {
		frame.namedResult = fn.Results[0]
	}
	// local is declared here (rather than := further down, where it's
	// actually assigned) so the deferred closure below — which runs before
	// local's own declaration point is reached on some paths (e.g. the
	// native-function early return) but always after it's been assigned on
	// the user-defined-function path — can read it to re-fetch a named
	// result's final value. A closure may only reference a variable whose
	// declaration lexically precedes it, hence declaring the (typed nil)
	// var up here instead of a := below.
	var local *Env
	defer func() {
		// A guest panic() never reaches here as a native panic — it
		// propagates as a *panicError return value instead (see below),
		// which is far cheaper than unwinding the real Go stack for every
		// user-level panic/recover pair. This recover() is only a backstop
		// against a genuinely unexpected native panic (an interpreter bug,
		// or a host builtin that panicked outright): still fold it into the
		// same frame.panicking bookkeeping so guest defer/recover can
		// observe and handle it exactly like any other panic, instead of
		// either crashing the host or silently vanishing as a false
		// success.
		if r := recover(); r != nil {
			frame.panicking = true
			if pe, ok := r.(*panicError); ok {
				frame.panicVal = pe.value
			} else {
				frame.panicVal = fmt.Sprintf("%v", r)
			}
		}
		// Execute defers in reverse order. Each runs via callFunction,
		// which never lets a panic escape natively — one that panics
		// without recovering comes back as a *panicError that becomes (or
		// replaces) frame's active panic, same as in real Go.
		for i := len(frame.defers) - 1; i >= 0; i-- {
			frame.defers[i]()
		}
		if frame.panicking {
			err = &panicError{value: frame.panicVal}
		}
		// A single named result is the final authority on what this call
		// returns, re-read here (after every defer, including one that
		// calls recover()) rather than trusting whatever value flowed
		// through the return statement itself — matching Go, where a
		// deferred function can still change a named result after the
		// return expression already ran. local is nil on the native-
		// function path (no named-result concept there) and get() on a nil
		// Env would be invalid, hence the guard.
		if local != nil && frame.namedResult != "" {
			if v, ok := vm.get(frame.namedResult, local); ok {
				ret = v
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
	local = NewEnv(fn.Env)
	local.frame = frame
	argIndex := 0
	if fn.RecvName != "" && recv != nil {
		vm.declare(fn.RecvName, *recv, local)
		if vm.trackingVariables() {
			vm.recordVariable(fn.RecvName, *recv, nil, local)
		}
	}
	if fn.IsVariadic && len(fn.Params) > 0 {
		// All args before the last param are regular; the rest packed into a slice.
		for i := 0; i < len(fn.Params)-1; i++ {
			if argIndex >= len(args) {
				vm.declare(fn.Params[i], nil, local)
				if vm.trackingVariables() {
					vm.recordVariable(fn.Params[i], nil, nil, local)
				}
			} else {
				vm.declare(fn.Params[i], args[argIndex], local)
				if vm.trackingVariables() {
					vm.recordVariable(fn.Params[i], args[argIndex], nil, local)
				}
			}
			argIndex++
		}
		var rest []any
		for argIndex < len(args) {
			rest = append(rest, args[argIndex])
			argIndex++
		}
		restValue := &SliceVal{ElementType: "any", Data: rest}
		vm.declare(fn.Params[len(fn.Params)-1], restValue, local)
		if vm.trackingVariables() {
			vm.recordVariable(fn.Params[len(fn.Params)-1], restValue, nil, local)
		}
	} else {
		for _, p := range fn.Params {
			if argIndex >= len(args) {
				vm.declare(p, nil, local)
				if vm.trackingVariables() {
					vm.recordVariable(p, nil, nil, local)
				}
			} else {
				vm.declare(p, args[argIndex], local)
				if vm.trackingVariables() {
					vm.recordVariable(p, args[argIndex], nil, local)
				}
			}
			argIndex++
		}
	}
	// Named results start out nil — nanoGo has no per-type zero-value
	// tracking for them (consistent with a missing argument also
	// defaulting to nil above, rather than 0/""/false) — so they exist as
	// ordinary locals a naked `return` or a deferred function can read and
	// write by name.
	for _, name := range fn.Results {
		vm.declare(name, nil, local)
	}

	c, bodyErr := vm.execStmtList(fn.Body.(*ast.BlockStmt).List, local)
	if bodyErr != nil {
		if pe, ok := bodyErr.(*panicError); ok {
			// Record the panic on frame and fall through to a normal
			// return: the deferred closure above runs on any return path,
			// sees frame.panicking, and re-raises it as err (unless a
			// guest defer calls recover() and clears it first).
			frame.panicking = true
			frame.panicVal = pe.value
			return nil, nil
		}
		return nil, bodyErr
	}
	switch c.kind {
	case controlReturn:
		return c.val, nil
	case controlBreak, controlContinue:
		return nil, NewRuntimeError("break/continue outside loop")
	case controlGoto:
		return nil, NewRuntimeError("goto " + c.label + ": label not found")
	}
	return nil, nil
}

// canFastCall identifies the normal production case: a parsed,
// non-variadic guest function with no defers, recovery, stack inspection,
// tracing, debugger, or variable tracker attached. Such a call cannot
// observe a callFrame, so allocating one (and its defer/recover closure)
// merely adds GC work to recursive and call-heavy programs.
func (vm *Interpreter) canFastCall(fn *Function) bool {
	exec := vm.activeExecution
	if exec != nil {
		if !exec.fastCallsAllowed {
			return false
		}
	}
	if exec == nil {
		if vm.tracer.Load() != nil ||
			vm.runtimeTraceAnnotations.Load() ||
			vm.variableTracker.Load() != nil ||
			vm.debugController.Load() != nil ||
			vm.stackFramesRequired.Load() {
			return false
		}
	}
	return fn.frameFree &&
		!fn.IsVariadic &&
		len(fn.Results) == 0 &&
		fn.Native == nil &&
		fn.NativeContext == nil
}

// callFrameFreeFunction is callFunction's lean user-function path. Its
// precondition is canFastCall(fn); keeping its binding and control-flow
// handling explicit leaves the full recover/defer machinery out of the hot
// path without changing behavior in observable or debuggable modes.
func (vm *Interpreter) callFrameFreeFunction(fn *Function, recv *any, args []any) (any, error) {
	var local *Env
	if fn.envReusable {
		if pooled := vm.fastEnvPool.Get(); pooled == nil {
			local = &Env{}
		} else {
			local = pooled.(*Env)
		}
		local.Parent = fn.Env
		if fn.Env != nil {
			local.frame = fn.Env.frame
		}
		defer vm.releaseFastEnv(local)
	} else {
		local = NewEnv(fn.Env)
	}
	argIndex := 0
	if fn.RecvName != "" && recv != nil {
		vm.declare(fn.RecvName, *recv, local)
	}
	for _, p := range fn.Params {
		if argIndex >= len(args) {
			vm.declare(p, nil, local)
		} else {
			vm.declare(p, args[argIndex], local)
		}
		argIndex++
	}

	c, err := vm.execStmtList(fn.Body.(*ast.BlockStmt).List, local)
	if err != nil {
		return nil, err
	}
	switch c.kind {
	case controlReturn:
		return c.val, nil
	case controlBreak, controlContinue:
		return nil, NewRuntimeError("break/continue outside loop")
	case controlGoto:
		return nil, NewRuntimeError("goto " + c.label + ": label not found")
	}
	return nil, nil
}

// releaseFastEnv removes every reference owned by a completed call before
// making its Env available to a later invocation. envReusable guarantees no
// closure can still reach it; sync.Pool also keeps this safe when guest
// goroutines execute independent reusable functions concurrently.
func (vm *Interpreter) releaseFastEnv(env *Env) {
	env.Vars = nil
	env.inlineIntVar = intVar{}
	env.Parent = nil
	env.frame = nil
	vm.fastEnvPool.Put(env)
}

// analyzeFunctionMetadata returns function-call optimization metadata:
// frameFree indicates whether this function can use the fast call path,
// needsFrames whether debug.Stack/debug.Vars calls appear in this body,
// and reusable whether the frame can be safely returned to the fast env pool.
func analyzeFunctionMetadata(body *ast.BlockStmt) (frameFree bool, needsFrames bool, reusable bool) {
	frameFree = true
	reusable = true
	ast.Inspect(body, func(node ast.Node) bool {
		if needsFrames {
			// Once stack inspection exists, caller-level metadata is fully
			// resolved; nested traversal can stop.
			return false
		}
		switch n := node.(type) {
		case *ast.DeferStmt:
			frameFree = false
			return true
		case *ast.CallExpr:
			if id, ok := n.Fun.(*ast.Ident); ok && id.Name == "recover" {
				frameFree = false
				return true
			}
			if sel, ok := n.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "Stack" || sel.Sel.Name == "Vars") {
				frameFree = false
				needsFrames = true
				// No further traversal needed: caller already must force full
				// frame capture for this run if this executes.
				return false
			}
		case *ast.FuncLit:
			// Nested function literals have separate activation records.
			// Their bodies do not affect this function's frame-free safety.
			return false
		}
		return true
	})
	return
}

// frameFreeBody returns false when a function's own body can observe or
// mutate its active call frame.
func frameFreeBody(body *ast.BlockStmt) bool {
	frameFree, _, _ := analyzeFunctionMetadata(body)
	return frameFree
}

// reusableEnvBody rejects function literals because a returned or otherwise
// escaped closure retains its defining Env beyond the current call. Other
// frame-free functions have strictly call-scoped environments and can recycle
// them immediately after execStmtList completes.
func reusableEnvBody(body *ast.BlockStmt) bool {
	_, _, reusable := analyzeFunctionMetadata(body)
	return reusable
}

// sourceMayInspectStack detects the stack-sensitive debug helpers before
// execution starts. A frame-free outer function would otherwise disappear
// from a stack captured by a nested callee, so this intentionally scans
// function literals as well as declarations.
func sourceMayInspectStack(node ast.Node) bool {
	needsFrames := false
	ast.Inspect(node, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok && (sel.Sel.Name == "Stack" || sel.Sel.Name == "Vars") {
				needsFrames = true
				return false
			}
		}
		return !needsFrames
	})
	return needsFrames
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
					args := make([]any, len(call.Args))
					for i, a := range call.Args {
						v, err := vm.evalExpr(a, env)
						if err != nil {
							return nil, nil, nil, err
						}
						args[i] = v
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
		args := make([]any, len(call.Args))
		for i, a := range call.Args {
			v, err := vm.evalExpr(a, env)
			if err != nil {
				return nil, nil, nil, err
			}
			args[i] = v
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
	args := make([]any, len(call.Args))
	for i, a := range call.Args {
		v, err := vm.evalExpr(a, env)
		if err != nil {
			return nil, nil, nil, err
		}
		args[i] = v
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
			// Unlike integer division, float64 division by zero does not
			// panic in Go — IEEE 754 defines it as ±Inf (or NaN for 0/0),
			// and Go's / operator on float64 values follows that at
			// runtime. There was a check here that turned it into an error
			// instead; that was simply wrong relative to real Go, not a
			// case of a missing recoverable-panic conversion.
			return ToFloat(left) / ToFloat(right), nil
		}
		if ToInt(right) == 0 {
			// A recoverable Go runtime panic — see the IndexExpr case
			// above for why this uses *panicError instead of RuntimeError.
			return nil, &panicError{value: "runtime error: integer divide by zero"}
		}
		return ToInt(left) / ToInt(right), nil
	case token.REM:
		if ToInt(right) == 0 {
			return nil, &panicError{value: "runtime error: integer divide by zero"}
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
		return "map[" + x.KeyType + "]" + x.ElementType
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

// evalTypeAssert evaluates the x.(T) expression at the heart of a
// *ast.TypeAssertExpr. It returns the asserted value (valid only when
// ok==true) and, always, dyn — the original, unwrapped value of x — so a
// caller can build an "interface conversion" panic message on failure
// without evaluating ex.X a second time (which would double any side
// effect, e.g. x itself being a call expression).
//
// nanoGo's dynamic-typing model has no separate "zero value" story for a
// failed comma-ok assertion (a missing function argument is likewise bound
// to nil rather than its type's zero value — see callFunction): the
// asserted value is nil whenever ok is false, matching that existing
// convention rather than trying to introduce a new one here.
func (vm *Interpreter) evalTypeAssert(ex *ast.TypeAssertExpr, env *Env) (asserted any, dyn any, ok bool, err error) {
	dyn, err = vm.evalExpr(ex.X, env)
	if err != nil {
		return nil, nil, false, err
	}
	// A nil interface value never satisfies any type assertion, including
	// against an interface type — matches Go.
	if dyn == nil {
		return nil, nil, false, nil
	}
	switch t := ex.Type.(type) {
	case *ast.InterfaceType:
		names := interfaceMethodNames(t)
		if len(names) == 0 {
			return dyn, dyn, true, nil // interface{} / inline empty interface
		}
		return dyn, dyn, vm.valueSatisfiesMethods(dyn, names), nil
	case *ast.Ident:
		switch t.Name {
		case "any":
			return dyn, dyn, true, nil
		case "error":
			if e, isErr := dyn.(error); isErr {
				return e, dyn, true, nil
			}
			return dyn, dyn, vm.valueSatisfiesMethods(dyn, []string{"Error"}), nil
		}
		if td, found := vm.types[t.Name]; found && td.Kind == "interface" {
			return dyn, dyn, vm.valueSatisfiesMethods(dyn, td.InterfaceMethods), nil
		}
	}
	// Concrete type: compare dynamic type against the asserted type name,
	// ignoring a leading "*" since nanoGo represents a struct the same way
	// whether it was declared by value or by pointer (see StructVal).
	want := strings.TrimPrefix(typeString(ex.Type), "*")
	if want != "" && typeOfValue(vm, dyn) == want {
		return dyn, dyn, true, nil
	}
	return dyn, dyn, false, nil
}

// interfaceMethodNames collects the directly-declared method names of an
// interface type literal. Embedded interfaces (a Field with no Names) are
// not expanded — a documented simplification, since resolving one requires
// looking it up by name and nanoGo's grammar allows arbitrary embedding
// expressions here, not just a plain identifier.
func interfaceMethodNames(it *ast.InterfaceType) []string {
	if it.Methods == nil {
		return nil
	}
	var names []string
	for _, f := range it.Methods.List {
		names = append(names, namesOf(f)...)
	}
	return names
}

func namesOf(f *ast.Field) []string {
	names := make([]string, 0, len(f.Names))
	for _, n := range f.Names {
		names = append(names, n.Name)
	}
	return names
}

// valueSatisfiesMethods reports whether v's registered type (its dynamic
// type's TypeDef.Methods) defines every method in names. An unregistered
// dynamic type (a bare int/string/slice, or a struct type nanoGo never saw
// a declaration for) satisfies only the empty method set.
func (vm *Interpreter) valueSatisfiesMethods(v any, names []string) bool {
	if len(names) == 0 {
		return true
	}
	td := vm.types[typeOfValue(vm, v)]
	if td == nil || td.Methods == nil {
		return false
	}
	for _, m := range names {
		if _, has := td.Methods[m]; !has {
			return false
		}
	}
	return true
}

// interfaceConversionMessage mirrors the shape of Go's real "interface
// conversion" panic text closely enough to be recognizable, without
// claiming exact parity (nanoGo has no static interface-name tracking for
// the "is" side, so it reports the dynamic type instead).
func interfaceConversionMessage(vm *Interpreter, target ast.Expr, dyn any) string {
	want := typeString(target)
	if dyn == nil {
		return fmt.Sprintf("interface conversion: interface is nil, not %s", want)
	}
	return fmt.Sprintf("interface conversion: interface {} is %s, not %s", typeOfValue(vm, dyn), want)
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
