// interp/evaluator.go
package interp

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
)

// Run parses one Go source unit (package main), resolves simple imports,
// and executes main().
func (vm *Interpreter) Run(src string) error {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "input.go", src, 0)
	if err != nil { return err }
	if file.Name.Name != "main" { return NewRuntimeError(`only "package main" is supported`) }

	global := vm.globals

	// Handle imports (limited curated set).
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl); if !ok || gd.Tok != token.IMPORT { continue }
		for _, sp := range gd.Specs {
			is := sp.(*ast.ImportSpec)
			path := strings.Trim(is.Path.Value, `"`)
			alias := ""
			if is.Name != nil { alias = is.Name.Name } else {
				// default alias is the last path segment
				parts := strings.Split(path, "/")
				alias = parts[len(parts)-1]
			}
			vm.installImportedPackage(alias, path)
		}
	}

	// Collect top-level declarations.
	for _, decl := range file.Decls {
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
							for _, n := range f.Names { td.Fields = append(td.Fields, FieldDef{Name: n.Name, Type: ft}) }
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
						if name.Name == "_" { continue }
						var val any
						if i < len(vs.Values) {
							v, err := vm.evalExpr(vs.Values[i], global); if err != nil { return err }
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
					for _, n := range f.Names { fn.Params = append(fn.Params, n.Name) }
					// variadic if last param is *ast.Ellipsis
					if i == len(d.Type.Params.List)-1 {
						if _, ok := f.Type.(*ast.Ellipsis); ok { fn.IsVariadic = true }
					}
				}
			}
			// Method receiver?
			if d.Recv != nil && len(d.Recv.List) > 0 {
				rcv := d.Recv.List[0]
				fn.RecvName = rcv.Names[0].Name
				fn.RecvType = strings.TrimPrefix(typeString(rcv.Type), "*")
				td := vm.types[fn.RecvType]
				if td == nil { td = &TypeDef{Name: fn.RecvType, Kind: "struct", Methods: map[string]*Function{}}; vm.types[fn.RecvType] = td }
				td.Methods[fn.Name] = fn
			} else {
				vm.funcs[fn.Name] = fn
				vm.globals.Vars[fn.Name] = fn
			}
		}
	}

	// Execute main()
	mainFn, ok := vm.funcs["main"]; if !ok { return NewRuntimeError("no main() function found") }
	_, err = vm.callFunction(mainFn, global, nil, nil)
	return err
}

// ---------------- Expression evaluation ---------------------------

func (vm *Interpreter) evalExpr(e ast.Expr, env *Env) (any, error) {
	switch ex := e.(type) {
	case *ast.BasicLit:
		switch ex.Kind {
		case token.INT:
			n := 0
			for i := 0; i < len(ex.Value); i++ { c := ex.Value[i]; if c < '0' || c > '9' { break }; n = n*10 + int(c-'0') }
			return n, nil
		case token.FLOAT:
			s := ex.Value; dot := strings.IndexByte(s, '.')
			if dot < 0 { return ToFloat(s), nil }
			intp := ToInt(s[:dot]); frac := 0.0; base := 1.0
			for i := dot+1; i < len(s); i++ { frac = frac*10 + float64(s[i]-'0'); base *= 10 }
			return float64(intp) + frac/base, nil
		case token.STRING:
			s := ex.Value; if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' { return s[1:len(s)-1], nil }; return s, nil
		case token.CHAR:
			v := ex.Value
			if len(v) >= 3 && v[0] == '\'' && v[len(v)-1] == '\'' {
				if v[1] == '\\' && len(v) >= 4 { switch v[2] { case 'n': return int('\n'), nil; case 't': return int('\t'), nil; default: return int(v[2]), nil } }
				return int(v[1]), nil
			}
			return 0, NewRuntimeError("invalid character literal")
		default:
			return nil, NewRuntimeError(fmt.Sprintf("unsupported basic literal kind: %v", ex.Kind))
		}
	case *ast.Ident:
		if isBuiltinType(ex.Name) {
			return &Function{Name: ex.Name, Native: func(args []any) (any, error) {
				if len(args) == 0 { return zeroValue(ex.Name), nil }
				return builtinConvert(ex.Name, args[0]), nil
			}}, nil
		}
		if v, ok := vm.get(ex.Name, env); ok { return v, nil }
		if f, ok := vm.funcs[ex.Name]; ok { return f, nil }
		if n, ok := vm.natives[ex.Name]; ok { return &Function{Name: ex.Name, Native: n}, nil }
		if _, ok := vm.types[ex.Name]; ok { return ex.Name, nil }
		return nil, NewRuntimeError("undefined: " + ex.Name)

	case *ast.UnaryExpr:
		if ex.Op == token.ARROW {
			// Receive from channel: <-ch  (single value; two-value handled in assign)
			v, err := vm.evalExpr(ex.X, env); if err != nil { return nil, err }
			ch, ok := v.(*ChannelVal); if !ok || ch == nil { return nil, NewRuntimeError("receive on non-channel") }
			val, ok2 := <- ch.C
			if !ok2 { return zeroValue(ch.ElementType), nil }
			return val, nil
		}
		v, err := vm.evalExpr(ex.X, env); if err != nil { return nil, err }
		switch ex.Op {
		case token.NOT:		return !ToBool(v), nil
		case token.SUB:		if _, ok := v.(float64); ok { return -ToFloat(v), nil }; return -ToInt(v), nil
		case token.ADD:		if _, ok := v.(float64); ok { return +ToFloat(v), nil }; return +ToInt(v), nil
		case token.XOR:		return ^ToInt(v), nil
		case token.AND:		return v, nil // address-of ignored
		default:			return nil, NewRuntimeError("unsupported unary op")
		}

	case *ast.BinaryExpr:
		l, err := vm.evalExpr(ex.X, env); if err != nil { return nil, err }
		r, err := vm.evalExpr(ex.Y, env); if err != nil { return nil, err }
		return vm.applyBinaryOp(ex.Op, l, r)

	case *ast.CallExpr:
		// Builtins: make, len, cap, append, copy, close, delete, panic
		if id, ok := ex.Fun.(*ast.Ident); ok {
			switch id.Name {
			case "make":
				if len(ex.Args) == 0 { return nil, NewRuntimeError("make: missing type") }
				tstr := typeString(ex.Args[0])
				var args []any
				for _, a := range ex.Args[1:] { v, err := vm.evalExpr(a, env); if err != nil { return nil, err }; args = append(args, v) }
				return builtinMake(tstr, args), nil
			case "len":
				if len(ex.Args) != 1 { return 0, nil }
				v, err := vm.evalExpr(ex.Args[0], env); if err != nil { return nil, err }
				return builtinLen(v), nil
			case "cap":
				if len(ex.Args) != 1 { return 0, nil }
				v, err := vm.evalExpr(ex.Args[0], env); if err != nil { return nil, err }
				return builtinCap(v), nil
			case "append":
				if len(ex.Args) < 1 { return nil, NewRuntimeError("append: args") }
				s, err := vm.evalExpr(ex.Args[0], env); if err != nil { return nil, err }
				var els []any
				for i, a := range ex.Args[1:] {
					// Support f(slice...) expansion if CallExpr.Ellipsis is set on last arg.
					if ex.Ellipsis != token.NoPos && i == len(ex.Args[1:])-1 {
						v, err := vm.evalExpr(a, env); if err != nil { return nil, err }
						if sv, ok := v.(*SliceVal); ok { els = append(els, sv.Data...) } else { els = append(els, v) }
					} else {
						v, err := vm.evalExpr(a, env); if err != nil { return nil, err }
						els = append(els, v)
					}
				}
				return builtinAppend(s, els...), nil
			case "copy":
				if len(ex.Args) != 2 { return 0, nil }
				dst, err := vm.evalExpr(ex.Args[0], env); if err != nil { return nil, err }
				src, err := vm.evalExpr(ex.Args[1], env); if err != nil { return nil, err }
				return builtinCopy(dst, src), nil
			case "close":
				if len(ex.Args) != 1 { return nil, NewRuntimeError("close: need channel") }
				v, err := vm.evalExpr(ex.Args[0], env); if err != nil { return nil, err }
				return builtinClose(v), nil
			case "delete":
				if len(ex.Args) != 2 { return nil, nil }
				m, err := vm.evalExpr(ex.Args[0], env); if err != nil { return nil, err }
				k, err := vm.evalExpr(ex.Args[1], env); if err != nil { return nil, err }
				if mm, ok := m.(*MapVal); ok { mm.deleteByKey(k) }
				return nil, nil
			case "panic":
				if len(ex.Args) == 0 { return nil, &panicError{value: "panic"} }
				v, err := vm.evalExpr(ex.Args[0], env); if err != nil { return nil, err }
				return nil, &panicError{value: v}
			}
		}

		// Package function call: fmt.Printf, time.Now, ...
		if sel, ok := ex.Fun.(*ast.SelectorExpr); ok {
			if pid, ok := sel.X.(*ast.Ident); ok {
				if p, ok := vm.globals.Vars[pid.Name].(*Package); ok {
					member, ok2 := vm.resolvePackageSelector(p, sel.Sel.Name)
					if !ok2 { return nil, NewRuntimeError("unknown package member: " + pid.Name + "." + sel.Sel.Name) }
					fn, ok3 := member.(*Function); if !ok3 { return nil, NewRuntimeError("package member is not function") }
					// Evaluate args (including ... expansion)
					var args []any
					if ex.Ellipsis != token.NoPos && len(ex.Args) > 0 {
						for i, a := range ex.Args {
							if i == len(ex.Args)-1 {
								v, err := vm.evalExpr(a, env); if err != nil { return nil, err }
								if sv, ok := v.(*SliceVal); ok { args = append(args, sv.Data...) } else { args = append(args, v) }
							} else {
								v, err := vm.evalExpr(a, env); if err != nil { return nil, err }
								args = append(args, v)
							}
						}
					} else {
						for _, a := range ex.Args { v, err := vm.evalExpr(a, env); if err != nil { return nil, err }; args = append(args, v) }
					}
					return vm.callFunction(fn, env, nil, args)
				}
			}
		}

		// Method call on struct: obj.M(...)
		if sel, ok := ex.Fun.(*ast.SelectorExpr); ok {
			recv, err := vm.evalExpr(sel.X, env); if err != nil { return nil, err }
			recvType := typeOfValue(vm, recv)
			td := vm.types[recvType]; if td == nil || td.Methods == nil { return nil, NewRuntimeError("unknown method on type " + recvType) }
			fn := td.Methods[sel.Sel.Name]; if fn == nil { return nil, NewRuntimeError("method not found: " + recvType + "." + sel.Sel.Name) }
			args := []any{recv}
			// Evaluate args (support last ... expansion)
			if ex.Ellipsis != token.NoPos && len(ex.Args) > 0 {
				for i, a := range ex.Args {
					if i == len(ex.Args)-1 {
						v, err := vm.evalExpr(a, env); if err != nil { return nil, err }
						if sv, ok := v.(*SliceVal); ok { args = append(args, sv.Data...) } else { args = append(args, v) }
					} else {
						v, err := vm.evalExpr(a, env); if err != nil { return nil, err }
						args = append(args, v)
					}
				}
			} else {
				for _, a := range ex.Args { v, err := vm.evalExpr(a, env); if err != nil { return nil, err }; args = append(args, v) }
			}
			return vm.callFunction(fn, env, &recv, args[1:])
		}

		// Normal function call
		callee, err := vm.evalExpr(ex.Fun, env); if err != nil { return nil, err }
		switch fn := callee.(type) {
		case *Function:
			var args []any
			// Handle foo(slice...) expansion
			if ex.Ellipsis != token.NoPos && len(ex.Args) > 0 {
				for i, a := range ex.Args {
					if i == len(ex.Args)-1 {
						v, err := vm.evalExpr(a, env); if err != nil { return nil, err }
						if sv, ok := v.(*SliceVal); ok { args = append(args, sv.Data...) } else { args = append(args, v) }
					} else { v, err := vm.evalExpr(a, env); if err != nil { return nil, err }; args = append(args, v) }
				}
			} else {
				for _, a := range ex.Args { v, err := vm.evalExpr(a, env); if err != nil { return nil, err }; args = append(args, v) }
			}
			return vm.callFunction(fn, env, nil, args)
		default:
			return nil, NewRuntimeError("not a function")
		}

	case *ast.IndexExpr:
		v, err := vm.evalExpr(ex.X, env); if err != nil { return nil, err }
		i, err := vm.evalExpr(ex.Index, env); if err != nil { return nil, err }
		switch t := v.(type) {
		case *SliceVal:
			ii := ToInt(i); if ii < 0 || ii >= len(t.Data) { return nil, NewRuntimeError("index out of range") }
			return t.Data[ii], nil
		case *MapVal:
			val, _ := t.getByKey(i); return val, nil
		case string:
			idx := ToInt(i); if idx < 0 || idx >= len(t) { return nil, NewRuntimeError("index out of range") }
			return int(t[idx]), nil
		default:	return nil, NewRuntimeError("indexing unsupported")
		}

	case *ast.SliceExpr:
		v, err := vm.evalExpr(ex.X, env); if err != nil { return nil, err }
		lo := 0; hi := -1
		if ex.Low != nil { lv, err := vm.evalExpr(ex.Low, env); if err != nil { return nil, err }; lo = ToInt(lv) }
		if ex.High != nil { hv, err := vm.evalExpr(ex.High, env); if err != nil { return nil, err }; hi = ToInt(hv) }
		switch s := v.(type) {
		case *SliceVal:
			if hi < 0 || hi > len(s.Data) { hi = len(s.Data) }
			if lo < 0 || lo > hi { return nil, NewRuntimeError("invalid slice indices") }
			return &SliceVal{ElementType: s.ElementType, Data: s.Data[lo:hi]}, nil
		case string:
			if hi < 0 || hi > len(s) { hi = len(s) }
			if lo < 0 || lo > hi { return nil, NewRuntimeError("invalid slice indices") }
			return s[lo:hi], nil
		default:	return nil, NewRuntimeError("slice unsupported")
		}

	case *ast.SelectorExpr:
		// Package selector (pkg.Member)
		if id, ok := ex.X.(*ast.Ident); ok {
			if p, ok := vm.globals.Vars[id.Name].(*Package); ok {
				m, ok2 := vm.resolvePackageSelector(p, ex.Sel.Name); if !ok2 { return nil, NewRuntimeError("unknown package member: " + id.Name + "." + ex.Sel.Name) }
				return m, nil
			}
		}
		// Struct field access is handled when receiver is *StructVal during method calls or via fieldRef in assignments.
		recv, err := vm.evalExpr(ex.X, env); if err != nil { return nil, err }
		sv, ok := recv.(*StructVal); if !ok { return nil, NewRuntimeError("selector on non-struct") }
		return sv.Fields[ex.Sel.Name], nil

	case *ast.CompositeLit:
		// Struct, slice, map literals.
		typ := typeString(ex.Type)
		if strings.HasPrefix(typ, "[]") {
			elem := typ[2:]
			lit := &SliceVal{ElementType: elem, Data: []any{}}
			for _, elt := range ex.Elts {
				v, err := vm.evalExpr(elt, env); if err != nil { return nil, err }
				lit.Data = append(lit.Data, v)
			}
			return lit, nil
		}
		if strings.HasPrefix(typ, "map[") {
			k, v := parseMapType(typ)
			lit := &MapVal{KeyType: k, ElementType: v, Data: map[string]any{}, Keys: map[string]any{}}
			for _, elt := range ex.Elts {
				kv, ok := elt.(*ast.KeyValueExpr); if !ok { continue }
				key, err := vm.evalExpr(kv.Key, env); if err != nil { return nil, err }
				val, err := vm.evalExpr(kv.Value, env); if err != nil { return nil, err }
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
		for _, f := range td.Fields { obj.Fields[f.Name] = zeroValue(f.Type) }
		for _, elt := range ex.Elts {
			kv, ok := elt.(*ast.KeyValueExpr); if !ok { continue }
			key := kv.Key.(*ast.Ident).Name
			val, err := vm.evalExpr(kv.Value, env); if err != nil { return nil, err }
			obj.Fields[key] = val
		}
		return obj, nil

	case *ast.ParenExpr:
		return vm.evalExpr(ex.X, env)

	default:
		return nil, NewRuntimeError(fmt.Sprintf("unsupported expr: %T", e))
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

type controlFlow struct { kind controlKind; val any }

func (vm *Interpreter) evalStmt(s ast.Stmt, env *Env) (controlFlow, error) {
	switch st := s.(type) {
	case *ast.ExprStmt:
		_, err := vm.evalExpr(st.X, env)
		return controlFlow{}, err

	case *ast.SendStmt:
		chv, err := vm.evalExpr(st.Chan, env); if err != nil { return controlFlow{}, err }
		val, err := vm.evalExpr(st.Value, env); if err != nil { return controlFlow{}, err }
		ch, ok := chv.(*ChannelVal); if !ok || ch == nil { return controlFlow{}, NewRuntimeError("send on non-channel") }
		ch.C <- val
		return controlFlow{}, nil

	case *ast.AssignStmt:
		// Evaluate RHS first
		rightVals := make([]any, len(st.Rhs))

		// Special case: v, ok := m[k]
		if len(st.Lhs) == 2 && len(st.Rhs) == 1 {
			if ie, ok := st.Rhs[0].(*ast.IndexExpr); ok {
				mv, err := vm.evalExpr(ie.X, env); if err != nil { return controlFlow{}, err }
				if m, ok := mv.(*MapVal); ok {
					key, err := vm.evalExpr(ie.Index, env); if err != nil { return controlFlow{}, err }
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
					cv, err := vm.evalExpr(ue.X, env); if err != nil { return controlFlow{}, err }
					ch, ok := cv.(*ChannelVal); if !ok || ch == nil { return controlFlow{}, NewRuntimeError("receive on non-channel") }
					v, ok2 := <- ch.C
					rightVals = []any{v, ok2}
					goto RHS_DONE
				}
			}
			v, err := vm.evalExpr(r, env); if err != nil { return controlFlow{}, err }
			rightVals[i] = v
		}
	RHS_DONE:
		// Resolve LHS references
		leftRefs := make([]Ref, len(st.Lhs))
		for i, l := range st.Lhs {
			ref, err := vm.resolveRef(l, env); if err != nil { return controlFlow{}, err }
			leftRefs[i] = ref
		}
		switch st.Tok {
		case token.DEFINE:
			for i, l := range st.Lhs {
				if id, ok := l.(*ast.Ident); ok { if id.Name == "_" { continue }; var v any; if len(rightVals) == 1 { v = rightVals[0] } else { v = rightVals[i] }; vm.declare(id.Name, v, env) } else { return controlFlow{}, NewRuntimeError("invalid := lhs") }
			}
		case token.ASSIGN:
			for i, ref := range leftRefs {
				var v any; if len(rightVals) == 1 { v = rightVals[0] } else { v = rightVals[i] }
				if err := ref.Set(v); err != nil { return controlFlow{}, err }
			}
		default:
			// augmented assignments supported via applyBinaryOp
			if len(leftRefs) != 1 || len(rightVals) != 1 { return controlFlow{}, NewRuntimeError("augmented assignment expects 1 lhs and 1 rhs") }
			cur := leftRefs[0].Get()
			var base token.Token
			switch st.Tok {
			case token.ADD_ASSIGN: base = token.ADD
			case token.SUB_ASSIGN: base = token.SUB
			case token.MUL_ASSIGN: base = token.MUL
			case token.QUO_ASSIGN: base = token.QUO
			case token.REM_ASSIGN: base = token.REM
			case token.AND_ASSIGN: base = token.AND
			case token.OR_ASSIGN:  base = token.OR
			case token.XOR_ASSIGN: base = token.XOR
			case token.SHL_ASSIGN: base = token.SHL
			case token.SHR_ASSIGN: base = token.SHR
			case token.AND_NOT_ASSIGN: base = token.AND_NOT
			default: return controlFlow{}, NewRuntimeError("unsupported assignment token")
			}
			newVal, err := vm.applyBinaryOp(base, cur, rightVals[0]); if err != nil { return controlFlow{}, err }
			if err := leftRefs[0].Set(newVal); err != nil { return controlFlow{}, err }
		}
		return controlFlow{}, nil

	case *ast.IncDecStmt:
		ref, err := vm.resolveRef(st.X, env); if err != nil { return controlFlow{}, err }
		cur := ToInt(ref.Get()); if st.Tok == token.INC { ref.Set(cur+1) } else { ref.Set(cur-1) }
		return controlFlow{}, nil

	case *ast.DeclStmt:
		decl := st.Decl.(*ast.GenDecl)
		switch decl.Tok {
		case token.VAR, token.CONST:
			for _, sp := range decl.Specs {
				vs := sp.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if n.Name == "_" { continue }
					var val any
					if i < len(vs.Values) { v, err := vm.evalExpr(vs.Values[i], env); if err != nil { return controlFlow{}, err }; val = v } else { val = zeroValue(typeString(vs.Type)) }
					vm.declare(n.Name, val, env)
				}
			}
		}
		return controlFlow{}, nil

	case *ast.BlockStmt:
		local := NewEnv(env)
		for _, s2 := range st.List {
			c, err := vm.evalStmt(s2, local); if err != nil { return controlFlow{}, err }
			switch c.kind {
			case controlReturn, controlBreak, controlContinue:
				return c, nil
			}
		}
		return controlFlow{}, nil

	case *ast.IfStmt:
		if st.Init != nil { if _, err := vm.evalStmt(st.Init, env); err != nil { return controlFlow{}, err } }
		cond, err := vm.evalExpr(st.Cond, env); if err != nil { return controlFlow{}, err }
		if ToBool(cond) { return vm.evalStmt(st.Body, env) } else if st.Else != nil { return vm.evalStmt(st.Else, env) }
		return controlFlow{}, nil

	case *ast.ForStmt:
		local := NewEnv(env)
		if st.Init != nil { if _, err := vm.evalStmt(st.Init, local); err != nil { return controlFlow{}, err } }
		for {
			cond := true
			if st.Cond != nil { v, err := vm.evalExpr(st.Cond, local); if err != nil { return controlFlow{}, err }; cond = ToBool(v) }
			if !cond { break }
			c, err := vm.evalStmt(st.Body, local); if err != nil { return controlFlow{}, err }
			switch c.kind {
			case controlBreak: return controlFlow{}, nil
			case controlReturn: return c, nil
			case controlContinue: /* continue */ }
			if st.Post != nil { if _, err := vm.evalStmt(st.Post, local); err != nil { return controlFlow{}, err } }
		}
		return controlFlow{}, nil

	case *ast.RangeStmt:
		local := NewEnv(env)
		x, err := vm.evalExpr(st.X, local); if err != nil { return controlFlow{}, err }
		switch s := x.(type) {
		case *SliceVal:
			for i := 0; i < len(s.Data); i++ {
				if st.Key != nil { if id, ok := st.Key.(*ast.Ident); ok && id.Name != "_" { vm.set(id.Name, i, local) } }
				if st.Value != nil { if id, ok := st.Value.(*ast.Ident); ok && id.Name != "_" { vm.set(id.Name, s.Data[i], local) } }
				c, err := vm.evalStmt(st.Body, local); if err != nil { return controlFlow{}, err }
				switch c.kind { case controlBreak: return controlFlow{}, nil; case controlReturn: return c, nil; case controlContinue: }
			}
		case *MapVal:
			for _, hk := range keysOfMap(s) {
				key := s.Keys[hk]; val := s.Data[hk]
				if st.Key != nil { if id, ok := st.Key.(*ast.Ident); ok && id.Name != "_" { vm.set(id.Name, key, local) } }
				if st.Value != nil { if id, ok := st.Value.(*ast.Ident); ok && id.Name != "_" { vm.set(id.Name, val, local) } }
				c, err := vm.evalStmt(st.Body, local); if err != nil { return controlFlow{}, err }
				switch c.kind { case controlBreak: return controlFlow{}, nil; case controlReturn: return c, nil; case controlContinue: }
			}
		case string:
			for i := 0; i < len(s); i++ {
				if st.Key != nil { if id, ok := st.Key.(*ast.Ident); ok && id.Name != "_" { vm.set(id.Name, i, local) } }
				if st.Value != nil { if id, ok := st.Value.(*ast.Ident); ok && id.Name != "_" { vm.set(id.Name, int(s[i]), local) } }
				c, err := vm.evalStmt(st.Body, local); if err != nil { return controlFlow{}, err }
				switch c.kind { case controlBreak: return controlFlow{}, nil; case controlReturn: return c, nil; case controlContinue: }
			}
		case *ChannelVal:
			for v := range s.C {
				if st.Key != nil { if id, ok := st.Key.(*ast.Ident); ok && id.Name != "_" { vm.set(id.Name, v, local) } }
				c, err := vm.evalStmt(st.Body, local); if err != nil { return controlFlow{}, err }
				switch c.kind { case controlBreak: return controlFlow{}, nil; case controlReturn: return c, nil; case controlContinue: }
			}
		default:
			return controlFlow{}, NewRuntimeError("range over unsupported type")
		}
		return controlFlow{}, nil

	case *ast.SwitchStmt:
		local := NewEnv(env)
		if st.Init != nil { if _, err := vm.evalStmt(st.Init, local); err != nil { return controlFlow{}, err } }
		var tag any; var err error
		if st.Tag != nil { tag, err = vm.evalExpr(st.Tag, local); if err != nil { return controlFlow{}, err } }
		matched := false
		for _, clause := range st.Body.List {
			cc := clause.(*ast.CaseClause)
			if cc.List == nil {
				if !matched {
					return vm.evalStmt(&ast.BlockStmt{List: cc.Body}, local)
				}
				continue
			}
			if matched { continue }
			for _, ce := range cc.List {
				val, err := vm.evalExpr(ce, local); if err != nil { return controlFlow{}, err }
				if st.Tag == nil {
					if ToBool(val) { matched = true; break }
				} else {
					if equals(tag, val) { matched = true; break }
				}
			}
			if matched { return vm.evalStmt(&ast.BlockStmt{List: cc.Body}, local) }
		}
		return controlFlow{}, nil

	case *ast.DeferStmt:
		// Capture callable and its arguments NOW, but execute on function return/panic.
		fn, recv, args, err := vm.prepareCall(st.Call, env)
		if err != nil { return controlFlow{}, err }
		frame := vm.currentFrame(); if frame == nil { return controlFlow{}, NewRuntimeError("defer outside of function") }
		frame.defers = append(frame.defers, func() {
			_, _ = vm.callFunction(fn, env, recv, args)
		})
		return controlFlow{}, nil

	case *ast.GoStmt:
		fn, recv, args, err := vm.prepareCall(st.Call, env)
		if err != nil { return controlFlow{}, err }
		go func() { _, _ = vm.callFunction(fn, vm.globals, recv, args) }()
		return controlFlow{}, nil

	case *ast.ReturnStmt:
		if len(st.Results) == 0 { return controlFlow{kind: controlReturn, val: nil}, nil }
		v, err := vm.evalExpr(st.Results[0], env); if err != nil { return controlFlow{}, err }
		return controlFlow{kind: controlReturn, val: v}, nil

	case *ast.BranchStmt:
		switch st.Tok {
		case token.BREAK:		return controlFlow{kind: controlBreak}, nil
		case token.CONTINUE:	return controlFlow{kind: controlContinue}, nil
		}
		return controlFlow{}, nil

	default:
		return controlFlow{}, NewRuntimeError(fmt.Sprintf("unsupported stmt: %T", s))
	}
}

func keysOfMap(m *MapVal) []string { out := make([]string, 0, len(m.Keys)); for k := range m.Keys { out = append(out, k) }; return out }

func (vm *Interpreter) resolveRef(l ast.Expr, env *Env) (Ref, error) {
	switch ee := l.(type) {
	case *ast.Ident:
		return &varRef{vm: vm, env: env, name: ee.Name}, nil
	case *ast.IndexExpr:
		x, err := vm.evalExpr(ee.X, env); if err != nil { return nil, err }
		i, err := vm.evalExpr(ee.Index, env); if err != nil { return nil, err }
		switch s := x.(type) {
		case *SliceVal:
			ii := ToInt(i); if ii < 0 || ii >= len(s.Data) { return nil, NewRuntimeError("index out of range") }
			return &sliceIndexRef{s: s, i: ii}, nil
		case *MapVal:
			return &mapIndexRef{m: s, k: i}, nil
		default:
			return nil, NewRuntimeError("index assign unsupported")
		}
	case *ast.SelectorExpr:
		recv, err := vm.evalExpr(ee.X, env); if err != nil { return nil, err }
		sv, ok := recv.(*StructVal); if !ok { return nil, NewRuntimeError("selector assign unsupported") }
		return &fieldRef{s: sv, name: ee.Sel.Name}, nil
	default:
		return nil, NewRuntimeError("invalid lvalue")
	}
}

func (vm *Interpreter) callFunction(fn *Function, env *Env, recv *any, args []any) (ret any, err error) {
	// Run defers in LIFO order on exit; also handle panic unwinding.
	frame := vm.pushFrame()
	defer func() {
		// Execute defers in reverse order
		for i := len(frame.defers)-1; i >= 0; i-- { frame.defers[i]() }
		vm.popFrame()
		if r := recover(); r != nil {
			if pe, ok := r.(*panicError); ok {
				// Convert to error so callers can see panic
				err = pe
			}
		}
	}()

	// Native function?
	if fn.Native != nil {
		var a []any
		if recv != nil { a = append(a, *recv) }
		a = append(a, args...)
		return fn.Native(a)
	}

	// User-defined function
	local := NewEnv(fn.Env)
	argIndex := 0
	if fn.RecvName != "" && recv != nil { vm.declare(fn.RecvName, *recv, local) }
	if fn.IsVariadic && len(fn.Params) > 0 {
		// All args before the last param are regular; the rest packed into a slice.
		for i := 0; i < len(fn.Params)-1; i++ {
			if argIndex >= len(args) { vm.declare(fn.Params[i], nil, local) } else { vm.declare(fn.Params[i], args[argIndex], local) }
			argIndex++
		}
		var rest []any
		for argIndex < len(args) { rest = append(rest, args[argIndex]); argIndex++ }
		vm.declare(fn.Params[len(fn.Params)-1], &SliceVal{ElementType: "any", Data: rest}, local)
	} else {
		for _, p := range fn.Params {
			if argIndex >= len(args) { vm.declare(p, nil, local) } else { vm.declare(p, args[argIndex], local) }
			argIndex++
		}
	}

	for _, st := range fn.Body.(*ast.BlockStmt).List {
		c, err := vm.evalStmt(st, local); if err != nil {
			// If err is panicError, re-panic to trigger unwinding of outer defers.
			if _, ok := err.(*panicError); ok { panic(err) }
			return nil, err
		}
		switch c.kind {
		case controlReturn: return c.val, nil
		case controlBreak, controlContinue: return nil, NewRuntimeError("break/continue outside loop")
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
			if p, ok := vm.globals.Vars[pid.Name].(*Package); ok {
				m, ok2 := vm.resolvePackageSelector(p, sel.Sel.Name); if !ok2 { return nil, nil, nil, NewRuntimeError("unknown package member") }
				fn, ok3 := m.(*Function); if !ok3 { return nil, nil, nil, NewRuntimeError("member not function") }
				var args []any; for _, a := range call.Args { v, err := vm.evalExpr(a, env); if err != nil { return nil, nil, nil, err }; args = append(args, v) }
				return fn, nil, args, nil
			}
		}
		// Method call on struct
		recv, err := vm.evalExpr(sel.X, env); if err != nil { return nil, nil, nil, err }
		recvType := typeOfValue(vm, recv); td := vm.types[recvType]; if td == nil || td.Methods == nil { return nil, nil, nil, NewRuntimeError("unknown method") }
		fn := td.Methods[sel.Sel.Name]; if fn == nil { return nil, nil, nil, NewRuntimeError("method not found") }
		var args []any; for _, a := range call.Args { v, err := vm.evalExpr(a, env); if err != nil { return nil, nil, nil, err }; args = append(args, v) }
		return fn, &recv, args, nil
	}

	callee, err := vm.evalExpr(call.Fun, env); if err != nil { return nil, nil, nil, err }
	fn, ok := callee.(*Function); if !ok { return nil, nil, nil, NewRuntimeError("not a function") }
	var args []any; for _, a := range call.Args { v, err := vm.evalExpr(a, env); if err != nil { return nil, nil, nil, err }; args = append(args, v) }
	return fn, nil, args, nil
}

// ---------------- Helpers ----------------------------------------

func (vm *Interpreter) applyBinaryOp(op token.Token, left, right any) (any, error) {
	switch op {
	case token.ADD:
		if _, ok := left.(string); ok { return ToString(left)+ToString(right), nil }
		if _, ok := right.(string); ok { return ToString(left)+ToString(right), nil }
		if _, ok := left.(float64); ok || isFloat(right) { return ToFloat(left)+ToFloat(right), nil }
		return ToInt(left)+ToInt(right), nil
	case token.SUB:
		if _, ok := left.(float64); ok || isFloat(right) { return ToFloat(left)-ToFloat(right), nil }
		return ToInt(left)-ToInt(right), nil
	case token.MUL:
		if _, ok := left.(float64); ok || isFloat(right) { return ToFloat(left)*ToFloat(right), nil }
		return ToInt(left)*ToInt(right), nil
	case token.QUO:		return ToFloat(left)/ToFloat(right), nil
	case token.REM:		return ToInt(left)%ToInt(right), nil
	case token.SHL:		return ToInt(left) << uint(ToInt(right)), nil
	case token.SHR:		return ToInt(left) >> uint(ToInt(right)), nil
	case token.AND:		return ToInt(left) & ToInt(right), nil
	case token.OR:		return ToInt(left) | ToInt(right), nil
	case token.XOR:		return ToInt(left) ^ ToInt(right), nil
	case token.AND_NOT:	return ToInt(left) &^ ToInt(right), nil
	case token.LAND:	return ToBool(left) && ToBool(right), nil
	case token.LOR:		return ToBool(left) || ToBool(right), nil
	case token.EQL:		return equals(left, right), nil
	case token.NEQ:		return !equals(left, right), nil
	case token.LSS:		if _, ok := left.(float64); ok || isFloat(right) { return ToFloat(left) < ToFloat(right), nil }; return ToInt(left) < ToInt(right), nil
	case token.GTR:		if _, ok := left.(float64); ok || isFloat(right) { return ToFloat(left) > ToFloat(right), nil }; return ToInt(left) > ToInt(right), nil
	case token.LEQ:		if _, ok := left.(float64); ok || isFloat(right) { return ToFloat(left) <= ToFloat(right), nil }; return ToInt(left) <= ToInt(right), nil
	case token.GEQ:		if _, ok := left.(float64); ok || isFloat(right) { return ToFloat(left) >= ToFloat(right), nil }; return ToInt(left) >= ToInt(right), nil
	default:		return nil, NewRuntimeError("unsupported binary op")
	}
}

func isFloat(v any) bool { _, ok := v.(float64); return ok }

func typeOfValue(vm *Interpreter, v any) string {
	switch x := v.(type) {
	case *StructVal: return x.TypeName
	case *SliceVal:  return "[]"+x.ElementType
	case *MapVal:    return "map"
	case *ChannelVal:return "chan "+x.ElementType
	case int:        return "int"
	case float64:    return "float64"
	case bool:       return "bool"
	case string:     return "string"
	case *Function:  return "func"
	default:         return fmt.Sprintf("%T", v)
	}
}

func equals(a, b any) bool {
	switch x := a.(type) {
	case int:     return x == ToInt(b)
	case float64: return x == ToFloat(b)
	case bool:    return x == ToBool(b)
	case string:  return x == ToString(b)
	case *StructVal: return hashKey(a) == hashKey(b)
	default:      return a == b
	}
}
