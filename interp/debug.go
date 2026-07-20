package interp

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
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
}

// SourceLocation identifies guest source when an event has an AST origin.
type SourceLocation struct {
	File   string
	Line   int
	Column int
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

func (vm *Interpreter) emitTrace(kind, function, message string, node ast.Node) {
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

func debugExpression(expr ast.Expr) string {
	if expr == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := format.Node(&buf, token.NewFileSet(), expr); err != nil {
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
	parts := make([]string, 0, len(call.Args))
	for _, arg := range call.Args {
		value, err := vm.evalExpr(arg, env)
		if err != nil {
			return nil, err
		}
		parts = append(parts, debugExpression(arg)+" = "+debugValue(value))
	}
	vm.emitTrace("debug_q", "debug.Q", joinDebugParts(parts), call)
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

func joinDebugParts(parts []string) string {
	if len(parts) == 0 {
		return "<no values>"
	}
	result := parts[0]
	for _, part := range parts[1:] {
		result += ", " + part
	}
	return result
}
