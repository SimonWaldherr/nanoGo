// interp/runtime_panic_test.go
package interp

import (
	"strings"
	"testing"
)

// These lock in that nanoGo's own runtime faults (the same conditions real
// Go raises as a recoverable runtime.Error panic) flow through the same
// *panicError channel as a guest panic() call, so recover() can catch them
// — unlike a structural error (undefined identifier, wrong argument count,
// an operation Go's type checker would reject), which stays a plain,
// non-recoverable error.

func TestRecoverCatchesIntegerDivideByZero(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func safe() (result int) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("recovered:", r)
			result = -1
		}
	}()
	a, b := 10, 0
	return a / b
}
func main() { fmt.Println(safe()) }
`)
	want := "recovered: runtime error: integer divide by zero\n-1\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestRecoverCatchesIntegerModByZero(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func safe() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = true
		}
	}()
	a, b := 10, 0
	_ = a % b
	return false
}
func main() { fmt.Println(safe()) }
`)
	want := "true\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestRecoverCatchesIndexOutOfRange(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func safe() (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("recovered:", r)
			ok = true
		}
	}()
	s := []int{1, 2, 3}
	_ = s[10]
	return false
}
func main() { fmt.Println(safe()) }
`)
	want := "recovered: runtime error: index out of range [10] with length 3\ntrue\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestRecoverCatchesIndexAssignmentOutOfRange(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func safe() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = true
		}
	}()
	s := []int{1, 2, 3}
	s[10] = 5
	return false
}
func main() { fmt.Println(safe()) }
`)
	want := "true\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestRecoverCatchesStringIndexOutOfRange(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func safe() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = true
		}
	}()
	s := "abc"
	_ = s[10]
	return false
}
func main() { fmt.Println(safe()) }
`)
	want := "true\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestRecoverCatchesSliceBoundsOutOfRange(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func safe() (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("recovered:", r)
			ok = true
		}
	}()
	s := []int{1, 2, 3}
	_ = s[2:1]
	return false
}
func main() { fmt.Println(safe()) }
`)
	want := "recovered: runtime error: slice bounds out of range [2:1]\ntrue\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestRecoverCatchesSendOnClosedChannel(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func safe() (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("recovered:", r)
			ok = true
		}
	}()
	ch := make(chan int)
	close(ch)
	ch <- 1
	return false
}
func main() { fmt.Println(safe()) }
`)
	want := "recovered: send on closed channel\ntrue\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestRecoverCatchesCloseOfClosedChannel(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func safe() (ok bool) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Println("recovered:", r)
			ok = true
		}
	}()
	ch := make(chan int)
	close(ch)
	close(ch)
	return false
}
func main() { fmt.Println(safe()) }
`)
	want := "recovered: close of closed channel\ntrue\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestRecoverCatchesFailedTypeAssertion(t *testing.T) {
	// The type-assertion feature already produced a *panicError for a
	// failed single-result assertion; this just confirms it participates
	// in the same recover() mechanism as everything else here.
	out := runAndCapture(t, `
package main
import "fmt"
func safe() (ok bool) {
	defer func() {
		if recover() != nil {
			ok = true
		}
	}()
	var x any = 42
	_ = x.(string)
	return false
}
func main() { fmt.Println(safe()) }
`)
	want := "true\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestUnrecoveredRuntimePanicStillPropagates(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`
package main
func main() {
	a, b := 1, 0
	_ = a / b
}
`)
	if err == nil {
		t.Fatal("expected divide-by-zero to still fail the program when nothing recovers it")
	}
	if !strings.Contains(err.Error(), "integer divide by zero") {
		t.Errorf("expected an integer-divide-by-zero message, got %q", err.Error())
	}
}

func TestFloatDivisionByZeroDoesNotPanic(t *testing.T) {
	// Unlike integer division, Go's float64 division by zero yields ±Inf
	// or NaN per IEEE 754 rather than panicking — this must stay a plain,
	// successful computation, not an error at all (recoverable or not).
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	a, b := 1.0, 0.0
	fmt.Println(a / b)
	fmt.Println(-a / b)
	fmt.Println(0.0 / b)
}
`)
	want := "+Inf\n-Inf\nNaN\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestStructuralErrorsStayNonRecoverable(t *testing.T) {
	// Indexing an int isn't a Go runtime panic — it's something the type
	// checker would reject before the program ever ran — so unlike the
	// cases above, a defer+recover here must not stop the failure.
	vm, _ := newTestVM()
	err := vm.Run(`
package main
func main() {
	defer func() { recover() }()
	x := 5
	_ = x[0]
}
`)
	if err == nil {
		t.Fatal("expected indexing a non-indexable value to still fail even though a defer calls recover()")
	}
}
