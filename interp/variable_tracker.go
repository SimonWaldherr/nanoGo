package interp

import (
	"fmt"
	"go/ast"
	"sort"
	"sync"
	"sync/atomic"
)

// VariableSnapshot is the last observed value of one named binding in a
// function. Values are rendered through the same capability-safe bridge as
// debug.Q/debug.Vars; functions, channels, and private runtime fields are not
// exposed to hosts.
type VariableSnapshot struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Type     string `json:"type"`
	Function string `json:"function"`
	File     string `json:"file,omitempty"`
	Line     int    `json:"line,omitempty"`
	Column   int    `json:"column,omitempty"`
	Writes   uint64 `json:"writes"`
	Sequence uint64 `json:"sequence"`
}

// VariableTracker retains only the latest value per function/name pair, so a
// long-running loop does not grow memory with every assignment. It is safe for
// guest goroutines to update concurrently.
type VariableTracker struct {
	mu       sync.RWMutex
	values   map[string]VariableSnapshot
	sequence atomic.Uint64
}

func NewVariableTracker() *VariableTracker {
	return &VariableTracker{values: make(map[string]VariableSnapshot)}
}

// SetVariableTracker enables or disables last-value collection.
func (vm *Interpreter) SetVariableTracker(tracker *VariableTracker) {
	vm.variableTracker.Store(tracker)
}

// VariableTracker returns the currently configured tracker, if any.
func (vm *Interpreter) VariableTracker() *VariableTracker {
	return vm.variableTracker.Load()
}

// Snapshots returns newest-first copies of the retained values.
func (t *VariableTracker) Snapshots() []VariableSnapshot {
	if t == nil {
		return nil
	}
	t.mu.RLock()
	out := make([]VariableSnapshot, 0, len(t.values))
	for _, snapshot := range t.values {
		out = append(out, snapshot)
	}
	t.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Sequence > out[j].Sequence })
	return out
}

func (t *VariableTracker) record(snapshot VariableSnapshot) {
	key := snapshot.Function + "\x00" + snapshot.Name
	snapshot.Sequence = t.sequence.Add(1)
	t.mu.Lock()
	if previous, ok := t.values[key]; ok {
		snapshot.Writes = previous.Writes + 1
	} else {
		snapshot.Writes = 1
	}
	t.values[key] = snapshot
	t.mu.Unlock()
}

// trackingVariables reports whether a VariableTracker is attached. Call
// sites guard every recordVariable/recordAssignedExpression call with this
// check rather than relying on those functions' own nil check: Go boxes a
// concrete value (e.g. a loop counter int) into the `value any` parameter at
// the call site itself, before the callee runs, so skipping the call
// entirely is the only way to avoid that heap allocation on the hot
// assignment/increment/loop-variable/function-call-argument paths when no
// tracker is configured (the default).
func (vm *Interpreter) trackingVariables() bool {
	if exec := vm.activeExecution; exec != nil {
		return exec.trackVariables
	}
	return vm.variableTracker.Load() != nil
}

func (vm *Interpreter) recordVariable(name string, value any, node ast.Node, env *Env) {
	tracker := vm.variableTracker.Load()
	if tracker == nil || name == "" || name == "_" {
		return
	}
	function := "program"
	if env != nil && env.frame != nil && env.frame.funcName != "" {
		function = env.frame.funcName
	}
	loc := SourceLocation{}
	if node != nil {
		loc = vm.traceLocation(node.Pos())
	}
	tracker.record(VariableSnapshot{
		Name: name, Value: debugValue(value), Type: fmt.Sprintf("%T", value),
		Function: function, File: loc.File, Line: loc.Line, Column: loc.Column,
	})
}

// recordAssignedExpression captures named selector fields as well as plain
// identifiers. Index assignments have no standalone symbol name, so the
// surrounding slice/map binding continues to be represented by its own last
// direct assignment snapshot.
func (vm *Interpreter) recordAssignedExpression(expr ast.Expr, value any, env *Env) {
	switch target := expr.(type) {
	case *ast.Ident:
		vm.recordVariable(target.Name, value, target, env)
	case *ast.SelectorExpr:
		vm.recordVariable(target.Sel.Name, value, target.Sel, env)
	}
}
