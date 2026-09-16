package interp

import (
	"errors"
	"fmt"
	"sync/atomic"
)

var (
	ErrCallDepthLimit  = errors.New("nanogo: active call limit exceeded")
	ErrOutputLimit     = errors.New("nanogo: output byte limit exceeded")
	ErrAllocationLimit = errors.New("nanogo: guest allocation budget exceeded")
	ErrResultLimit     = errors.New("nanogo: result byte limit exceeded")
)

// LimitError describes a refused operation. Used includes its requested cost.
// Unwrap preserves errors.Is against the corresponding resource sentinel.
type LimitError struct {
	Resource string `json:"resource"`
	Maximum  uint64 `json:"maximum"`
	Used     uint64 `json:"used"`
	cause    error
}

func (e *LimitError) Error() string {
	return fmt.Sprintf("nanogo: %s limit exceeded (%d > %d)", e.Resource, e.Used, e.Maximum)
}
func (e *LimitError) Unwrap() error { return e.cause }
func (vm *Interpreter) charge(counter *atomic.Uint64, maximum, n uint64, resource string, cause error) error {
	if maximum == 0 {
		return nil
	}
	for {
		used := counter.Load()
		if n > maximum-used {
			requested := used + n
			if requested < used {
				requested = ^uint64(0)
			}
			err := &LimitError{resource, maximum, requested, cause}
			vm.activeExecution.recordWorkerError(err)
			return err
		}
		if counter.CompareAndSwap(used, used+n) {
			return nil
		}
	}
}

var noLeave = func() {}

// enterCall counts all simultaneously active guest/native calls across guest
// goroutines. With one goroutine it is call depth; with several it is a shared
// frame budget. A disabled limit adds no frame bookkeeping.
func (vm *Interpreter) enterCall() (func(), error) {
	e := vm.activeExecution
	if e == nil || e.limits.MaxCallDepth == 0 {
		return noLeave, nil
	}
	if err := vm.charge(&e.calls, e.limits.MaxCallDepth, 1, "calls", ErrCallDepthLimit); err != nil {
		return noLeave, err
	}
	return func() { e.calls.Add(^uint64(0)) }, nil
}

// chargeAllocation counts requested container element slots cumulatively.
func (vm *Interpreter) chargeAllocation(n uint64) error {
	e := vm.activeExecution
	if e == nil {
		return nil
	}
	return vm.charge(&e.allocationUnits, e.limits.MaxAllocationUnits, n, "allocationUnits", ErrAllocationLimit)
}
func (vm *Interpreter) budgetNativeOutput(name string, f func([]any) (any, error)) func([]any) (any, error) {
	if name != "ConsoleLog" && name != "ConsoleWarn" && name != "ConsoleError" {
		return f
	}
	return func(args []any) (any, error) {
		if e := vm.activeExecution; e != nil && e.limits.MaxOutputBytes != 0 {
			n := uint64(0)
			for _, arg := range args {
				n += uint64(len(ToString(arg)))
			}
			if err := vm.charge(&e.outputBytes, e.limits.MaxOutputBytes, n, "outputBytes", ErrOutputLimit); err != nil {
				return nil, err
			}
		}
		return f(args)
	}
}
func (vm *Interpreter) chargeResult(name string, value any) error {
	e := vm.activeExecution
	if e == nil || e.limits.MaxResultBytes == 0 {
		return nil
	}
	v := bridgeValidator{limits: vm.BridgeLimits.defaults(), active: map[bridgeVisit]bool{}}
	if err := v.walk(value, 1, true); err != nil {
		return err
	}
	// Charge at least a fixed event/name cost so empty emissions are bounded.
	return vm.charge(&e.resultBytes, e.limits.MaxResultBytes, v.bytes+uint64(len(name))+16, "resultBytes", ErrResultLimit)
}

func (vm *Interpreter) rejectValueAllocation(resource string, maximum, used uint64) {
	if e := vm.activeExecution; e != nil {
		e.recordWorkerError(&LimitError{Resource: resource, Maximum: maximum, Used: used, cause: ErrAllocationLimit})
	}
}
