// interp/control_flow_test.go
package interp

import (
	"strings"
	"testing"
)

func TestGotoForward(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	goto skip
	fmt.Println("should not print")
skip:
	fmt.Println("after skip")
}
`)
	if strings.Contains(out, "should not print") {
		t.Errorf("goto did not skip the statement: %q", out)
	}
	if !strings.Contains(out, "after skip") {
		t.Errorf("expected 'after skip', got %q", out)
	}
}

func TestGotoBackwardLoop(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	i := 0
loop:
	if i < 3 {
		fmt.Println(i)
		i = i + 1
		goto loop
	}
	fmt.Println("done")
}
`)
	want := "0\n1\n2\ndone\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestGotoRestartAllowsRedeclaration(t *testing.T) {
	// A := between a label and the goto that jumps back to it must be
	// allowed to redeclare on each pass, exactly as it would if the
	// restarted region got a fresh scope per iteration (like a real loop
	// body does).
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	i := 0
Loop:
	x := i
	i++
	if i < 3 {
		goto Loop
	}
	fmt.Println("final x:", x)
}
`)
	want := "final x: 2\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestDanglingLabelBeforeClosingBrace(t *testing.T) {
	// `done:` with nothing after it parses as a LabeledStmt wrapping an
	// EmptyStmt — the common goto-to-exit idiom's target.
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	for i := 0; i < 3; i++ {
		if i == 1 {
			goto done
		}
		fmt.Println(i)
	}
done:
}
`)
	want := "0\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestLabeledDeclarationInsideLoopBody(t *testing.T) {
	// A label on a := inside a for-loop body (unused as a goto target here)
	// must not stop that loop body from getting a fresh scope per
	// iteration — the label itself carries no scoping meaning.
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	for i := 0; i < 3; i++ {
	Retry:
		y := i * 2
		if y < 0 {
			goto Retry
		}
		fmt.Println(y)
	}
}
`)
	want := "0\n2\n4\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestGotoOutOfNestedFor(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if i == 1 && j == 1 {
				goto done
			}
			fmt.Println(i, j)
		}
	}
done:
	fmt.Println("done")
}
`)
	if !strings.Contains(out, "done\n") {
		t.Errorf("expected loop to be exited via goto, got %q", out)
	}
	if strings.Contains(out, "1 1") {
		t.Errorf("goto should have jumped out before printing 1 1: %q", out)
	}
	if !strings.Contains(out, "1 0") {
		t.Errorf("expected '1 0' to have printed before the goto fired: %q", out)
	}
}

func TestGotoRestartsLabeledLoop(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	count := 0
retry:
	for i := 0; i < 3; i++ {
		if i == 1 && count < 2 {
			count = count + 1
			goto retry
		}
		fmt.Println(i)
	}
	fmt.Println("done", count)
}
`)
	want := "0\n0\n0\n1\n2\ndone 2\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestLabeledBreak(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
Outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if j == 1 {
				break Outer
			}
			fmt.Println(i, j)
		}
	}
	fmt.Println("done")
}
`)
	want := "0 0\ndone\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestLabeledContinue(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
Outer:
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			if j == 1 {
				continue Outer
			}
			fmt.Println(i, j)
		}
	}
}
`)
	want := "0 0\n1 0\n2 0\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestLabeledBreakSwitch(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
Loop:
	for i := 0; i < 3; i++ {
		switch i {
		case 1:
			break Loop
		default:
			fmt.Println(i)
		}
	}
	fmt.Println("done")
}
`)
	want := "0\ndone\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestRecoverStopsPanic(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func safeCall() {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("recovered:", r)
		}
	}()
	panic("boom")
}
func main() {
	safeCall()
	fmt.Println("still running")
}
`)
	want := "recovered: boom\nstill running\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestRecoverReturnsNilWithoutPanic(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	defer func() {
		r := recover()
		if r == nil {
			fmt.Println("nothing to recover")
		}
	}()
	fmt.Println("ok")
}
`)
	want := "ok\nnothing to recover\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestDoubleRecoverOnlyRecoversOnce(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	defer func() {
		first := recover()
		second := recover()
		fmt.Println(first, second)
	}()
	panic("boom")
}
`)
	want := "boom <nil>\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestRecoverPreservesNonStringPanicValue(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	defer func() {
		r := recover()
		fmt.Println(r)
	}()
	panic(42)
}
`)
	want := "42\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestRecoverOutsideDeferDoesNotStopPanic(t *testing.T) {
	vm, buf := newTestVM()
	err := vm.Run(`
package main
import "fmt"
func main() {
	fmt.Println("before")
	recover()
	panic("boom")
}
`)
	if err == nil {
		t.Fatal("expected panic to propagate past a non-deferred recover()")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected panic message 'boom', got %q", err.Error())
	}
	if !strings.Contains(buf.String(), "before") {
		t.Errorf("expected 'before' to have printed, got %q", buf.String())
	}
}

func TestDefersRunInLIFOOrderAcrossPanic(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func work() {
	defer fmt.Println("first deferred, runs last")
	defer fmt.Println("second deferred, runs first")
	panic("boom")
}
func main() {
	defer func() {
		recover()
	}()
	work()
}
`)
	want := "second deferred, runs first\nfirst deferred, runs last\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestEarlierDefersStillRunWhenALaterOnePanics(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func work() {
	defer fmt.Println("outer defer ran")
	defer func() { panic("second panic") }()
	panic("first panic")
}
func main() {
	defer func() {
		r := recover()
		fmt.Println("recovered:", r)
	}()
	work()
}
`)
	if !strings.Contains(out, "outer defer ran") {
		t.Errorf("expected the earlier-registered defer to still run despite a later deferred call panicking: %q", out)
	}
	if !strings.Contains(out, "recovered: second panic") {
		t.Errorf("expected the second (later) panic to be the one observed by recover: %q", out)
	}
}

func TestUnrecoveredPanicPropagatesThroughCallers(t *testing.T) {
	vm, buf := newTestVM()
	err := vm.Run(`
package main
import "fmt"
func inner() { panic("deep boom") }
func middle() {
	defer fmt.Println("middle defer")
	inner()
}
func main() {
	middle()
}
`)
	if err == nil {
		t.Fatal("expected unrecovered panic to propagate to Run's caller")
	}
	if !strings.Contains(err.Error(), "deep boom") {
		t.Errorf("expected panic message 'deep boom', got %q", err.Error())
	}
	if !strings.Contains(buf.String(), "middle defer") {
		t.Errorf("expected middle's defer to still run during unwinding, got %q", buf.String())
	}
}
