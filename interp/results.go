package interp

import (
	"context"
	"errors"
	"fmt"
)

// ResultEvent is a copied named value. Ordering follows emission order; guest
// goroutines may interleave. Repeated names are allowed.
type ResultEvent struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

// ExecutionResults is the outcome of the last execution scope. Events emitted
// before an error remain visible, but only Committed=true marks successful output.
type ExecutionResults struct {
	Events    []ResultEvent `json:"events"`
	Committed bool          `json:"committed"`
}

// BindInputs installs the domain-neutral host package and copies explicit inputs.
// Configure between executions. Input returns a fresh copy on every access;
// callers cannot expose pointers or ambient host capabilities through this API.
func (vm *Interpreter) BindInputs(inputs map[string]any) error {
	value, err := BridgeToGuestWithLimits(inputs, vm.BridgeLimits)
	if err != nil {
		return err
	}
	vm.runMu.Lock()
	defer vm.runMu.Unlock()
	vm.hostInputs = value
	vm.installHostPackage()
	return nil
}

// EnableResults installs host.Input and host.Emit with empty inputs.
func (vm *Interpreter) EnableResults() { _ = vm.BindInputs(nil) }
func (vm *Interpreter) installHostPackage() {
	pkg := &Package{Name: "nanogo/host", Funcs: map[string]*Function{}}
	pkg.Funcs["Input"] = &Function{Name: "Input", Native: func(args []any) (any, error) {
		if len(args) != 1 {
			return nil, fmt.Errorf("%w: host.Input expects one name", ErrHostContract)
		}
		name, ok := args[0].(string)
		if !ok {
			return nil, fmt.Errorf("%w: input name must be string", ErrHostContract)
		}
		inputs, _ := vm.hostInputs.(*MapVal)
		var value any
		found := false
		if inputs != nil {
			value, found = inputs.Data[name]
		}
		if !found {
			return ReturnValues{nil, fmt.Errorf("input %q not found", name)}, nil
		}
		copied, err := BridgeToHostWithLimits(value, vm.BridgeLimits)
		if err == nil {
			copied, err = BridgeToGuestWithLimits(copied, vm.BridgeLimits)
		}
		return ReturnValues{copied, err}, nil
	}}
	pkg.Funcs["Emit"] = &Function{Name: "Emit", Native: func(args []any) (any, error) {
		if len(args) != 2 {
			return nil, fmt.Errorf("%w: host.Emit expects name and value", ErrHostContract)
		}
		name, ok := args[0].(string)
		if !ok || name == "" {
			return errors.New("result name must be nonempty string"), nil
		}
		exec := vm.activeExecution
		if exec == nil {
			return nil, errors.New("nanogo: emit requires active execution")
		}
		if err := vm.chargeResult(name, args[1]); err != nil {
			if errors.Is(err, ErrBridgeValue) {
				return err, nil
			}
			return nil, err
		}
		value, err := BridgeToHostWithLimits(args[1], vm.BridgeLimits)
		if err != nil {
			return err, nil
		}
		exec.resultsMu.Lock()
		exec.results = append(exec.results, ResultEvent{Name: name, Value: value})
		exec.resultsMu.Unlock()
		return nil, nil
	}}
	for _, fn := range pkg.Funcs {
		native := fn.Native
		fn.Native = func(args []any) (value any, err error) {
			value, err = native(args)
			if err != nil {
				return nil, strictHostFailure(err)
			}
			if _, ok := value.(ReturnValues); !ok {
				return ReturnValues{value}, nil
			}
			return value, nil
		}
	}
	vm.RegisterPackage("nanogo/host", pkg)
	vm.declare("host", pkg, vm.globals)
}
func (vm *Interpreter) completeResults(exec *execution, err error) {
	exec.resultsMu.Lock()
	defer exec.resultsMu.Unlock()
	vm.resultsMu.Lock()
	defer vm.resultsMu.Unlock()
	vm.lastResults = ExecutionResults{Events: exec.results, Committed: err == nil}
}

// LastResults returns another deep copy, safe for the host to mutate. Never call
// this a live event stream: completion is published only after guest work joins.
func (vm *Interpreter) LastResults() ExecutionResults {
	vm.resultsMu.Lock()
	defer vm.resultsMu.Unlock()
	result := ExecutionResults{Committed: vm.lastResults.Committed, Events: make([]ResultEvent, len(vm.lastResults.Events))}
	for i, event := range vm.lastResults.Events {
		guestCopy, _ := bridgeToGuestUnchecked(event.Value)
		value, _ := bridgeToHostUnchecked(guestCopy)
		result.Events[i] = ResultEvent{Name: event.Name, Value: value}
	}
	return result
}

var ErrHostContract = errors.New("nanogo: host function contract")

// HostFunctionSpec declares exact argument and result counts. Callbacks validate
// application data types themselves. A callback error fails execution; include an
// error in the returned result slice for an ordinary guest-visible error result.
type HostFunctionSpec struct {
	Args, Results int
	Limits        BridgeLimits
}

// RegisterHostFunction adds a cancellation-aware, copying native adapter. It
// validates arity before invoking f and never passes guest pointers to f.
func (vm *Interpreter) RegisterHostFunction(name string, spec HostFunctionSpec, f func(context.Context, []any) ([]any, error)) error {
	if !validGuestIdentifier(name) || f == nil || spec.Args < 0 || spec.Results < 0 {
		return ErrHostContract
	}
	vm.RegisterNativeContext(name, func(ctx context.Context, args []any) (ret any, callErr error) {
		defer func() {
			if callErr != nil {
				callErr = strictHostFailure(callErr)
			}
		}()
		if len(args) != spec.Args {
			return nil, fmt.Errorf("%w: %s expects %d arguments, got %d", ErrHostContract, name, spec.Args, len(args))
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		copied := make([]any, len(args))
		for i, arg := range args {
			v, err := BridgeToHostWithLimits(arg, spec.Limits)
			if err != nil {
				return nil, err
			}
			copied[i] = v
		}
		values, err := f(ctx, copied)
		if err != nil {
			return nil, err
		}
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		if len(values) != spec.Results {
			return nil, fmt.Errorf("%w: %s returned %d values; expected %d", ErrHostContract, name, len(values), spec.Results)
		}
		out := make(ReturnValues, len(values))
		for i, v := range values {
			if e, ok := v.(error); ok {
				out[i] = errors.New(e.Error())
				continue
			}
			x, err := BridgeToGuestWithLimits(v, spec.Limits)
			if err != nil {
				return nil, err
			}
			out[i] = x
		}

		return out, nil
	})
	return nil
}

// strictHostError exempts new explicit-result APIs from the historical native
// two-target assignment adapter. Its cause remains available to errors.Is/As.
type strictHostError struct{ cause error }

func (e *strictHostError) Error() string { return e.cause.Error() }
func (e *strictHostError) Unwrap() error { return e.cause }
func strictHostFailure(err error) error {
	if err == nil {
		return nil
	}
	return &strictHostError{err}
}
