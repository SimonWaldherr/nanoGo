// interp/entry.go
package interp

import (
	"context"
	"go/token"
)

// WithExecution begins one execution scope (step/goroutine limits,
// cancellation, tracing) exactly like RunContext does internally, runs fn
// inside it, and ends the execution afterward. fset is attached to the
// execution so trace/panic locations resolve correctly; pass the FileSet
// used to parse whatever fn will evaluate (nil is fine if unavailable).
//
// Hosts building multi-package programs (see interp/loader) use this to
// wrap an entire package-build-and-run sequence in a single execution, so
// package-level var initializers and init() functions are covered by the
// same step-limit and cancellation guarantees as a plain RunContext call —
// not just the final entry-point call.
func (vm *Interpreter) WithExecution(ctx context.Context, fset *token.FileSet, fn func() error) (err error) {
	exec, err := vm.beginExecution(ctx)
	if err != nil {
		return err
	}
	exec.fset = fset
	vm.emitTrace("run_start", "program", "", nil)
	defer func() {
		exec.cancel()
		exec.wg.Wait()
		message := "ok"
		if err != nil {
			message = err.Error()
		}
		vm.emitTrace("run_end", "program", message, nil)
		vm.endExecution(exec)
	}()

	if err := exec.err(); err != nil {
		return err
	}
	if err := fn(); err != nil {
		if execErr := exec.err(); execErr != nil {
			return execErr
		}
		return err
	}
	return exec.err()
}

// Invoke calls fn with args, the same way a guest call expression would. It
// is meant to run inside an active execution — established by WithExecution
// or CallEntry — so step limits and cancellation apply; called with no
// active execution it runs unbounded, matching vm.Context()'s existing
// fallback-to-Background behavior when idle.
func (vm *Interpreter) Invoke(fn *Function, args []any) (any, error) {
	return vm.callFunction(fn, fn.Env, nil, args)
}

// CallEntry runs fn under a fresh execution scope: cancellation, step and
// goroutine limits, and tracing all behave exactly like a RunContext call
// whose only top-level statement is fn(args...). It is the building block
// interp/loader uses for one-shot invocations — an entry point, a single
// *_test.go Test/Benchmark function — where each call gets its own,
// independent step budget (mirroring how `go test` runs each test in
// isolation).
func (vm *Interpreter) CallEntry(ctx context.Context, fset *token.FileSet, fn *Function, args []any) (any, error) {
	var ret any
	err := vm.WithExecution(ctx, fset, func() error {
		v, invokeErr := vm.Invoke(fn, args)
		ret = v
		return invokeErr
	})
	return ret, err
}

// StepCount returns the number of evaluator checkpoints (expressions and
// statements) consumed so far by the currently active execution, or 0 if
// none is active. It is primarily useful for deterministic benchmarking
// (see interp/loader's RunFunctionBench): unlike wall-clock time, steps are
// counted identically across machines and runs.
func (vm *Interpreter) StepCount() uint64 {
	e := vm.execution.Load()
	if e == nil {
		return 0
	}
	return e.steps.Load()
}

// LastStepCount returns the final step counter of the most recently completed
// execution, or 0 if this interpreter has never run. Unlike StepCount it works
// after Run/RunContext has returned, so hosts (the WASM playground, CLIs) can
// report the deterministic cost of the run they just performed.
func (vm *Interpreter) LastStepCount() uint64 {
	return vm.lastSteps.Load()
}
