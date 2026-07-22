package interp

import (
	"context"
	"go/ast"
	"go/token"
	"strconv"
	"sync"
	"sync/atomic"
)

// buildLitCache parses every integer literal in file once up front. Called
// single-threaded, before any guest goroutine can run, so the resulting map
// is safe to read from multiple goroutines without synchronization for the
// rest of the execution.
func buildLitCache(file *ast.File) map[*ast.BasicLit]int {
	cache := make(map[*ast.BasicLit]int)
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.INT {
			return true
		}
		if v, err := strconv.ParseInt(lit.Value, 0, strconv.IntSize); err == nil {
			cache[lit] = int(v)
		}
		return true
	})
	return cache
}

// execution contains state that belongs to exactly one RunContext call.
// It is shared by guest goroutines, but never by two host executions.
type execution struct {
	ctx    context.Context
	cancel context.CancelFunc
	limits ExecutionLimits
	fset   *token.FileSet

	// litCache holds pre-parsed values for every *ast.BasicLit in the run's
	// source, built once (single-threaded, before main() runs) so tryEvalIntExpr
	// never re-runs strconv.ParseInt for the same literal on every visit — a
	// hot path for anything with a loop or recursion. Read-only for the rest
	// of the execution's life, including from guest goroutines, so it needs
	// no lock.
	litCache map[*ast.BasicLit]int

	killed     atomic.Bool
	cancelled  atomic.Bool // mirrors ctx.Done() without a channel op on the hot path — see beginExecution
	limitCause atomic.Uint32
	steps      atomic.Uint64
	goroutines atomic.Int64
	concurrent atomic.Bool
	wg         sync.WaitGroup
}

const (
	limitNone uint32 = iota
	limitSteps
	limitGoroutines
)

// err reports why execution should stop, or nil to continue. It runs on
// every single evaluator checkpoint (evalExpr and evalStmt both call it via
// executionError/checkpoint), so cancellation is read from the plain atomic
// bool a background goroutine already maintains (see beginExecution) rather
// than via `select { case <-e.ctx.Done(): ... default: }` here: a select
// still has to synchronize on the channel's internal lock even when it
// hits the default case, and profiling showed that cost was significant
// when paid on every node of a running program instead of exactly once per
// cancellation.
func (e *execution) err() error {
	switch e.limitCause.Load() {
	case limitSteps:
		return ErrStepLimit
	case limitGoroutines:
		return ErrGoroutineLimit
	}
	// Kill sets this flag before cancelling the context. Check it directly
	// rather than waiting for the cancellation-watcher goroutine to run, so
	// an evaluator or channel operation that observes the cancelled context
	// immediately still reports ErrKilled (not context.Canceled).
	if e.killed.Load() {
		return ErrKilled
	}
	if e.cancelled.Load() {
		return e.ctx.Err()
	}
	return nil
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
		e.concurrent.Store(true)
		return nil
	}
	if e.goroutines.Add(1) <= int64(e.limits.MaxGoroutines) {
		e.concurrent.Store(true)
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
	// The one place this execution's ctx.Done() channel is actually waited
	// on: cheaply (a parked goroutine, not a busy poll) and exactly once,
	// flipping e.cancelled for err()'s hot-path atomic read. This goroutine
	// always exits promptly — RunContext/WithExecution's defer calls
	// e.cancel() unconditionally, including on ordinary successful
	// completion, which closes Done() and unblocks it — so it never leaks
	// or outlives the execution it belongs to.
	go func() {
		<-ctx.Done()
		e.cancelled.Store(true)
	}()
	vm.execution.Store(e)
	vm.activeExecution = e
	return e, nil
}

func (vm *Interpreter) endExecution(e *execution) {
	vm.lastSteps.Store(e.steps.Load())
	vm.execution.CompareAndSwap(e, nil)
	vm.activeExecution = nil
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
	e := vm.activeExecution
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
