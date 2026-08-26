package interp

import (
	"context"
	"errors"
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

	killed    atomic.Bool
	cancelled atomic.Bool // mirrors parent cancellation without a channel op on the hot path — see beginExecution
	// stopped is the single healthy-path status read made by err. The detailed
	// atomics below are consulted only after a stop has been published, avoiding
	// four independent atomic loads at every evaluator checkpoint.
	stopped    atomic.Bool
	limitCause atomic.Uint32
	steps      atomic.Uint64
	goroutines atomic.Int64
	concurrent atomic.Bool
	wg         sync.WaitGroup

	// stopParentWatch unregisters the parent cancellation callback installed
	// by beginExecution. context.AfterFunc does not reserve a goroutine while
	// an execution is healthy, unlike a goroutine parked on ctx.Done().
	stopParentWatch func() bool
	failure         atomic.Pointer[executionFailure]
}

// executionFailure is immutable after publication. Keeping the error behind
// an atomic pointer lets evaluator checkpoints observe a worker failure
// without taking a lock on the hot path.
type executionFailure struct{ err error }

const (
	limitNone uint32 = iota
	limitSteps
	limitGoroutines
)

// err reports why execution should stop, or nil to continue. It runs on
// every single evaluator checkpoint (evalExpr and evalStmt both call it via
// executionError/checkpoint), so its healthy path reads only the stopped
// atomic maintained by cancellation and limit publishers rather than using
// `select { case <-e.ctx.Done(): ... default: }` here: a select still has to
// synchronize on the channel's internal lock even when it hits the default
// case, and profiling showed that cost was significant when paid on every
// node of a running program instead of exactly once per cancellation.
func (e *execution) err() error {
	if !e.stopped.Load() {
		return nil
	}
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
	if failure := e.failure.Load(); failure != nil {
		return failure.err
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
	active := e.goroutines.Add(1)
	if e.limits.MaxGoroutines <= 0 || active <= int64(e.limits.MaxGoroutines) {
		// Publish before the Go statement launches the child. The child and
		// its parent therefore both take Env locks for their whole overlap.
		e.concurrent.Store(true)
		return nil
	}
	e.goroutines.Add(-1)
	e.stopForLimit(limitGoroutines)
	return ErrGoroutineLimit
}

func (e *execution) releaseGoroutine() {
	if e.goroutines.Add(-1) == 0 {
		// Once the final guest child has returned only the root evaluator is
		// left, so Env operations can recover their lock-free fast path.
		e.concurrent.Store(false)
	}
}

func (e *execution) stopForLimit(cause uint32) {
	e.limitCause.CompareAndSwap(limitNone, cause)
	e.stopped.Store(true)
	e.cancel()
}

func (e *execution) kill() {
	e.killed.Store(true)
	e.stopped.Store(true)
	e.cancel()
}

// recordWorkerError publishes the first real guest-goroutine failure and
// cancels sibling work. Context errors that arrive after an execution has
// already been cancelled are expected teardown, not a new program failure.
func (e *execution) recordWorkerError(err error) {
	if err == nil || e.ctx.Err() != nil {
		return
	}
	if e.failure.CompareAndSwap(nil, &executionFailure{err: err}) {
		e.stopped.Store(true)
		e.cancel()
	}
}

// finalError keeps a root error when it is more specific, but surfaces a
// worker failure that woke the root through context cancellation or that
// happened just as the root returned successfully.
func (e *execution) finalError(err error) error {
	failure := e.failure.Load()
	if failure == nil {
		return err
	}
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return failure.err
	}
	return err
}

// finish cancels all remaining guest work when the root invocation returns.
// Store cancelled after cancellation so evaluator-only busy loops observe the
// teardown as well as channel and select waits. This is especially important
// for Run(), whose background parent has no cancellation callback.
func (e *execution) finish() {
	if e.stopParentWatch != nil {
		e.stopParentWatch()
	}
	e.cancel()
	e.cancelled.Store(true)
	e.stopped.Store(true)
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
	// A background context cannot be cancelled by its parent. For a
	// cancellable parent, AfterFunc registers an efficient callback without
	// starting a parked goroutine for every RunContext call. The callback
	// cancels the child itself before publishing the flag, so err() never sees
	// a true flag paired with a still-live child context.
	if parent.Done() != nil {
		e.stopParentWatch = context.AfterFunc(parent, func() {
			cancel()
			e.cancelled.Store(true)
			e.stopped.Store(true)
		})
	}
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

// cancellationError is used after a context wait has already unblocked. It
// preserves an interpreter-specific stop reason (limit, Kill, worker failure)
// when it has been published; otherwise it returns the context's precise
// cancellation error even if its lightweight callback has not run yet.
func (vm *Interpreter) cancellationError() error {
	if e := vm.activeExecution; e != nil {
		if err := e.err(); err != nil {
			return err
		}
	}
	return contextError(vm.Context())
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
