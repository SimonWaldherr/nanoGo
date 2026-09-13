package interp

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Generic functions are checked before first use and specialized into ordinary
// evaluator ASTs. No type bindings are installed in shared interpreter globals.
// This first implementation deliberately requires a self-contained declaration:
// builtins, callbacks, recursion and inline constraints are supported; unresolved
// package types/functions are rejected rather than erased or guessed.
type genericFunction struct {
	decl      *ast.FuncDecl
	once      sync.Once
	err       error
	source    string
	file      *ast.File
	fset      *token.FileSet
	info      *types.Info
	signature *types.Signature
	mu        sync.Mutex
	instances map[string]*Function
}

type genericInstance struct{ signature *types.Signature }

func genericMetadata(decl *ast.FuncDecl) *genericFunction {
	if decl.Type.TypeParams == nil || len(decl.Type.TypeParams.List) == 0 {
		return nil
	}
	return &genericFunction{decl: decl}
}

func (g *genericFunction) prepare() error {
	g.once.Do(func() {
		var source bytes.Buffer
		source.WriteString("package specialization\n")
		if err := format.Node(&source, token.NewFileSet(), g.decl); err != nil {
			g.err = err
			return
		}
		g.source = source.String()
		g.fset = token.NewFileSet()
		file, err := parser.ParseFile(g.fset, "generic.go", g.source, parser.SkipObjectResolution)
		if err != nil {
			g.err = err
			return
		}
		g.file = file
		g.info = &types.Info{Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}}
		config := types.Config{GoVersion: "go1.25", Sizes: types.SizesFor("gc", runtime.GOARCH)}
		pkg, err := config.Check("nanogo/specialization", g.fset, []*ast.File{file}, g.info)
		if err != nil {
			g.err = fmt.Errorf("generic %s: unsupported or invalid declaration: %w", g.decl.Name.Name, err)
			return
		}
		g.signature = pkg.Scope().Lookup(g.decl.Name.Name).Type().(*types.Signature)
		g.instances = make(map[string]*Function)
	})
	return g.err
}

func genericTypeExpr(expr ast.Expr) (types.Type, error) {
	var text bytes.Buffer
	if err := format.Node(&text, token.NewFileSet(), expr); err != nil {
		return nil, err
	}
	return genericTypeName(text.String())
}

func genericTypeName(name string) (types.Type, error) {
	if object, ok := types.Universe.Lookup(name).(*types.TypeName); ok {
		return object.Type(), nil
	}
	value, err := types.Eval(token.NewFileSet(), nil, token.NoPos, name)
	if err != nil {
		return nil, fmt.Errorf("generic type %q is not supported: %w", name, err)
	}
	if !value.IsType() {
		return nil, fmt.Errorf("generic argument %q is not a type", name)
	}
	return value.Type, nil
}

func genericValueType(value any) (types.Type, error) {
	switch v := value.(type) {
	case nil:
		return types.Typ[types.UntypedNil], nil
	case int:
		return types.Typ[types.Int], nil
	case int64:
		return types.Typ[types.Int64], nil
	case float64:
		return types.Typ[types.Float64], nil
	case bool:
		return types.Typ[types.Bool], nil
	case string:
		return types.Typ[types.String], nil
	case spreadSlice:
		return genericValueType(&v.value)
	case *SliceVal:
		elem, err := genericTypeName(v.ElementType)
		if err != nil {
			return nil, err
		}
		if v.Fixed {
			return types.NewArray(elem, int64(len(v.Data))), nil
		}
		return types.NewSlice(elem), nil
	case *MapVal:
		key, err := genericTypeName(v.KeyType)
		if err != nil {
			return nil, err
		}
		elem, err := genericTypeName(v.ElementType)
		if err != nil {
			return nil, err
		}
		return types.NewMap(key, elem), nil
	case *PointerVal:
		elem, err := genericTypeName(v.ElementType)
		if err != nil {
			return nil, err
		}
		return types.NewPointer(elem), nil
	case *ChannelVal:
		elem, err := genericTypeName(v.ElementType)
		if err != nil {
			return nil, err
		}
		return types.NewChan(types.SendRecv, elem), nil
	case *Function:
		if v.genericInstance != nil {
			return v.genericInstance.signature, nil
		}
		if v.syntax != nil {
			return genericTypeExpr(v.syntax)
		}
	}
	return nil, fmt.Errorf("generic inference does not support %T", value)
}

func inferGenericType(pattern, actual types.Type, bindings map[*types.TypeParam]types.Type) error {
	if parameter, ok := pattern.(*types.TypeParam); ok {
		if actual == types.Typ[types.UntypedNil] {
			return nil
		}
		if previous := bindings[parameter]; previous != nil && !types.Identical(previous, actual) {
			return fmt.Errorf("conflicting inference for %s", parameter.Obj().Name())
		}
		bindings[parameter] = actual
		return nil
	}
	switch p := pattern.(type) {
	case *types.Slice:
		if a, ok := actual.(*types.Slice); ok {
			return inferGenericType(p.Elem(), a.Elem(), bindings)
		}
	case *types.Array:
		if a, ok := actual.(*types.Array); ok && a.Len() == p.Len() {
			return inferGenericType(p.Elem(), a.Elem(), bindings)
		}
	case *types.Pointer:
		if a, ok := actual.(*types.Pointer); ok {
			return inferGenericType(p.Elem(), a.Elem(), bindings)
		}
	case *types.Chan:
		if a, ok := actual.(*types.Chan); ok {
			return inferGenericType(p.Elem(), a.Elem(), bindings)
		}
	case *types.Map:
		if a, ok := actual.(*types.Map); ok {
			if err := inferGenericType(p.Key(), a.Key(), bindings); err != nil {
				return err
			}
			return inferGenericType(p.Elem(), a.Elem(), bindings)
		}
	case *types.Signature:
		if a, ok := actual.(*types.Signature); ok && a.Params().Len() == p.Params().Len() && a.Results().Len() == p.Results().Len() {
			for i := 0; i < p.Params().Len(); i++ {
				if err := inferGenericType(p.Params().At(i).Type(), a.Params().At(i).Type(), bindings); err != nil {
					return err
				}
			}
			for i := 0; i < p.Results().Len(); i++ {
				if err := inferGenericType(p.Results().At(i).Type(), a.Results().At(i).Type(), bindings); err != nil {
					return err
				}
			}
		}
	}
	return nil // The instantiated signature performs the final assignability check.
}

func genericArguments(signature *types.Signature, args []any, visit func(types.Type, any) error) error {
	count := signature.Params().Len()
	if (!signature.Variadic() && len(args) != count) || (signature.Variadic() && len(args) < count-1) {
		return fmt.Errorf("generic call: argument count mismatch")
	}
	for i, arg := range args {
		index := i
		if signature.Variadic() && index >= count-1 {
			index = count - 1
		}
		pattern := signature.Params().At(index).Type()
		if signature.Variadic() && i >= count-1 {
			if _, spread := arg.(spreadSlice); !spread {
				pattern = pattern.(*types.Slice).Elem()
			}
		}
		if err := visit(pattern, arg); err != nil {
			return err
		}
	}
	return nil
}

func (g *genericFunction) infer(fn *Function, args []any) (*Function, error) {
	if err := g.prepare(); err != nil {
		return nil, err
	}
	bindings := make(map[*types.TypeParam]types.Type)
	err := genericArguments(g.signature, args, func(pattern types.Type, arg any) error {
		actual, err := genericValueType(arg)
		if err != nil {
			return err
		}
		return inferGenericType(pattern, actual, bindings)
	})
	if err != nil {
		return nil, err
	}
	typesList := make([]types.Type, g.signature.TypeParams().Len())
	for i := range typesList {
		parameter := g.signature.TypeParams().At(i)
		typesList[i] = bindings[parameter]
		if typesList[i] == nil {
			return nil, fmt.Errorf("cannot infer %s; supply explicit type arguments", parameter.Obj().Name())
		}
	}
	return g.instantiate(fn, typesList)
}

func (g *genericFunction) explicit(fn *Function, expressions []ast.Expr) (*Function, error) {
	if err := g.prepare(); err != nil {
		return nil, err
	}
	typesList := make([]types.Type, len(expressions))
	for i, expr := range expressions {
		typ, err := genericTypeExpr(expr)
		if err != nil {
			return nil, err
		}
		typesList[i] = typ
	}
	return g.instantiate(fn, typesList)
}

func (g *genericFunction) instantiate(fn *Function, arguments []types.Type) (*Function, error) {
	if len(arguments) != g.signature.TypeParams().Len() {
		return nil, fmt.Errorf("generic %s: expected %d type arguments, got %d", fn.Name, g.signature.TypeParams().Len(), len(arguments))
	}
	names := make([]string, len(arguments))
	for i, typ := range arguments {
		names[i] = types.TypeString(typ, nil)
	}
	key := strings.Join(names, "\x00")
	g.mu.Lock()
	defer g.mu.Unlock()
	if cached := g.instances[key]; cached != nil {
		return cached, nil
	}
	instance, err := types.Instantiate(nil, g.signature, arguments, true)
	if err != nil {
		return nil, fmt.Errorf("generic %s: %w", fn.Name, err)
	}
	bindings := make(map[*types.TypeParam]string)
	for i, name := range names {
		bindings[g.signature.TypeParams().At(i)] = name
	}
	type replacement struct {
		start, end int
		text       string
	}
	decl := g.file.Decls[0].(*ast.FuncDecl)
	params := decl.Type.TypeParams
	replacements := []replacement{{g.fset.Position(params.Opening).Offset, g.fset.Position(params.Closing).Offset + 1, ""}}
	for identifier, object := range g.info.Uses {
		if _, ok := object.(*types.TypeName); !ok {
			continue
		}
		parameter, ok := object.Type().(*types.TypeParam)
		if !ok {
			continue
		}
		name, ok := bindings[parameter]
		if !ok {
			continue
		}
		start, end := g.fset.Position(identifier.Pos()).Offset, g.fset.Position(identifier.End()).Offset
		if start >= replacements[0].start && end <= replacements[0].end {
			continue
		}
		replacements = append(replacements, replacement{start, end, name})
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	source := g.source
	for _, r := range replacements {
		source = source[:r.start] + r.text + source[r.end:]
	}
	file, err := parser.ParseFile(token.NewFileSet(), "specialized.go", source, parser.SkipObjectResolution)
	if err != nil {
		return nil, err
	}
	specialized := file.Decls[0].(*ast.FuncDecl)
	copy := *fn
	copy.generic = nil
	copy.genericInstance = &genericInstance{signature: instance.(*types.Signature)}
	copy.Body = specialized.Body
	copy.syntax = specialized.Type
	copy.Results = namedResults(specialized.Type.Results)
	copy.resultTypes = namedResultTypes(specialized.Type.Results)
	g.instances[key] = &copy
	return &copy, nil
}

func (g *genericInstance) validate(args []any) error {
	return genericArguments(g.signature, args, func(expected types.Type, arg any) error {
		actual, err := genericValueType(arg)
		if err != nil {
			return err
		}
		if !types.AssignableTo(actual, expected) {
			return fmt.Errorf("generic argument has type %s, want %s", actual, expected)
		}
		return nil
	})
}
