package interp

import "go/token"

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
