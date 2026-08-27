// interp/debugger.go
package interp

import (
	"fmt"
	"go/ast"
	"go/parser"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// StepMode selects what a resumed goroutine should do before pausing again.
type StepMode int32

const (
	// DebugRun keeps the goroutine running until it hits a breakpoint or an
	// explicit Pause() request.
	DebugRun StepMode = iota
	// DebugStepInto pauses at the very next statement checkpoint reached by
	// the SAME goroutine, whether that statement is at the same level, is a
	// newly entered call, or is back in a caller after a return.
	DebugStepInto
	// DebugStepOver pauses at the next statement checkpoint in the same
	// frame, or (if the current call returns first) in an ancestor of it —
	// it does not stop inside a call newly entered from the stepped line.
	DebugStepOver
	// DebugStepOut pauses only once execution has returned to a strict
	// ancestor of the frame the step command was issued from.
	DebugStepOut
)

func (m StepMode) String() string {
	switch m {
	case DebugStepInto:
		return "step-into"
	case DebugStepOver:
		return "step-over"
	case DebugStepOut:
		return "step-out"
	default:
		return "run"
	}
}

// PauseToken identifies one paused goroutine. A host addresses Continue/
// StepInto/StepOver/StepOut at a specific token so it can resume the right
// goroutine when several are paused at once (see DebugController's doc
// comment on concurrency).
type PauseToken uint64

// DebugPauseInfo snapshots interpreter state at one pause point. It is safe
// to hold onto and serialize (e.g. to JSON for a browser host) after the
// evaluator goroutine has moved on, since it contains only copied values.
type DebugPauseInfo struct {
	Token    PauseToken         `json:"token"`
	Location SourceLocation     `json:"location"`
	Function string             `json:"function"`
	Depth    int                `json:"depth"`
	Stack    string             `json:"stack"`
	Vars     []VariableSnapshot `json:"vars"`
	// Reason is "breakpoint", "pause", "step-into", "step-over",
	// "step-out", or "watchpoint" — whichever caused this checkpoint to stop.
	Reason string `json:"reason"`
	Watch  string `json:"watch,omitempty"`
}

type debugBreakpoint struct {
	cond    ast.Expr
	condSrc string
}

type debugWatch struct {
	expr    ast.Expr
	exprSrc string
}

// DebugController drives a live pause/step debugging session against one
// interpreter's execution. Attach it with SetDebugController before Run/
// RunContext; a nil controller (the default) costs a single atomic pointer
// load per statement and never blocks.
//
// A blocked checkpoint is real: the goroutine that called Run/RunContext (or
// that is running a guest `go` statement) parks until the host calls one of
// Continue/StepInto/StepOver/StepOut for that pause's token, or the
// execution's context is cancelled/killed. A host that attaches a
// DebugController must therefore run Run/RunContext on a goroutine it can
// afford to block, and drive Pause/Continue/Step* from another one — see
// [Interpreter.SetDebugController].
//
// Concurrency: nanoGo guest programs may run multiple goroutines. Each one
// that hits a breakpoint or matches the active step mode pauses
// independently and gets its own PauseToken, so several pauses can be
// outstanding at once; OnPause fires once per pause. Stepping mode
// (StepInto/Over/Out) itself is a single shared session, though — only the
// goroutine most recently told to step is tracked by frame identity, so
// issuing a step command while a second goroutine is also paused steers
// only the targeted one; the other keeps running free until its own
// breakpoint or an explicit Pause().
type DebugController struct {
	bpMu        sync.RWMutex
	breakpoints map[int]debugBreakpoint
	watchMu     sync.RWMutex
	watches     map[string]debugWatch
	watchEpoch  *execution
	watchValues map[any]map[string]string
	watchActive atomic.Bool

	pauseRequested atomic.Bool
	detached       atomic.Bool

	mode      atomic.Int32
	baseFrame atomic.Pointer[callFrame]

	pendingMu sync.Mutex
	pending   map[PauseToken]*pendingPause
	nextToken atomic.Uint64

	onPauseMu sync.RWMutex
	onPause   func(DebugPauseInfo)
}

// pendingPause is what checkpoint registers for one paused goroutine: the
// channel it blocks on, plus enough to let a host inspect or mutate that
// exact pause's scope (via SetVariable) while it waits. env is only ever
// touched by the paused goroutine itself (once resumed) and by SetVariable
// calls that arrive before then — pendingMu's Lock/Unlock around every
// lookup and removal of a token's entry is what makes those safe to
// interleave with the resume, not any lock on env itself.
type pendingPause struct {
	ch  chan StepMode
	vm  *Interpreter
	env *Env
}

// NewDebugController creates a debug session with no breakpoints, in free
// run mode (it only stops on an explicit Pause() until breakpoints or a
// step command are configured).
func NewDebugController() *DebugController {
	return &DebugController{
		breakpoints: make(map[int]debugBreakpoint),
		watches:     make(map[string]debugWatch),
		watchValues: make(map[any]map[string]string),
		pending:     make(map[PauseToken]*pendingPause),
	}
}

// SetDebugController installs (or removes with nil) the live debugging
// session used by subsequent statement checkpoints.
func (vm *Interpreter) SetDebugController(dc *DebugController) {
	vm.debugController.Store(dc)
}

// DebugController returns the currently attached session, or nil.
func (vm *Interpreter) DebugController() *DebugController {
	return vm.debugController.Load()
}

// OnPause installs a callback invoked synchronously, on the pausing
// goroutine, immediately before it blocks. It must return promptly — e.g.
// hand the info to a channel, or invoke an async host callback — rather
// than doing its own long-running work, since it runs inline on the guest's
// own goroutine. A nil fn (the default) disables the callback; hosts that
// only need synchronous polling can skip it and rely on Continue/StepOver's
// return values plus their own bookkeeping instead.
func (dc *DebugController) OnPause(fn func(DebugPauseInfo)) {
	dc.onPauseMu.Lock()
	dc.onPause = fn
	dc.onPauseMu.Unlock()
}

// SetBreakpoints installs plain (unconditional) source-line breakpoints,
// replacing any previously configured breakpoints or conditions.
func (dc *DebugController) SetBreakpoints(lines []int) {
	next := make(map[int]debugBreakpoint, len(lines))
	for _, line := range lines {
		if line > 0 {
			next[line] = debugBreakpoint{}
		}
	}
	dc.bpMu.Lock()
	dc.breakpoints = next
	dc.bpMu.Unlock()
}

// SetConditionalBreakpoint arms a breakpoint at line that only pauses when
// expr — a single Go expression evaluated in the paused statement's own
// scope — is truthy. An empty expr clears the condition (equivalent to a
// plain breakpoint at that line); a parse error is returned immediately
// without changing the existing breakpoint set.
func (dc *DebugController) SetConditionalBreakpoint(line int, expr string) error {
	if line <= 0 {
		return NewRuntimeError("SetConditionalBreakpoint: line must be positive")
	}
	bp := debugBreakpoint{}
	if expr != "" {
		cond, err := parser.ParseExpr(expr)
		if err != nil {
			return fmt.Errorf("nanogo: invalid breakpoint condition: %w", err)
		}
		bp.cond, bp.condSrc = cond, expr
	}
	dc.bpMu.Lock()
	if dc.breakpoints == nil {
		dc.breakpoints = make(map[int]debugBreakpoint)
	}
	dc.breakpoints[line] = bp
	dc.bpMu.Unlock()
	return nil
}

// ClearBreakpoints removes every configured breakpoint (plain or
// conditional).
func (dc *DebugController) ClearBreakpoints() {
	dc.bpMu.Lock()
	dc.breakpoints = make(map[int]debugBreakpoint)
	dc.bpMu.Unlock()
}

// Breakpoints returns a sorted snapshot of configured breakpoint lines,
// each with its condition source if one is set.
func (dc *DebugController) Breakpoints() map[int]string {
	dc.bpMu.RLock()
	defer dc.bpMu.RUnlock()
	out := make(map[int]string, len(dc.breakpoints))
	for line, bp := range dc.breakpoints {
		out[line] = bp.condSrc
	}
	return out
}

func (dc *DebugController) breakpointAt(line int) (debugBreakpoint, bool) {
	dc.bpMu.RLock()
	bp, ok := dc.breakpoints[line]
	dc.bpMu.RUnlock()
	return bp, ok
}

// SetWatch adds or replaces a data watchpoint. The expression is evaluated at
// statement checkpoints while this controller is attached; execution pauses
// when its displayed value changes. Watchpoints are debugger-only work and do
// not add checks or state to a normal run without a DebugController.
func (dc *DebugController) SetWatch(name, expr string) error {
	if dc == nil {
		return NewRuntimeError("nanogo: nil debug controller")
	}
	if strings.TrimSpace(name) == "" {
		return NewRuntimeError("nanogo: watch name must not be empty")
	}
	if strings.TrimSpace(expr) == "" {
		return NewRuntimeError("nanogo: watch expression must not be empty")
	}
	parsed, err := parser.ParseExpr(expr)
	if err != nil {
		return fmt.Errorf("nanogo: invalid watch expression: %w", err)
	}
	dc.watchMu.Lock()
	if dc.watches == nil {
		dc.watches = make(map[string]debugWatch)
	}
	if dc.watchValues == nil {
		dc.watchValues = make(map[any]map[string]string)
	}
	dc.watches[name] = debugWatch{expr: parsed, exprSrc: expr}
	// Replacing a watch establishes a fresh baseline on its next checkpoint,
	// rather than comparing values from the previous expression.
	for _, values := range dc.watchValues {
		delete(values, name)
	}
	dc.watchMu.Unlock()
	return nil
}

// ClearWatch removes one watchpoint. It is safe to call while the program is
// running; the next checkpoint sees the updated watchpoint set.
func (dc *DebugController) ClearWatch(name string) {
	if dc == nil {
		return
	}
	dc.watchMu.Lock()
	delete(dc.watches, name)
	for _, values := range dc.watchValues {
		delete(values, name)
	}
	dc.watchMu.Unlock()
}

// Watches returns the configured watchpoint expressions keyed by host name.
func (dc *DebugController) Watches() map[string]string {
	if dc == nil {
		return nil
	}
	dc.watchMu.RLock()
	defer dc.watchMu.RUnlock()
	out := make(map[string]string, len(dc.watches))
	for name, watch := range dc.watches {
		out[name] = watch.exprSrc
	}
	return out
}

// Pause requests a stop at the very next statement checkpoint reached by
// any guest goroutine, regardless of breakpoints or step mode.
func (dc *DebugController) Pause() {
	dc.pauseRequested.Store(true)
}

// Detach resumes every currently paused goroutine in free-run mode and
// makes the controller inert: future checkpoints become no-ops until the
// host calls SetDebugController again (with this controller or a fresh
// one). It does not stop the underlying execution — combine with
// Interpreter.Kill for that.
func (dc *DebugController) Detach() {
	dc.detached.Store(true)
	dc.pendingMu.Lock()
	pending := dc.pending
	dc.pending = make(map[PauseToken]*pendingPause)
	dc.pendingMu.Unlock()
	for _, p := range pending {
		p.ch <- DebugRun
	}
}

func (dc *DebugController) resume(token PauseToken, mode StepMode) bool {
	dc.pendingMu.Lock()
	p, ok := dc.pending[token]
	if ok {
		delete(dc.pending, token)
	}
	dc.pendingMu.Unlock()
	if !ok {
		return false
	}
	p.ch <- mode
	return true
}

// SetVariable assigns the value of expr — a single Go expression, evaluated
// in the paused statement's own scope — to name, in the goroutine paused at
// token. It is safe to call while that goroutine is parked in checkpoint()
// waiting to be resumed (see pendingPause's doc comment for why), but has no
// effect once it has resumed: token stops being valid the moment Continue/
// StepInto/StepOver/StepOut/Detach removes it from the pending set, and
// SetVariable then reports "not paused" like any other stale token. It
// reports an error if name isn't an existing binding visible from that
// scope — this only edits variables, it does not declare new ones — or if
// expr fails to parse or evaluate. On success it returns the same
// capability-safe display string a VariableSnapshot would show.
func (dc *DebugController) SetVariable(token PauseToken, name, expr string) (string, error) {
	dc.pendingMu.Lock()
	p, ok := dc.pending[token]
	dc.pendingMu.Unlock()
	if !ok {
		return "", NewRuntimeError("nanogo: no goroutine is paused at that token")
	}
	if _, found := p.vm.get(name, p.env); !found {
		return "", NewRuntimeError("nanogo: undefined variable " + name)
	}
	valueExpr, err := parser.ParseExpr(expr)
	if err != nil {
		return "", fmt.Errorf("nanogo: invalid value expression: %w", err)
	}
	value, err := p.vm.evalExpr(valueExpr, p.env)
	if err != nil {
		return "", err
	}
	p.vm.set(name, value, p.env)
	return debugValue(value), nil
}

// Evaluate evaluates an expression in the paused goroutine's scope and
// returns the same capability-safe display representation used by Vars.
// Like SetVariable, it is valid only while token remains paused. Evaluation
// does not assign a value, but expressions that call guest/native functions
// may still have those functions' normal side effects.
func (dc *DebugController) Evaluate(token PauseToken, expr string) (string, error) {
	dc.pendingMu.Lock()
	p, ok := dc.pending[token]
	dc.pendingMu.Unlock()
	if !ok {
		return "", NewRuntimeError("nanogo: no goroutine is paused at that token")
	}
	valueExpr, err := parser.ParseExpr(expr)
	if err != nil {
		return "", fmt.Errorf("nanogo: invalid evaluation expression: %w", err)
	}
	value, err := p.vm.evalExpr(valueExpr, p.env)
	if err != nil {
		return "", err
	}
	return debugValue(value), nil
}

// Continue resumes the goroutine paused at token in free-run mode. It
// reports false if token is not (or no longer) paused.
func (dc *DebugController) Continue(token PauseToken) bool { return dc.resume(token, DebugRun) }

// StepInto resumes the goroutine paused at token, pausing again at the very
// next statement it evaluates (same frame, a newly entered call, or a
// caller reached by returning).
func (dc *DebugController) StepInto(token PauseToken) bool { return dc.resume(token, DebugStepInto) }

// StepOver resumes the goroutine paused at token, running through any call
// made from the paused statement without stopping inside it.
func (dc *DebugController) StepOver(token PauseToken) bool { return dc.resume(token, DebugStepOver) }

// StepOut resumes the goroutine paused at token, running until its current
// function returns to a caller.
func (dc *DebugController) StepOut(token PauseToken) bool { return dc.resume(token, DebugStepOut) }

// frameChainContains walks start's caller chain (start itself included) and
// reports whether target appears in it. A nil target never matches.
func frameChainContains(start, target *callFrame) bool {
	if target == nil {
		return false
	}
	for f := start; f != nil; f = f.caller {
		if f == target {
			return true
		}
	}
	return false
}

// sameLineage reports whether a and b belong to the same goroutine's frame
// chain — one is (transitively) an ancestor of, or equal to, the other.
// Two different goroutines' chains never intersect (each is rooted at a
// nil caller), so this also correctly says "no" across goroutines.
func sameLineage(a, b *callFrame) bool {
	return frameChainContains(a, b) || frameChainContains(b, a)
}

type debugWatchEntry struct {
	name  string
	watch debugWatch
}

// changedWatch evaluates configured watches only while a debugger is active.
// Values are tracked per activation frame (or per Env for frame-free calls),
// so independent guest goroutines do not overwrite one another's baselines.
func (dc *DebugController) changedWatch(vm *Interpreter, env *Env) string {
	// Watch expressions may call guest functions, whose statements reach this
	// same checkpoint recursively. Suppress nested watch evaluation while the
	// outer sample is active; the outer expression still observes the result.
	if !dc.watchActive.CompareAndSwap(false, true) {
		return ""
	}
	defer dc.watchActive.Store(false)

	epoch := vm.activeExecution
	key := any(env)
	if env != nil && env.frame != nil {
		key = env.frame
	}

	dc.watchMu.Lock()
	if dc.watchEpoch != epoch {
		dc.watchEpoch = epoch
		dc.watchValues = make(map[any]map[string]string)
	}
	if dc.watchValues == nil {
		dc.watchValues = make(map[any]map[string]string)
	}
	if len(dc.watches) == 0 {
		dc.watchMu.Unlock()
		return ""
	}
	entries := make([]debugWatchEntry, 0, len(dc.watches))
	for name, watch := range dc.watches {
		entries = append(entries, debugWatchEntry{name: name, watch: watch})
	}
	dc.watchMu.Unlock()

	var changed string
	for _, entry := range entries {
		value, err := vm.evalExpr(entry.watch.expr, env)
		if err != nil {
			// A watch can be out of scope on one frame. Keep its old baseline
			// until it becomes evaluable again instead of stopping execution.
			continue
		}
		rendered := debugValue(value)
		dc.watchMu.Lock()
		values := dc.watchValues[key]
		if values == nil {
			values = make(map[string]string, len(entries))
			dc.watchValues[key] = values
		}
		previous, initialized := values[entry.name]
		values[entry.name] = rendered
		dc.watchMu.Unlock()
		if initialized && previous != rendered && changed == "" {
			changed = entry.name
		}
	}
	return changed
}

// checkpoint is called from evalStmt for every statement. It decides
// whether to pause the calling goroutine and, if so, blocks it until the
// host resumes this pause's token or the active execution ends.
func (dc *DebugController) checkpoint(vm *Interpreter, s ast.Stmt, env *Env) error {
	if dc.detached.Load() {
		return nil
	}

	var frame *callFrame
	if env != nil {
		frame = env.frame
	}

	reason := ""
	watchName := ""
	switch {
	case dc.pauseRequested.CompareAndSwap(true, false):
		reason = "pause"
	default:
		loc := vm.traceLocation(s.Pos())
		if bp, ok := dc.breakpointAt(loc.Line); ok {
			if bp.cond == nil {
				reason = "breakpoint"
			} else if v, err := vm.evalExpr(bp.cond, env); err == nil && ToBool(v) {
				reason = "breakpoint"
			}
		}
		if reason == "" {
			switch StepMode(dc.mode.Load()) {
			case DebugStepInto:
				if sameLineage(frame, dc.baseFrame.Load()) {
					reason = "step-into"
				}
			case DebugStepOver:
				if frameChainContains(dc.baseFrame.Load(), frame) {
					reason = "step-over"
				}
			case DebugStepOut:
				base := dc.baseFrame.Load()
				if frame != base && frameChainContains(base, frame) {
					reason = "step-out"
				}
			}
		}
	}
	if reason == "" {
		watchName = dc.changedWatch(vm, env)
		if watchName != "" {
			reason = "watchpoint"
		}
	}
	if reason == "" {
		return nil
	}

	loc := vm.traceLocation(s.Pos())
	function := "program"
	if frame != nil && frame.funcName != "" {
		function = frame.funcName
	}
	depth := 0
	if frame != nil {
		depth = frame.depth
	}
	info := DebugPauseInfo{
		Location: loc,
		Function: function,
		Depth:    depth,
		Stack:    callStackString(frame),
		Vars:     vm.collectPauseVars(env, function),
		Reason:   reason,
		Watch:    watchName,
	}

	token := PauseToken(dc.nextToken.Add(1))
	info.Token = token
	p := &pendingPause{ch: make(chan StepMode, 1), vm: vm, env: env}
	dc.pendingMu.Lock()
	dc.pending[token] = p
	dc.pendingMu.Unlock()

	dc.onPauseMu.RLock()
	onPause := dc.onPause
	dc.onPauseMu.RUnlock()
	if onPause != nil {
		onPause(info)
	}

	select {
	case mode := <-p.ch:
		dc.mode.Store(int32(mode))
		if mode == DebugRun {
			dc.baseFrame.Store(nil)
		} else {
			dc.baseFrame.Store(frame)
		}
		return nil
	case <-vm.Context().Done():
		dc.pendingMu.Lock()
		delete(dc.pending, token)
		dc.pendingMu.Unlock()
		return vm.Context().Err()
	}
}

// collectPauseVars snapshots every local binding visible from env (see
// collectLocalVars) as sorted VariableSnapshots, formatted the same
// capability-safe way as debug.Q/debug.Vars.
func (vm *Interpreter) collectPauseVars(env *Env, function string) []VariableSnapshot {
	vars := vm.collectLocalVars(env)
	if len(vars) == 0 {
		return nil
	}
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]VariableSnapshot, 0, len(names))
	for _, name := range names {
		v := vars[name]
		out = append(out, VariableSnapshot{
			Name: name, Value: debugValue(v), Type: fmt.Sprintf("%T", v), Function: function,
		})
	}
	return out
}
