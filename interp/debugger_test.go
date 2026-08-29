// interp/debugger_test.go
package interp

import (
	"context"
	"strings"
	"testing"
	"time"
)

// runDebugged starts src on its own goroutine with dc attached, forwarding
// every pause to the returned channel. The caller drives the session by
// reading from pauses and calling Continue/StepInto/StepOver/StepOut/Kill;
// done receives the final Run error exactly once.
func runDebugged(t *testing.T, dc *DebugController, src string) (pauses chan DebugPauseInfo, done chan error, vm *Interpreter) {
	t.Helper()
	vm, _ = newTestVM()
	vm.SetDebugController(dc)
	pauses = make(chan DebugPauseInfo, 64)
	dc.OnPause(func(info DebugPauseInfo) { pauses <- info })
	done = make(chan error, 1)
	go func() { done <- vm.RunContext(context.Background(), src) }()
	return pauses, done, vm
}

func mustPause(t *testing.T, pauses chan DebugPauseInfo) DebugPauseInfo {
	t.Helper()
	select {
	case info := <-pauses:
		return info
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a pause")
		return DebugPauseInfo{}
	}
}

func mustFinish(t *testing.T, done chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to finish")
		return nil
	}
}

func varValue(vars []VariableSnapshot, name string) (string, bool) {
	for _, v := range vars {
		if v.Name == name {
			return v.Value, true
		}
	}
	return "", false
}

func TestDebugBreakpointPausesAndContinues(t *testing.T) {
	dc := NewDebugController()
	dc.SetBreakpoints([]int{6})
	pauses, done, _ := runDebugged(t, dc, `
package main
import "fmt"
func main() {
	for i := 0; i < 3; i++ {
		fmt.Println(i)
	}
	fmt.Println("done")
}
`)
	for i := 0; i < 3; i++ {
		info := mustPause(t, pauses)
		if info.Reason != "breakpoint" {
			t.Fatalf("pause %d: reason = %q, want breakpoint", i, info.Reason)
		}
		if info.Location.Line != 6 {
			t.Fatalf("pause %d: line = %d, want 6", i, info.Location.Line)
		}
		if !dc.Continue(info.Token) {
			t.Fatalf("pause %d: Continue(%d) reported not pending", i, info.Token)
		}
	}
	if err := mustFinish(t, done); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

func TestDebugConditionalBreakpointOnlyPausesWhenTrue(t *testing.T) {
	dc := NewDebugController()
	if err := dc.SetConditionalBreakpoint(6, "i == 2"); err != nil {
		t.Fatalf("SetConditionalBreakpoint: %v", err)
	}
	pauses, done, _ := runDebugged(t, dc, `
package main
import "fmt"
func main() {
	for i := 0; i < 5; i++ {
		fmt.Println(i)
	}
}
`)
	info := mustPause(t, pauses)
	if info.Reason != "breakpoint" {
		t.Fatalf("reason = %q, want breakpoint", info.Reason)
	}
	if v, ok := varValue(info.Vars, "i"); !ok || v != "2" {
		t.Fatalf("i = %q (ok=%v), want 2", v, ok)
	}
	if !dc.Continue(info.Token) {
		t.Fatal("Continue reported not pending")
	}
	select {
	case p := <-pauses:
		t.Fatalf("unexpected second pause: %+v", p)
	case err := <-done:
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to finish")
	}
}

func TestDebugHitBreakpointPausesOnceAtTarget(t *testing.T) {
	dc := NewDebugController()
	if err := dc.SetHitBreakpoint(6, 2, "i%2 == 0"); err != nil {
		t.Fatalf("SetHitBreakpoint: %v", err)
	}
	details := dc.BreakpointDetails()[6]
	if details.HitTarget != 2 || !details.Temporary || details.Condition != "i%2 == 0" {
		t.Fatalf("BreakpointDetails = %+v, want temporary two-hit condition", details)
	}
	pauses, done, _ := runDebugged(t, dc, `
package main
import "fmt"
func main() {
	for i := 0; i < 5; i++ {
		fmt.Println(i)
	}
}
`)
	info := mustPause(t, pauses)
	if info.Reason != "breakpoint" {
		t.Fatalf("reason = %q, want breakpoint", info.Reason)
	}
	if v, ok := varValue(info.Vars, "i"); !ok || v != "2" {
		t.Fatalf("i = %q (ok=%v), want 2", v, ok)
	}
	if len(dc.BreakpointDetails()) != 0 {
		t.Fatal("hit breakpoint was not removed after pausing")
	}
	if !dc.Continue(info.Token) {
		t.Fatal("Continue reported not pending")
	}
	select {
	case extra := <-pauses:
		t.Fatalf("unexpected extra pause: %+v", extra)
	case err := <-done:
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to finish")
	}
}

func TestDebugTemporaryBreakpointRemovesItself(t *testing.T) {
	dc := NewDebugController()
	if err := dc.SetTemporaryBreakpoint(6, ""); err != nil {
		t.Fatalf("SetTemporaryBreakpoint: %v", err)
	}
	pauses, done, _ := runDebugged(t, dc, `
package main
import "fmt"
func main() {
	for i := 0; i < 3; i++ {
		fmt.Println(i)
	}
}
`)
	info := mustPause(t, pauses)
	if info.Reason != "breakpoint" {
		t.Fatalf("reason = %q, want breakpoint", info.Reason)
	}
	if len(dc.BreakpointDetails()) != 0 {
		t.Fatal("temporary breakpoint was not removed after first pause")
	}
	if !dc.Continue(info.Token) {
		t.Fatal("Continue reported not pending")
	}
	if err := mustFinish(t, done); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

func TestDebugStepOverSkipsNestedCall(t *testing.T) {
	dc := NewDebugController()
	dc.Pause()
	pauses, done, _ := runDebugged(t, dc, `
package main
import "fmt"
func helper() {
	fmt.Println("in helper")
}
func main() {
	fmt.Println("before")
	helper()
	fmt.Println("after")
}
`)
	first := mustPause(t, pauses)
	if first.Function != "main" {
		t.Fatalf("first pause function = %q, want main", first.Function)
	}
	// First step-over just advances one statement, to the `helper()` call
	// line itself (still in main, not yet inside it).
	if !dc.StepOver(first.Token) {
		t.Fatal("StepOver reported not pending")
	}
	atCall := mustPause(t, pauses)
	if atCall.Function != "main" {
		t.Fatalf("pause at the call line: function = %q, want main", atCall.Function)
	}
	if atCall.Reason != "step-over" {
		t.Fatalf("reason = %q, want step-over", atCall.Reason)
	}
	// Stepping over THIS statement must not stop inside helper's body.
	if !dc.StepOver(atCall.Token) {
		t.Fatal("StepOver reported not pending")
	}
	next := mustPause(t, pauses)
	if next.Function != "main" {
		t.Fatalf("after step-over, function = %q, want main (helper's body should be skipped)", next.Function)
	}
	if next.Reason != "step-over" {
		t.Fatalf("reason = %q, want step-over", next.Reason)
	}
	if !dc.Continue(next.Token) {
		t.Fatal("Continue reported not pending")
	}
	if err := mustFinish(t, done); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

func TestDebugStepIntoEntersNestedCall(t *testing.T) {
	dc := NewDebugController()
	dc.SetBreakpoints([]int{9}) // the `helper()` call line
	pauses, done, _ := runDebugged(t, dc, `
package main
import "fmt"
func helper() {
	fmt.Println("in helper")
}
func main() {
	fmt.Println("before")
	helper()
	fmt.Println("after")
}
`)
	first := mustPause(t, pauses)
	if first.Function != "main" {
		t.Fatalf("first pause function = %q, want main", first.Function)
	}
	if !dc.StepInto(first.Token) {
		t.Fatal("StepInto reported not pending")
	}
	next := mustPause(t, pauses)
	if next.Function != "helper" {
		t.Fatalf("after step-into, function = %q, want helper", next.Function)
	}
	if !dc.Continue(next.Token) {
		t.Fatal("Continue reported not pending")
	}
	if err := mustFinish(t, done); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

func TestDebugStepOutReturnsToCaller(t *testing.T) {
	dc := NewDebugController()
	dc.SetBreakpoints([]int{5})
	pauses, done, _ := runDebugged(t, dc, `
package main
import "fmt"
func helper() {
	fmt.Println("in helper")
}
func main() {
	helper()
	fmt.Println("after")
}
`)
	inside := mustPause(t, pauses)
	if inside.Function != "helper" {
		t.Fatalf("function = %q, want helper", inside.Function)
	}
	if !dc.StepOut(inside.Token) {
		t.Fatal("StepOut reported not pending")
	}
	back := mustPause(t, pauses)
	if back.Function != "main" {
		t.Fatalf("after step-out, function = %q, want main", back.Function)
	}
	if back.Reason != "step-out" {
		t.Fatalf("reason = %q, want step-out", back.Reason)
	}
	if !dc.Continue(back.Token) {
		t.Fatal("Continue reported not pending")
	}
	if err := mustFinish(t, done); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

func TestDebugSetVariableChangesObservedValue(t *testing.T) {
	dc := NewDebugController()
	dc.SetBreakpoints([]int{6})
	pauses, done, _ := runDebugged(t, dc, `
package main
import "fmt"
func main() {
	for i := 0; i < 3; i++ {
		fmt.Println(i)
	}
	fmt.Println("done")
}
`)
	first := mustPause(t, pauses)
	if v, ok := varValue(first.Vars, "i"); !ok || v != "0" {
		t.Fatalf("i = %q (ok=%v), want 0", v, ok)
	}
	display, err := dc.SetVariable(first.Token, "i", "9")
	if err != nil {
		t.Fatalf("SetVariable: %v", err)
	}
	if display != "9" {
		t.Fatalf("SetVariable returned %q, want 9", display)
	}
	if !dc.Continue(first.Token) {
		t.Fatal("Continue reported not pending")
	}
	// The loop's post statement (i++) now runs against the edited value, so
	// the very next breakpoint hit — if any — should observe i == 10, and
	// the loop condition (i < 3) should end the loop instead of looping
	// twice more.
	select {
	case p := <-pauses:
		t.Fatalf("unexpected extra pause after editing the loop counter past its bound: %+v", p)
	case err := <-done:
		if err != nil {
			t.Fatalf("Run failed: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to finish")
	}
}

func TestDebugSetVariableRejectsUnknownName(t *testing.T) {
	dc := NewDebugController()
	dc.SetBreakpoints([]int{5})
	pauses, done, _ := runDebugged(t, dc, `
package main
import "fmt"
func main() {
	i := 0
	fmt.Println(i)
}
`)
	first := mustPause(t, pauses)
	if _, err := dc.SetVariable(first.Token, "doesNotExist", "1"); err == nil {
		t.Fatal("expected an error for an undefined variable")
	}
	if !dc.Continue(first.Token) {
		t.Fatal("Continue reported not pending")
	}
	if err := mustFinish(t, done); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

func TestDebugWatchpointAndEvaluate(t *testing.T) {
	dc := NewDebugController()
	if err := dc.SetWatch("counter", "x"); err != nil {
		t.Fatalf("SetWatch: %v", err)
	}
	if got := dc.Watches()["counter"]; got != "x" {
		t.Fatalf("Watches()[counter] = %q, want x", got)
	}
	pauses, done, _ := runDebugged(t, dc, `
package main
import "fmt"
func main() {
	x := 0
	x++
	fmt.Println(x)
	x++
	fmt.Println(x)
}
`)
	first := mustPause(t, pauses)
	if first.Reason != "watchpoint" || first.Watch != "counter" {
		t.Fatalf("first pause = %+v, want counter watchpoint", first)
	}
	if value, err := dc.Evaluate(first.Token, "x * 2"); err != nil || value != "2" {
		t.Fatalf("Evaluate = %q, %v; want 2", value, err)
	}
	if !dc.Continue(first.Token) {
		t.Fatal("Continue(first) reported not pending")
	}
	second := mustPause(t, pauses)
	if second.Reason != "watchpoint" || second.Watch != "counter" {
		t.Fatalf("second pause = %+v, want counter watchpoint", second)
	}
	if v, ok := varValue(second.Vars, "x"); !ok || v != "2" {
		t.Fatalf("second x = %q (ok=%v), want 2", v, ok)
	}
	dc.ClearWatch("counter")
	if len(dc.Watches()) != 0 {
		t.Fatal("ClearWatch left a configured watch")
	}
	if !dc.Continue(second.Token) {
		t.Fatal("Continue(second) reported not pending")
	}
	if err := mustFinish(t, done); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

func TestDebugLogpointReportsValueWithoutPausing(t *testing.T) {
	dc := NewDebugController()
	if err := dc.SetLogpoint(6, "x * 10"); err != nil {
		t.Fatalf("SetLogpoint: %v", err)
	}
	if got := dc.Logpoints()[6]; got != "x * 10" {
		t.Fatalf("Logpoints()[6] = %q, want x * 10", got)
	}
	logs := make(chan DebugLogInfo, 1)
	dc.OnLog(func(info DebugLogInfo) { logs <- info })
	vm, _ := newTestVM()
	vm.SetDebugController(dc)
	if err := vm.Run(`package main
import "fmt"
func main() {
	x := 1
	x++
	fmt.Println(x)
}`); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	select {
	case info := <-logs:
		if info.Location.Line != 6 || info.Expression != "x * 10" || info.Value != "20" {
			t.Fatalf("log = %+v, want line 6 expression x * 10 value 20", info)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for logpoint")
	}
	dc.ClearLogpoint(6)
	if len(dc.Logpoints()) != 0 {
		t.Fatal("ClearLogpoint left a configured logpoint")
	}
}

func TestDebugPausedReturnsSnapshot(t *testing.T) {
	dc := NewDebugController()
	dc.SetBreakpoints([]int{6})
	pauses, done, _ := runDebugged(t, dc, `
package main
import "fmt"
func main() {
	i := 7
	fmt.Println(i)
}
`)
	info := mustPause(t, pauses)
	pending := dc.Paused()
	if len(pending) != 1 || pending[0].Token != info.Token {
		t.Fatalf("Paused() = %+v, want token %d", pending, info.Token)
	}
	if v, ok := varValue(pending[0].Vars, "i"); !ok || v != "7" {
		t.Fatalf("Paused i = %q (ok=%v), want 7", v, ok)
	}
	if !dc.Continue(info.Token) {
		t.Fatal("Continue reported not pending")
	}
	if got := dc.Paused(); len(got) != 0 {
		t.Fatalf("Paused after Continue = %+v, want none", got)
	}
	if err := mustFinish(t, done); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

func TestDebugDetachResumesAndStopsPausing(t *testing.T) {
	dc := NewDebugController()
	dc.SetBreakpoints([]int{6})
	pauses, done, _ := runDebugged(t, dc, `
package main
import "fmt"
func main() {
	for i := 0; i < 5; i++ {
		fmt.Println(i)
	}
}
`)
	mustPause(t, pauses)
	dc.Detach()
	if err := mustFinish(t, done); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
}

func TestDebugPauseUnblocksOnKill(t *testing.T) {
	dc := NewDebugController()
	dc.SetBreakpoints([]int{5})
	pauses, done, vm := runDebugged(t, dc, `
package main
import "fmt"
func main() {
	for {
		fmt.Println("tick")
	}
}
`)
	mustPause(t, pauses)
	if !vm.Kill() {
		t.Fatal("Kill reported no active execution")
	}
	err := mustFinish(t, done)
	if err == nil || !strings.Contains(err.Error(), "kill") {
		t.Fatalf("Run error = %v, want a kill error", err)
	}
}
