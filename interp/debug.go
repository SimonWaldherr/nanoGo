package interp

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	runtimeTrace "runtime/trace"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// TraceEvent is one compact, time-ordered record of interpreter activity.
// The tracer is local-only: it never writes files or opens network connections.
// Hosts may forward Events to their own logging or visualisation system.
type TraceEvent struct {
	Sequence uint64
	At       time.Time
	Kind     string
	Function string
	Message  string
	Location SourceLocation

	// Assertion is set only for testing.T Errorf/Fatalf events (see
	// interp/testing_pkg.go). It carries the call's raw pieces separately
	// from Message, so a host-side sanitizer can drop Args while keeping
	// Format/Fatal/Location.
	Assertion *AssertionEvent
}

// SourceLocation identifies guest source when an event has an AST origin.
type SourceLocation struct {
	File   string
	Line   int
	Column int
}

// AssertionEvent carries the raw pieces of one testing.T.Errorf/Fatalf call.
type AssertionEvent struct {
	Format string
	Args   []any
	Fatal  bool
}

// Tracer is a bounded, concurrency-safe event timeline. Its ring buffer avoids
// turning debugging itself into an unbounded memory allocation.
type Tracer struct {
	mu       sync.RWMutex
	events   []TraceEvent
	capacity int
	next     int
	wrapped  bool
	sequence atomic.Uint64
}

// breakpointSet is immutable after publication, so evaluator goroutines can
// read it without taking a lock while a host updates breakpoints between runs.
type breakpointSet struct {
	lines map[int]struct{}
}

// SetBreakpoints installs source-line breakpoints for subsequent executions.
// A breakpoint is a lightweight trace event when a tracer is enabled; it does
// not pause or mutate guest execution. This keeps the core evaluator
// deterministic and lets browser/host frontends choose replay or stepping
// semantics without blocking an evaluator goroutine.
func (vm *Interpreter) SetBreakpoints(lines []int) {
	set := &breakpointSet{lines: make(map[int]struct{}, len(lines))}
	for _, line := range lines {
		if line > 0 {
			set.lines[line] = struct{}{}
		}
	}
	// Keep the hot evaluator path at a single nil atomic load when the host
	// clears its breakpoint list (the normal case for an ordinary run).
	if len(set.lines) == 0 {
		vm.breakpoints.Store(nil)
		vm.refreshStmtHooks()
		return
	}
	vm.breakpoints.Store(set)
	vm.refreshStmtHooks()
}

// Breakpoints returns a sorted snapshot of configured source lines.
func (vm *Interpreter) Breakpoints() []int {
	set := vm.breakpoints.Load()
	if set == nil || len(set.lines) == 0 {
		return nil
	}
	lines := make([]int, 0, len(set.lines))
	for line := range set.lines {
		lines = append(lines, line)
	}
	sort.Ints(lines)
	return lines
}

// NewTracer creates a local trace recorder. A non-positive capacity selects a
// compact default of 1,024 events.
func NewTracer(capacity int) *Tracer {
	if capacity <= 0 {
		capacity = 1024
	}
	return &Tracer{capacity: capacity, events: make([]TraceEvent, 0, capacity)}
}

// Events returns a chronological copy of retained events, oldest first.
func (t *Tracer) Events() []TraceEvent {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	if !t.wrapped {
		return append([]TraceEvent(nil), t.events...)
	}
	out := make([]TraceEvent, 0, len(t.events))
	out = append(out, t.events[t.next:]...)
	out = append(out, t.events[:t.next]...)
	return out
}

// Reset discards retained events while keeping the tracer configuration.
func (t *Tracer) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.events = t.events[:0]
	t.next = 0
	t.wrapped = false
	t.mu.Unlock()
}

func (t *Tracer) record(event TraceEvent) {
	if t == nil {
		return
	}
	event.Sequence = t.sequence.Add(1)
	event.At = time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.events) < t.capacity {
		t.events = append(t.events, event)
		return
	}
	t.events[t.next] = event
	t.next = (t.next + 1) % t.capacity
	t.wrapped = true
}

// SetTracer installs (or removes with nil) the active local trace recorder.
// It is safe to call between runs. A tracer may be shared with host code that
// concurrently reads Events.
func (vm *Interpreter) SetTracer(tracer *Tracer) {
	vm.tracer.Store(tracer)
}

// Tracer returns the currently configured tracer, or nil when tracing is off.
func (vm *Interpreter) Tracer() *Tracer {
	return vm.tracer.Load()
}

// SetRuntimeTraceAnnotations controls whether nanoGo's high-level events are
// also written as user annotations into the host Go runtime trace. It is off
// by default, so ordinary interpreter runs retain their fast tracing-free
// path. When enabled, a host must independently activate runtime/trace (or
// its FlightRecorder); the browser inspector continues to use Tracer.
//
// This mirrors semantic guest events such as calls, guest goroutines, debug
// marks, and denied capabilities. Go runtime scheduler, GC, and host
// goroutine events are already collected by runtime/trace itself.
func (vm *Interpreter) SetRuntimeTraceAnnotations(enabled bool) {
	vm.runtimeTraceAnnotations.Store(enabled)
}

func (vm *Interpreter) emitRuntimeTraceAnnotation(kind, function, message string) {
	if !vm.runtimeTraceAnnotations.Load() || !runtimeTrace.IsEnabled() {
		return
	}
	if function != "" {
		if message != "" {
			message = function + ": " + message
		} else {
			message = function
		}
	}
	runtimeTrace.Log(vm.Context(), "nanogo."+kind, message)
}

// emitAssertionTrace records a testing.T.Errorf/Fatalf call. Unlike
// emitTrace, it has no AST node to resolve a Location from — Errorf/Fatalf
// are plain natives, not specially intercepted in evalExpr the way
// debug.Q/debug.Mark are — so Location stays zero-value, matching every
// other native call's call_start/call_end trace events today.
func (vm *Interpreter) emitAssertionTrace(function string, assertion AssertionEvent) {
	vm.emitRuntimeTraceAnnotation("test_assertion", function, assertion.Format)
	tracer := vm.tracer.Load()
	if tracer == nil {
		return
	}
	kind := "test_error"
	if assertion.Fatal {
		kind = "test_fatal"
	}
	tracer.record(TraceEvent{Kind: kind, Function: function, Message: assertion.Format, Assertion: &assertion})
}

func (vm *Interpreter) emitTrace(kind, function, message string, node ast.Node) {
	vm.emitRuntimeTraceAnnotation(kind, function, message)
	tracer := vm.tracer.Load()
	if tracer == nil {
		return
	}
	event := TraceEvent{Kind: kind, Function: function, Message: message}
	if node != nil {
		event.Location = vm.traceLocation(node.Pos())
	}
	tracer.record(event)
}

func (vm *Interpreter) traceLocation(pos token.Pos) SourceLocation {
	exec := vm.execution.Load()
	if exec == nil || exec.fset == nil || !pos.IsValid() {
		return SourceLocation{}
	}
	p := exec.fset.Position(pos)
	return SourceLocation{File: p.Filename, Line: p.Line, Column: p.Column}
}

// attachRuntimeErrorLocation tags err with loc the first time it passes
// through a location-aware boundary (evalExpr/evalStmt): a no-op if err
// isn't a *RuntimeError, if loc itself couldn't be resolved (Line == 0 —
// e.g. no active execution, as in package-level var-initializer evaluation
// during a loader build), or if err was already tagged deeper in the call
// stack. See evalExpr's doc comment for why first-write-wins matters here.
func attachRuntimeErrorLocation(err error, loc SourceLocation) {
	if loc.Line == 0 {
		return
	}
	if re, ok := err.(*RuntimeError); ok && re.Loc.Line == 0 {
		re.Loc = loc
	}
}

// debugExpression returns canonical source for a probe expression. Identifiers
// and literals make up the overwhelming majority of debug.Q calls, so avoid
// go/format's buffer and file-set work for those simple forms.
func debugExpression(expr ast.Expr, fset *token.FileSet) string {
	if expr == nil {
		return ""
	}
	switch x := expr.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.BasicLit:
		return x.Value
	}
	var buf bytes.Buffer
	if fset == nil {
		fset = token.NewFileSet()
	}
	if err := format.Node(&buf, fset, expr); err != nil {
		return fmt.Sprintf("%T", expr)
	}
	return buf.String()
}

func debugValue(value any) string {
	// Reuse the bridge's capability-safe conversion: it drops private runtime
	// fields and refuses to stringify functions, channels, and native pointers.
	if converted, err := bridgeToHost(value); err == nil {
		return fmt.Sprintf("%#v", converted)
	}
	return fmt.Sprintf("<%T>", value)
}

func (vm *Interpreter) traceDebugQ(call *ast.CallExpr, env *Env) (any, error) {
	// A probe must always evaluate its arguments, even without an observer:
	// they are ordinary Go expressions and may have side effects. Formatting
	// their source and values, however, is pure diagnostic work. Skip it when
	// neither local tracing nor a live runtime trace can receive the event.
	capture := vm.tracer.Load() != nil ||
		(vm.runtimeTraceAnnotations.Load() && runtimeTrace.IsEnabled())
	if !capture {
		for _, arg := range call.Args {
			if _, err := vm.evalExpr(arg, env); err != nil {
				return nil, err
			}
		}
		return nil, nil
	}

	var fset *token.FileSet
	if exec := vm.activeExecution; exec != nil {
		fset = exec.fset
	}
	var message strings.Builder
	for _, arg := range call.Args {
		value, err := vm.evalExpr(arg, env)
		if err != nil {
			return nil, err
		}
		if message.Len() > 0 {
			message.WriteString(", ")
		}
		message.WriteString(debugExpression(arg, fset))
		message.WriteString(" = ")
		message.WriteString(debugValue(value))
	}
	if message.Len() == 0 {
		message.WriteString("<no values>")
	}
	vm.emitTrace("debug_q", "debug.Q", message.String(), call)
	return nil, nil
}

func (vm *Interpreter) traceDebugMark(call *ast.CallExpr, env *Env) (any, error) {
	if len(call.Args) != 1 {
		return nil, NewRuntimeError("debug.Mark: expected one label")
	}
	value, err := vm.evalExpr(call.Args[0], env)
	if err != nil {
		return nil, err
	}
	vm.emitTrace("debug_mark", "debug.Mark", ToString(value), call)
	return nil, nil
}

// traceDebugStack backs debug.Stack(), returning the current guest call
// stack (innermost call first) as a newline-joined string. Like Q and Mark
// it is intercepted directly in evalExpr rather than registered as a plain
// native, because it needs env.frame — a native's args-only signature has
// no way to see the caller's frame.
func (vm *Interpreter) traceDebugStack(call *ast.CallExpr, env *Env) (any, error) {
	if len(call.Args) != 0 {
		return nil, NewRuntimeError("debug.Stack: expected no arguments")
	}
	s := callStackString(env.frame)
	vm.emitTrace("debug_stack", "debug.Stack", s, call)
	return s, nil
}

func callStackString(frame *callFrame) string {
	if frame == nil {
		return "<no active call>"
	}
	var buf strings.Builder
	for depth, f := 0, frame; f != nil; depth, f = depth+1, f.caller {
		fmt.Fprintf(&buf, "#%d %s\n", depth, f.funcName)
	}
	return strings.TrimRight(buf.String(), "\n")
}

// traceDebugVars backs debug.Vars(), returning a snapshot of every local
// binding visible at the call site (innermost scope wins on shadowing),
// formatted as sorted "name = value" lines. Also intercepted directly in
// evalExpr for the same reason as Stack: it needs env, not just args.
func (vm *Interpreter) traceDebugVars(call *ast.CallExpr, env *Env) (any, error) {
	if len(call.Args) != 0 {
		return nil, NewRuntimeError("debug.Vars: expected no arguments")
	}
	s := vm.envVarsString(env)
	vm.emitTrace("debug_vars", "debug.Vars", s, call)
	return s, nil
}

func (vm *Interpreter) envVarsString(env *Env) string {
	vars := vm.collectLocalVars(env)
	if len(vars) == 0 {
		return "<no local variables>"
	}
	names := make([]string, 0, len(vars))
	for name := range vars {
		names = append(names, name)
	}
	sort.Strings(names)
	var buf strings.Builder
	for _, name := range names {
		fmt.Fprintf(&buf, "%s = %s\n", name, debugValue(vars[name]))
	}
	return strings.TrimRight(buf.String(), "\n")
}
