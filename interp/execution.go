package interp

import (
	"context"
	"sync"
	"sync/atomic"
)

// execution contains state that belongs to exactly one RunContext call.
// It is shared by guest goroutines, but never by two host executions.
type execution struct {
	ctx    context.Context
	cancel context.CancelFunc
	limits ExecutionLimits

	killed     atomic.Bool
	limitCause atomic.Uint32
	steps      atomic.Uint64
	goroutines atomic.Int64
	wg         sync.WaitGroup
}

const (
	limitNone uint32 = iota
	limitSteps
	limitGoroutines
)

func (e *execution) err() error {
	switch e.limitCause.Load() {
	case limitSteps:
		return ErrStepLimit
	case limitGoroutines:
		return ErrGoroutineLimit
	}
	select {
	case <-e.ctx.Done():
		if e.killed.Load() {
			return ErrKilled
		}
		return e.ctx.Err()
	default:
		return nil
	}
}

func (e *execution) checkpoint() error {
	if err := e.err(); err != nil {
		return err
	}
	if e.limits.MaxSteps == 0 {
		return nil
	}
	if e.steps.Add(1) <= e.limits.MaxSteps {
		return nil
	}
	e.stopForLimit(limitSteps)
	return ErrStepLimit
}

func (e *execution) reserveGoroutine() error {
	if err := e.err(); err != nil {
		return err
	}
	if e.limits.MaxGoroutines <= 0 {
		return nil
	}
	if e.goroutines.Add(1) <= int64(e.limits.MaxGoroutines) {
		return nil
	}
	e.goroutines.Add(-1)
	e.stopForLimit(limitGoroutines)
	return ErrGoroutineLimit
}

func (e *execution) releaseGoroutine() {
	if e.limits.MaxGoroutines > 0 {
		e.goroutines.Add(-1)
	}
}

func (e *execution) stopForLimit(cause uint32) {
	e.limitCause.CompareAndSwap(limitNone, cause)
	e.cancel()
}

func (e *execution) kill() {
	e.killed.Store(true)
	e.cancel()
}

func (vm *Interpreter) beginExecution(parent context.Context) (*execution, error) {
	if parent == nil {
		parent = context.Background()
	}
	vm.runMu.Lock()
	if err := parent.Err(); err != nil {
		vm.runMu.Unlock()
		return nil, err
	}
	ctx, cancel := context.WithCancel(parent)
	e := &execution{ctx: ctx, cancel: cancel, limits: vm.Limits}
	vm.execution.Store(e)
	return e, nil
}

func (vm *Interpreter) endExecution(e *execution) {
	vm.execution.CompareAndSwap(e, nil)
	vm.runMu.Unlock()
}

// Context returns the active execution context. It is primarily useful inside
// RegisterNative callbacks. Outside a run it returns context.Background().
func (vm *Interpreter) Context() context.Context {
	e := vm.execution.Load()
	if e == nil {
		return context.Background()
	}
	return e.ctx
}

func (vm *Interpreter) executionError() error {
	e := vm.execution.Load()
	if e == nil {
		return nil
	}
	return e.checkpoint()
}

// Kill cooperatively stops the currently running program. It interrupts
// evaluator work, guest channel operations, select, range, and context-aware
// native functions. A legacy native that blocks forever cannot be force-killed
// by Go; use RegisterNativeContext for such host functions.
func (vm *Interpreter) Kill() bool {
	e := vm.execution.Load()
	if e == nil {
		return false
	}
	e.kill()
	return true
}

// IsRunning reports whether this interpreter currently owns an execution.
func (vm *Interpreter) IsRunning() bool {
	return vm.execution.Load() != nil
}

func (vm *Interpreter) maxContainerSize() int {
	if vm.MaxContainerSize > 0 {
		return vm.MaxContainerSize
	}
	return DefaultMaxContainerSize
}
