package interp

import (
	"context"
	"errors"
	"go/ast"
)

func resultTuple(value any) ([]any, bool) {
	switch v := value.(type) {
	case multipleValues:
		return v, true
	default:
		return nil, false
	}
}

func expandResultArgument(args []any) []any {
	if len(args) == 1 {
		if values, ok := resultTuple(args[0]); ok {
			return values
		}
	}
	return args
}

func namedResultTypes(results *ast.FieldList) []string {
	if results == nil {
		return nil
	}
	var types []string
	for _, field := range results.List {
		for range field.Names {
			types = append(types, typeString(field.Type))
		}
	}
	return types
}

func (vm *Interpreter) readNamedResults(names []string, env *Env) any {
	if len(names) == 1 {
		v, _ := vm.get(names[0], env)
		return v
	}
	values := make(multipleValues, len(names))
	for i, name := range names {
		values[i], _ = vm.get(name, env)
	}
	return values
}

// evalResultAssignment retains the native (value, error) ABI while keeping
// guest panics and cancellation on the unwind path. Native facades also use
// RuntimeError for ordinary API failures, so those retain the legacy ABI.
func (vm *Interpreter) evalResultAssignment(expr ast.Expr, count int, env *Env) ([]any, error) {
	// Preserve explicit singleton tuples from strict native APIs until arity is checked.
	value, err := vm.evalExprNode(expr, env)
	if err != nil {
		attachRuntimeErrorLocation(err, vm.traceLocation(expr.Pos()))
	}
	if err != nil {
		switch err.(type) {
		case *panicError:
			return nil, err
		}
		var strict *strictHostError
		var limit *LimitError
		if count != 2 || errors.As(err, &strict) || errors.As(err, &limit) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrStepLimit) || errors.Is(err, ErrGoroutineLimit) {
			return nil, err
		}
		return []any{value, err}, nil
	}
	if values, ok := resultTuple(value); ok {
		if len(values) != count {
			return nil, NewRuntimeError("assignment count mismatch")
		}
		return values, nil
	}
	if count == 2 {
		return []any{value, nil}, nil
	}
	return nil, NewRuntimeError("assignment count mismatch")
}

func (vm *Interpreter) declareResultAssignment(spec *ast.ValueSpec, env *Env) (bool, error) {
	if len(spec.Names) < 2 || len(spec.Values) != 1 {
		return false, nil
	}
	if _, ok := spec.Values[0].(*ast.CallExpr); !ok {
		return false, nil
	}
	values, err := vm.evalResultAssignment(spec.Values[0], len(spec.Names), env)
	if err != nil {
		return true, err
	}
	for i, name := range spec.Names {
		if name.Name == "_" {
			continue
		}
		value := values[i]
		if spec.Type != nil {
			value = vm.coerceToType(value, typeString(spec.Type))
		}
		vm.declare(name.Name, value, env)
		if vm.trackingVariables() {
			vm.recordVariable(name.Name, value, name, env)
		}
	}
	return true, nil
}

// evalSingleExpr rejects an explicit tuple used as one assignment value.
func (vm *Interpreter) evalSingleExpr(expr ast.Expr, env *Env) (any, error) {
	value, err := vm.evalExpr(expr, env)
	if err != nil {
		return nil, err
	}
	if _, tuple := resultTuple(value); tuple {
		return nil, NewRuntimeError("assignment count mismatch")
	}
	return value, nil
}
