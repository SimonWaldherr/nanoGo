package interp

import (
	"go/ast"
	"go/token"
)

// Only escaping := bindings need a new scope each iteration (Go 1.22).
// Cache syntax analysis per execution; ordinary range loops keep their
// allocation-free binding updates, including loops reached concurrently.
func (vm *Interpreter) rangeBindingsEscape(st *ast.RangeStmt) bool {
	exec := vm.activeExecution
	if exec != nil {
		if value, ok := exec.rangeEscapes.Load(st); ok {
			return value.(bool)
		}
	}
	escapes := false
	ast.Inspect(st.Body, func(node ast.Node) bool {
		if escapes {
			return false
		}
		switch n := node.(type) {
		case *ast.FuncLit:
			escapes = true
		case *ast.UnaryExpr:
			escapes = n.Op == token.AND
		}
		return !escapes
	})
	if exec != nil {
		exec.rangeEscapes.Store(st, escapes)
	}
	return escapes
}
