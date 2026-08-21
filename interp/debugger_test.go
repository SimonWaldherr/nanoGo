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
