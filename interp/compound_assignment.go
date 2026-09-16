package interp

import (
	"go/ast"
	"go/token"
)

// compoundNumeric updates an unboxed numeric binding in place. The caller has
// already evaluated the RHS, so calls and other side effects retain their
// ordinary order. Dynamic values and map-backed bindings use applyBinaryOp.
func (vm *Interpreter) compoundNumeric(name string, op token.Token, rhs any, env *Env) (bool, error) {
	switch rhs.(type) {
	case int, float64:
	default:
		return false, nil
	}
	exec := vm.activeExecution
	concurrent := exec != nil && exec.concurrent.Load()
	for e := env; e != nil; e = e.Parent {
		locked := e.shared || concurrent
		if locked {
			e.mu.Lock()
		}
		handled, err := compoundNumericInEnv(e, name, op, rhs)
		bound := handled || hasBinding(e, name)
		if locked {
			e.mu.Unlock()
		}
		if handled || bound {
			return handled, err
		}
	}
	return false, nil
}

func compoundNumericInEnv(env *Env, name string, op token.Token, rhs any) (bool, error) {
	if right, ok := rhs.(int); ok {
		if env.inlineIntVar.name == name && name != "" {
			return true, assignIntOp(&env.inlineIntVar.val, op, right)
		}
		if ints := env.inlineInts; ints != nil {
			for i := 0; i < int(ints.len); i++ {
				if ints.vars[i].name == name {
					return true, assignIntOp(&ints.vars[i].val, op, right)
				}
			}
		}
	} else if right, ok := rhs.(float64); ok {
		if floats := env.inlineFloats; floats != nil {
			for i := 0; i < int(floats.len); i++ {
				if floats.vars[i].name == name {
					value := &floats.vars[i].val
					switch op {
					case token.ADD:
						*value += right
					case token.SUB:
						*value -= right
					case token.MUL:
						*value *= right
					case token.QUO:
						*value /= right
					default:
						return false, nil
					}
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// A failed division must leave the binding unchanged so deferred recovery can
// still observe its original value.
func assignIntOp(value *int, op token.Token, right int) error {
	switch op {
	case token.ADD:
		*value += right
	case token.SUB:
		*value -= right
	case token.MUL:
		*value *= right
	case token.QUO:
		if right == 0 {
			return &panicError{value: "runtime error: integer divide by zero"}
		}
		*value /= right
	case token.REM:
		if right == 0 {
			return &panicError{value: "runtime error: integer divide by zero"}
		}
		*value %= right
	case token.AND:
		*value &= right
	case token.OR:
		*value |= right
	case token.XOR:
		*value ^= right
	case token.AND_NOT:
		*value &^= right
	case token.SHL:
		if right < 0 {
			return &panicError{value: "runtime error: negative shift amount"}
		}
		*value <<= uint(right)
	case token.SHR:
		if right < 0 {
			return &panicError{value: "runtime error: negative shift amount"}
		}
		*value >>= uint(right)
	}
	return nil
}

// compoundOperator keeps assignment dispatch identical on both paths.
func compoundOperator(op token.Token) token.Token {
	switch op {
	case token.ADD_ASSIGN:
		return token.ADD
	case token.SUB_ASSIGN:
		return token.SUB
	case token.MUL_ASSIGN:
		return token.MUL
	case token.QUO_ASSIGN:
		return token.QUO
	case token.REM_ASSIGN:
		return token.REM
	case token.AND_ASSIGN:
		return token.AND
	case token.OR_ASSIGN:
		return token.OR
	case token.XOR_ASSIGN:
		return token.XOR
	case token.SHL_ASSIGN:
		return token.SHL
	case token.SHR_ASSIGN:
		return token.SHR
	case token.AND_NOT_ASSIGN:
		return token.AND_NOT
	}
	return token.ILLEGAL
}

// tryCompoundAtom avoids boxing a scalar RHS when both operands have inline
// numeric storage. Only identifiers and cached literals are inspected: calls,
// composite expressions and invalid literals retain the complete evaluator.
// Once a numeric atom is found, consume exactly its ordinary checkpoint and
// never evaluate it again, including when the target needs the dynamic path.
func (vm *Interpreter) tryCompoundAtom(id *ast.Ident, op token.Token, expr ast.Expr, env *Env) (bool, error) {
	var n int
	var f float64
	var intOK, floatOK bool
	switch rhs := expr.(type) {
	case *ast.Ident:
		// These names are handled before environment lookup by evalExprNode.
		if rhs.Name == "true" || rhs.Name == "false" || rhs.Name == "nil" {
			return false, nil
		}
		n, intOK = vm.getIntIdent(rhs, env)
		if !intOK {
			f, floatOK = vm.getFloatIdent(rhs, env)
		}
	case *ast.BasicLit:
		if exec := vm.activeExecution; exec != nil {
			switch rhs.Kind {
			case token.INT:
				n, intOK = exec.litCache[rhs]
			case token.FLOAT:
				f, floatOK = exec.floatLitCache[rhs]
			}
		}
	}
	if !intOK && !floatOK {
		return false, nil
	}
	if err := vm.executionError(); err != nil {
		attachRuntimeErrorLocation(err, vm.traceLocation(expr.Pos()))
		return true, err
	}
	var handled bool
	var err error
	var fallback any
	if intOK {
		handled, err = vm.compoundNumeric(id.Name, op, n, env)
		if !handled {
			fallback = n
		}
	} else {
		handled, err = vm.compoundNumeric(id.Name, op, f, env)
		if !handled {
			fallback = f
		}
	}
	if !handled {
		current, _ := vm.get(id.Name, env)
		var result any
		result, err = vm.applyBinaryOp(op, current, fallback)
		if err == nil {
			vm.set(id.Name, result, env)
		}
	}
	if err == nil && vm.trackingVariables() {
		value, _ := vm.get(id.Name, env)
		vm.recordVariable(id.Name, value, id, env)
	}
	return true, err
}
