package interp

import (
	"fmt"
	"go/ast"
	"strings"
)

// NamedValue retains a guest named scalar's identity across interfaces and
// package boundaries. Value is its underlying scalar representation. Hosts
// can use ToNativeValue when they need only that ordinary Go value.
type NamedValue struct {
	TypeName string
	Value    any
}

func (v NamedValue) String() string { return fmt.Sprint(v.Value) }

func unwrapNamedValue(value any) any {
	for {
		v, ok := value.(NamedValue)
		if !ok {
			return value
		}
		value = v.Value
	}
}

func (vm *Interpreter) convertSlice(typ string, value any) (any, error) {
	elem := strings.TrimPrefix(typ, "[]")
	if value == nil {
		return &SliceVal{ElementType: elem}, nil
	}
	if source, ok := value.(*SliceVal); ok && source.ElementType == elem {
		header := *source
		return &header, nil
	}
	text, ok := value.(string)
	if !ok || (elem != "byte" && elem != "uint8" && elem != "rune" && elem != "int32") {
		return nil, NewRuntimeError("unsupported conversion to " + typ)
	}
	if len(text) > vm.maxContainerSize() {
		return nil, NewRuntimeError("slice conversion exceeds interpreter limit")
	}
	if err := vm.chargeAllocation(uint64(len(text))); err != nil {
		return nil, err
	}
	var data []any
	if elem == "rune" || elem == "int32" {
		data = make([]any, 0, len(text))
		for _, r := range text {
			data = append(data, int(r))
		}
	} else {
		data = make([]any, len(text))
		for i := 0; i < len(text); i++ {
			data[i] = int(text[i])
		}
	}
	return &SliceVal{ElementType: elem, Data: data}, nil
}

// Package type bindings live in their lexical package environment. The global
// registry uses canonical identities; it never needs a last-writer-wins short
// name alias for a loaded package.
const typeBindingPrefix = "\x00type:"

func (vm *Interpreter) typeStringInEnv(expr ast.Expr, env *Env) string {
	return vm.resolveTypeName(vm.typeStringCached(expr), env)
}

func (vm *Interpreter) resolveTypeName(name string, env *Env) string {
	if isBuiltinType(name) || name == "any" || name == "interface{}" || name == "error" {
		return name
	}
	if strings.HasPrefix(name, "[]") {
		return "[]" + vm.resolveTypeName(name[2:], env)
	}
	if strings.HasPrefix(name, "*") {
		return "*" + vm.resolveTypeName(name[1:], env)
	}
	if strings.HasPrefix(name, "map[") {
		k, v := parseMapType(name)
		return "map[" + vm.resolveTypeName(k, env) + "]" + vm.resolveTypeName(v, env)
	}
	if strings.HasPrefix(name, "chan ") {
		return "chan " + vm.resolveTypeName(name[5:], env)
	}
	if _, elem, ok := parseArrayType(name); ok {
		return name[:len(name)-len(elem)] + vm.resolveTypeName(elem, env)
	}
	if value, ok := vm.get(typeBindingPrefix+name, env); ok {
		if td, ok := value.(*TypeDef); ok {
			return vm.canonicalTypeName(td.Name)
		}
	}
	if alias, member, ok := strings.Cut(name, "."); ok {
		if pkg, ok := vm.packageForSelector(alias, env); ok {
			pkg.mu.RLock()
			td := pkg.Types[member]
			pkg.mu.RUnlock()
			if td != nil {
				return vm.canonicalTypeName(td.Name)
			}
		}
	}
	if name != "" && !isBuiltinType(name) && name != "any" && name != "interface{}" && name != "error" && !strings.ContainsAny(name, ".{} []") {
		if value, ok := vm.get(typeBindingPrefix, env); ok {
			if identity, ok := value.(string); ok {
				return vm.canonicalTypeName(identity + "." + name)
			}
		}
	}
	return vm.canonicalTypeName(name)
}

func (vm *Interpreter) canonicalTypeName(name string) string {
	// Reject cycles elsewhere; bounding resolution also makes host-registered
	// malformed aliases safe to inspect.
	for i := 0; i < 100; i++ {
		td := vm.types[name]
		if td == nil || td.Kind != "alias" || td.Underlying == "" {
			return name
		}
		name = td.Underlying
	}
	return name
}

func (vm *Interpreter) registerTypeSpec(ts *ast.TypeSpec, env *Env, canonical string) {
	td := vm.types[canonical]
	if td == nil {
		td = &TypeDef{Name: canonical, Methods: map[string]*Function{}}
	}
	vm.types[canonical] = td
	vm.declare(typeBindingPrefix+ts.Name.Name, td, env)
	if ts.Assign.IsValid() {
		td.Kind, td.Underlying = "alias", vm.typeStringInEnv(ts.Type, env)
		return
	}
	switch tt := ts.Type.(type) {
	case *ast.StructType:
		td.Kind, td.Fields = "struct", nil
		for _, field := range tt.Fields.List {
			if len(field.Names) == 0 {
				td.hasEmbeddedFields = true
			}
			for _, name := range field.Names {
				td.Fields = append(td.Fields, newFieldDef(name.Name, vm.typeStringInEnv(field.Type, env), astStructTag(field.Tag)))
			}
		}
		td.allIntFields = structFieldsAreInts(td.Fields)
	case *ast.InterfaceType:
		td.Kind, td.InterfaceMethods = "interface", interfaceMethodNames(tt)
		td.InterfaceEmbeds = interfaceEmbeddedNames(tt)
		for i, name := range td.InterfaceEmbeds {
			td.InterfaceEmbeds[i] = vm.resolveTypeName(name, env)
		}
	default:
		td.Kind, td.Underlying = "named", vm.typeStringInEnv(ts.Type, env)
	}
}

// evalTypedExpr supplies context only to literals whose omitted type is
// defined by their enclosing composite. The parsed AST remains immutable and
// can be reused by independently prepared executions.
func (vm *Interpreter) evalTypedExpr(expr ast.Expr, typ string, env *Env) (any, error) {
	if lit, ok := expr.(*ast.CompositeLit); ok && lit.Type == nil {
		copy := *lit
		copy.Type = &ast.Ident{Name: strings.TrimPrefix(typ, "*"), NamePos: lit.Lbrace}
		value, err := vm.evalExpr(&copy, env)
		if err == nil && strings.HasPrefix(typ, "*") {
			return newPointerValue(value, typ[1:]), nil
		}
		return value, err
	}
	v, err := vm.evalExpr(expr, env)
	if err != nil {
		return nil, err
	}
	return vm.coerceToType(v, typ), nil
}
