package interp

import "go/ast"

// evalConstantGroup gives iota its declaration index and reuses the previous
// expression list for omitted const initializers, without rewriting the AST.
func (vm *Interpreter) evalConstantGroup(decl *ast.GenDecl, env *Env) error {
	local := NewEnv(env)
	var expressions []ast.Expr
	var valueType ast.Expr
	for index, spec := range decl.Specs {
		vs := spec.(*ast.ValueSpec)
		if len(vs.Values) != 0 {
			expressions, valueType = vs.Values, vs.Type
		}
		if len(expressions) != len(vs.Names) {
			return NewRuntimeError("const initializer count mismatch")
		}
		vm.declare("iota", index, local)
		values := make([]any, len(expressions))
		for i, expr := range expressions {
			value, err := vm.evalExpr(expr, local)
			if err != nil {
				return err
			}
			if valueType != nil {
				value = vm.coerceToType(value, typeString(valueType))
			}
			values[i] = value
		}
		for i, name := range vs.Names {
			if name.Name == "_" {
				continue
			}
			vm.declare(name.Name, values[i], env)
			if vm.trackingVariables() {
				vm.recordVariable(name.Name, values[i], name, env)
			}
		}
	}
	return nil
}
