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
	stopped atomic.Bool
	// fastCallsAllowed is snapshotted at run start so call dispatch can
	// quickly choose the frame-free path without re-reading global debug/callback
	// state for every single call.
	fastCallsAllowed bool
	// trackVariables indicates whether variable snapshots are configured for this
	// execution, so hot assignment paths can avoid checking atomics.
	trackVariables bool
	limitCause     atomic.Uint32
	// steps is the authoritative checkpoint counter, but it is charged a
	// whole stepBlock at a time while the execution is single-goroutine (see
	// chargeStep). Between block boundaries it therefore runs ahead of what
	// has actually been consumed by exactly stepsLeft; refundSteps settles
	// that difference at every point where the number is observed or where
	// ownership of stepsLeft changes hands.
	steps atomic.Uint64
	// stepsLeft is the unconsumed remainder of the block most recently
	// reserved from steps. It is a plain field, not an atomic: a block is
	// only ever reserved and drawn down while exactly one goroutine is
	// running guest code, which is the same invariant (and the same
	// concurrent flag) that already lets Env lookups skip locking — see
	// Interpreter.get. reserveGoroutine hands the remainder back before a
	// second goroutine can exist, so no two goroutines ever touch it.
	stepsLeft  uint64
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

// stepBlock is how many checkpoints a single-goroutine execution reserves
// from steps in one atomic operation. Charging every checkpoint individually
// cost a locked read-modify-write per visited AST node, which profiling
// showed to be the largest single item in the interpreter's own CPU profile
// (~17% of total runtime on both the arithmetic-loop and the recursive-call
// benchmark). Drawing a block down through an ordinary field turns the common
// case into a load/decrement/store while chargeStep keeps the *observable*
// behavior identical: the limit still fires on exactly the same checkpoint it
// did before, because an over-reservation is trimmed back to MaxSteps rather
// than granted (see chargeStep), and refundSteps returns whatever remains
// unconsumed before the counter is read.
const stepBlock = 256

func (e *execution) checkpoint() error {
	if e.stopped.Load() {
		if err := e.err(); err != nil {
			return err
		}
	}
	if e.stepsLeft > 0 {
		e.stepsLeft--
		return nil
	}
	return e.chargeStep()
}

// chargeStep is checkpoint's slow path: the step limit is disabled, this
// execution has gone concurrent, or the reserved block just ran out.
func (e *execution) chargeStep() error {
	max := e.limits.MaxSteps
	if e.concurrent.Load() {
		// Several guest goroutines are running, so stepsLeft — which has a
		// single owner by construction — must not be written here at all.
		// Charge each checkpoint directly instead: that also keeps the limit
		// from depending on how work happened to be distributed between the
		// goroutines, which a per-goroutine private block would.
		if max == 0 {
			return nil
		}
		if e.steps.Add(1) <= max {
			return nil
		}
		e.stopForLimit(limitSteps)
		return ErrStepLimit
	}
	// Below here this is the only goroutine running guest code, so it owns
	// stepsLeft (see the field's comment).
	if max == 0 {
		// No limit configured, so nothing is counted — matching the
		// long-standing behavior that StepCount reports 0 for an execution
		// running without a step budget. Still hand out a block, purely so
		// checkpoint keeps taking its inline fast path instead of calling
		// down here for every node of a trusted, unlimited workload.
		e.stepsLeft = stepBlock
		return nil
	}
	reserved := e.steps.Add(stepBlock)
	if reserved > max {
		// The block overshot the budget. Give back the part that lies beyond
		// it so steps never reports more than MaxSteps, and grant only the
		// checkpoints that genuinely still fit.
		excess := reserved - max
		if excess >= stepBlock {
			// Nothing of this block fits. Charging one checkpoint per step
			// would have left the counter at MaxSteps+1 here — the step that
			// tripped the limit is counted — so land on exactly that, which
			// is what LastStepCount reports for a run that hit its budget.
			// steps was necessarily sitting at exactly MaxSteps: this branch
			// is only reachable once a previous block was trimmed to it.
			subSteps(&e.steps, stepBlock-1)
			e.stopForLimit(limitSteps)
			return ErrStepLimit
		}
		subSteps(&e.steps, excess)
		e.stepsLeft = stepBlock - excess - 1
		return nil
	}
	e.stepsLeft = stepBlock - 1
	return nil
}

// refundSteps returns the unconsumed remainder of the current block to steps,
// making the counter exact again. It must be called before anything reads
// steps, and before ownership of stepsLeft could pass to another goroutine.
// Callers must be the goroutine that owns the block: the sole guest goroutine
// (checkpoint's fast path can only ever run there) or a host between guest
// calls. While the execution is concurrent stepsLeft is already zero, so this
// is a no-op rather than a race.
func (e *execution) refundSteps() {
	if e.stepsLeft == 0 {
		return
	}
	if e.limits.MaxSteps == 0 {
		// An unlimited execution's block was never charged to steps (see
		// chargeStep), so there is nothing to give back — only the local
		// remainder to clear.
		e.stepsLeft = 0
		return
	}
	subSteps(&e.steps, e.stepsLeft)
	e.stepsLeft = 0
}

// subSteps decrements an atomic.Uint64 by n. atomic.Uint64.Add only takes an
// unsigned delta, so the decrement is expressed as its two's complement,
// which wraps to exactly the same result.
func subSteps(counter *atomic.Uint64, n uint64) { counter.Add(^(n - 1)) }

func (e *execution) reserveGoroutine() error {
	if err := e.err(); err != nil {
		return err
	}
	active := e.goroutines.Add(1)
	if e.limits.MaxGoroutines <= 0 || active <= int64(e.limits.MaxGoroutines) {
		if active == 1 {
			// This is the 0->1 transition, so the caller is still the only
			// goroutine running guest code and therefore owns the current
			// step block. Hand the remainder back before a second goroutine
			// can exist: from here on every checkpoint charges steps
			// directly, and nothing may touch stepsLeft until the last child
			// has cleared the concurrent flag again.
			e.refundSteps()
		}
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
	// The root evaluator has returned, so whatever it still held of its step
	// block is unconsumed. Settle it before endExecution publishes the total
	// as LastStepCount; guest goroutines that are still winding down charge
	// steps directly and are unaffected.
	e.refundSteps()
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
	e := &execution{
		ctx:    ctx,
		cancel: cancel,
		limits: vm.Limits,
		fastCallsAllowed: vm.tracer.Load() == nil &&
			!vm.runtimeTraceAnnotations.Load() &&
			vm.variableTracker.Load() == nil &&
			vm.debugController.Load() == nil &&
			!vm.stackFramesRequired.Load(),
		trackVariables: vm.variableTracker.Load() != nil,
	}
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
