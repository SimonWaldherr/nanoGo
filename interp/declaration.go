package interp

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
)

// Declare a numeric literal straight into its unboxed environment slot.
// Other expressions and conversions keep the ordinary evaluator, so this
// path neither speculatively executes a value nor charges extra checkpoints.
func (vm *Interpreter) declareNumericLiteral(name *ast.Ident, expr ast.Expr, typ ast.Expr, env *Env) (bool, error) {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || (lit.Kind != token.INT && lit.Kind != token.FLOAT) {
		return false, nil
	}
	if typ != nil {
		id, ok := typ.(*ast.Ident)
		if !ok || (lit.Kind == token.INT && id.Name != "int") || (lit.Kind == token.FLOAT && id.Name != "float64") {
			return false, nil
		}
	}
	var integer int
	var floating float64
	if lit.Kind == token.INT {
		cached := false
		if exec := vm.activeExecution; exec != nil {
			integer, cached = exec.litCache[lit]
		}
		if !cached {
			v, err := strconv.ParseInt(lit.Value, 0, strconv.IntSize)
			if err != nil {
				return false, nil
			}
			integer = int(v)
		}
	} else {
		cached := false
		if exec := vm.activeExecution; exec != nil {
			floating, cached = exec.floatLitCache[lit]
		}
		if !cached {
			v, err := strconv.ParseFloat(strings.ReplaceAll(lit.Value, "_", ""), 64)
			if err != nil {
				return false, nil
			}
			floating = v
		}
	}
	if err := vm.executionError(); err != nil {
		return true, err
	}
	if lit.Kind == token.INT {
		vm.declareInt(name.Name, integer, env)
		if vm.trackingVariables() {
			vm.recordVariable(name.Name, integer, name, env)
		}
	} else {
		vm.declareFloat(name.Name, floating, env)
		if vm.trackingVariables() {
			vm.recordVariable(name.Name, floating, name, env)
		}
	}
	return true, nil
}
