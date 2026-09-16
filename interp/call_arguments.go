package interp

import (
	"fmt"
	"go/ast"
	"go/token"
	"math"
	"strconv"
	"strings"
)

const constantBindingPrefix = "\x00constant:"
const declaredTypePrefix = "\x00declared-type:"

func errorMethod(err error) *Function {
	return &Function{Name: "Error", Native: func(args []any) (any, error) {
		if len(args) != 0 {
			return nil, NewRuntimeError("error.Error takes no arguments")
		}
		return err.Error(), nil
	}}
}

func (vm *Interpreter) declaredType(name string, env *Env) string {
	for scope := env; scope != nil; scope = scope.Parent {
		if !vm.hasLocalBinding(name, scope) {
			continue
		}
		value, _ := vm.getLocal(declaredTypePrefix+name, scope)
		typ, _ := value.(string)
		return typ
	}
	return ""
}

func (vm *Interpreter) expressionType(expr ast.Expr, value any, env *Env) string {
	switch e := expr.(type) {
	case *ast.ParenExpr:
		return vm.expressionType(e.X, value, env)
	case *ast.Ident:
		if typ := vm.declaredType(e.Name, env); typ != "" {
			return typ
		}
	case *ast.CallExpr:
		if id, ok := e.Fun.(*ast.Ident); ok {
			typ := vm.resolveTypeName(id.Name, env)
			if isBuiltinType(typ) || vm.types[typ] != nil {
				return typ
			}
		}
	}
	return typeOfValue(vm, value)
}

func sameScalarType(a, b string) bool {
	canonical := func(s string) string {
		if s == "byte" {
			return "uint8"
		}
		if s == "rune" {
			return "int32"
		}
		return s
	}
	return canonical(a) == canonical(b)
}

func (vm *Interpreter) recordParameterTypes(fn *Function, env *Env) {
	if fn.syntax == nil || fn.syntax.Params == nil || fn.generic != nil || fn.genericInstance != nil {
		return
	}
	for _, field := range fn.syntax.Params.List {
		// Runtime int/float64 carry their complete scalar type already.
		if id, ok := field.Type.(*ast.Ident); ok && (id.Name == "int" || id.Name == "float64" || id.Name == "bool" || id.Name == "string" || id.Name == "any") {
			continue
		}
		typ := vm.typeStringInEnv(field.Type, fn.Env)
		if ellipsis, ok := field.Type.(*ast.Ellipsis); ok {
			typ = "[]" + vm.typeStringInEnv(ellipsis.Elt, fn.Env)
		}
		for _, name := range field.Names {
			vm.declare(declaredTypePrefix+name.Name, typ, env)
			if _, variadic := field.Type.(*ast.Ellipsis); variadic {
				if v, _ := vm.getLocal(name.Name, env); v != nil {
					if s, ok := v.(*SliceVal); ok {
						s.ElementType = strings.TrimPrefix(typ, "[]")
					}
				}
			}
		}
	}
}

func (vm *Interpreter) untypedConstant(expr ast.Expr, env *Env) bool {
	switch e := expr.(type) {
	case *ast.BasicLit:
		return true
	case *ast.ParenExpr:
		return vm.untypedConstant(e.X, env)
	case *ast.UnaryExpr:
		return e.Op != token.AND && e.Op != token.ARROW && vm.untypedConstant(e.X, env)
	case *ast.BinaryExpr:
		return vm.untypedConstant(e.X, env) && vm.untypedConstant(e.Y, env)
	case *ast.Ident:
		for scope := env; scope != nil; scope = scope.Parent {
			if !vm.hasLocalBinding(e.Name, scope) {
				continue
			}
			value, _ := vm.getLocal(constantBindingPrefix+e.Name, scope)
			untyped, _ := value.(bool)
			return untyped
		}
	case *ast.SelectorExpr:
		if id, ok := e.X.(*ast.Ident); ok {
			if pkg, ok := vm.packageForSelector(id.Name, env); ok {
				pkg.mu.RLock()
				untyped := pkg.untypedConstants[e.Sel.Name]
				pkg.mu.RUnlock()
				return untyped
			}
		}
	}
	return false
}

func (vm *Interpreter) evalCallArgument(fn *Function, index int, expr ast.Expr, env *Env) (any, error) {
	value, err := vm.evalExpr(expr, env)
	if _, multiple := value.(ReturnValues); multiple {
		return value, err
	}
	if err != nil || fn.syntax == nil || fn.syntax.Params == nil || fn.generic != nil || fn.genericInstance != nil {
		return value, err
	}
	// Source type information is needed only for guest functions; native APIs
	// keep their documented dynamic-value contract.
	var parameter ast.Expr
	position := 0
	for _, field := range fn.syntax.Params.List {
		count := len(field.Names)
		if count == 0 {
			count = 1
		}
		if variadic, ok := field.Type.(*ast.Ellipsis); ok {
			if index >= position {
				parameter = variadic.Elt
			}
			break
		}
		if index >= position && index < position+count {
			parameter = field.Type
			break
		}
		position += count
	}
	if parameter == nil {
		return value, nil
	}
	typ := vm.typeStringInEnv(parameter, fn.Env)
	if value == nil {
		if fn.IsVariadic && index >= len(fn.Params)-1 {
			return nil, nil
		}
		return vm.coerceToType(nil, typ), nil
	}
	// An expanded variadic slice is checked by its element type, not treated
	// as one scalar argument. Its backing array must remain shared.
	if slice, ok := value.(*SliceVal); ok && fn.IsVariadic && index >= len(fn.Params)-1 {
		if slice.ElementType != typ {
			return nil, NewRuntimeError("variadic slice element type mismatch")
		}
		return value, nil
	}
	underlying := typ
	untyped := vm.untypedConstant(expr, env)
	if td := vm.types[typ]; td != nil && td.Kind == "named" {
		if named, ok := value.(NamedValue); ok && named.TypeName == typ {
			return value, nil
		}
		if !untyped {
			return nil, NewRuntimeError("explicit conversion required for named argument " + typ)
		}
		// A definition may name another named type. Validate against the final
		// scalar representation, but retain the outer type's identity below.
		for depth := 0; td != nil && (td.Kind == "named" || td.Kind == "alias"); depth++ {
			if depth == 100 {
				return nil, NewRuntimeError("argument type nesting exceeds limit")
			}
			underlying = td.Underlying
			td = vm.types[underlying]
		}
	}
	if isBuiltinType(underlying) && !untyped && !sameScalarType(vm.expressionType(expr, value, env), typ) {
		return nil, NewRuntimeError("explicit conversion required for argument of type " + typ)
	}
	switch underlying {
	case "float32", "float64":
		if n, ok := value.(float64); ok {
			// Go rounds constants to float32 before deciding whether they
			// overflow. Values slightly above MaxFloat32 may still round down.
			if underlying == "float32" && math.IsInf(float64(float32(n)), 0) && untyped {
				return nil, NewRuntimeError("constant overflows float32")
			}
			return vm.coerceToType(value, typ), nil
		}
		if _, ok := value.(int); ok && untyped {
			return vm.coerceToType(value, typ), nil
		}
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64", "uintptr", "byte", "rune":
		if _, ok := value.(int); ok {
			if untyped {
				if !representableInteger(value, underlying) {
					return nil, NewRuntimeError("constant overflows " + typ)
				}
				return vm.coerceToType(value, typ), nil
			}
			return value, nil
		}
		if _, ok := value.(float64); ok && untyped && representableInteger(value, underlying) {
			return vm.coerceToType(value, typ), nil
		}
	case "bool":
		if _, ok := value.(bool); ok {
			return vm.coerceToType(value, typ), nil
		}
	case "string":
		if _, ok := value.(string); ok {
			return vm.coerceToType(value, typ), nil
		}
	default:
		return value, nil
	}
	return nil, NewRuntimeError(fmt.Sprintf("cannot use %s as %s argument; explicit conversion required", typeOfValue(vm, value), typ))
}

func representableInteger(value any, typ string) bool {
	bits := 64
	switch typ {
	case "int", "uint", "uintptr":
		bits = strconv.IntSize
	case "byte", "uint8", "int8":
		bits = 8
	case "uint16", "int16":
		bits = 16
	case "rune", "uint32", "int32":
		bits = 32
	}
	unsigned := strings.HasPrefix(typ, "uint") || typ == "byte"
	switch n := value.(type) {
	case int:
		// Converting to float64 first would round MaxInt64 to 2^63,
		// falsely rejecting a representable integer (and lose other bits).
		if unsigned {
			return n >= 0 && (bits >= strconv.IntSize || uint64(n) < uint64(1)<<bits)
		}
		return bits >= strconv.IntSize || (int64(n) >= -(int64(1)<<(bits-1)) && int64(n) < int64(1)<<(bits-1))
	case float64:
		if math.IsNaN(n) || math.IsInf(n, 0) || math.Trunc(n) != n {
			return false
		}
		if unsigned {
			return n >= 0 && n < math.Ldexp(1, bits)
		}
		return n >= -math.Ldexp(1, bits-1) && n < math.Ldexp(1, bits-1)
	}
	return false
}
