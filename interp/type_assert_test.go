// interp/type_assert_test.go
package interp

import (
	"strings"
	"testing"
)

func TestTypeAssertCommaOkSuccess(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	var x any = 42
	n, ok := x.(int)
	fmt.Println(n, ok)
}
`)
	want := "42 true\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestTypeAssertCommaOkFailureDoesNotPanic(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	var x any = 42
	s, ok := x.(string)
	fmt.Println(s, ok)
	fmt.Println("still running")
}
`)
	if !strings.Contains(out, "still running") {
		t.Errorf("comma-ok failure should not abort execution: %q", out)
	}
	if !strings.Contains(out, "false") {
		t.Errorf("expected ok=false, got %q", out)
	}
}

func TestTypeAssertSingleResultPanicsOnFailure(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`
package main
func main() {
	var x any = 42
	_ = x.(string)
}
`)
	if err == nil {
		t.Fatal("expected a failed single-result type assertion to panic")
	}
	if !strings.Contains(err.Error(), "interface conversion") {
		t.Errorf("expected an 'interface conversion' panic message, got %q", err.Error())
	}
}

func TestTypeAssertSingleResultSucceeds(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	var x any = "hello"
	s := x.(string)
	fmt.Println(s)
}
`)
	want := "hello\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestTypeAssertStruct(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
type Point struct {
	X int
	Y int
}
type Circle struct {
	R int
}
func main() {
	var shape any = Point{X: 1, Y: 2}
	p, ok := shape.(Point)
	fmt.Println(p.X, p.Y, ok)
	_, ok2 := shape.(Circle)
	fmt.Println(ok2)
}
`)
	want := "1 2 true\nfalse\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestTypeAssertAnyAlwaysSucceeds(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
type Point struct{ X int }
func main() {
	var x any = Point{X: 5}
	p, ok := x.(any)
	fmt.Println(ok)
	q := p.(Point)
	fmt.Println(q.X)
}
`)
	want := "true\n5\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestTypeAssertNilNeverSucceeds(t *testing.T) {
	// A nil interface value fails every type assertion, including against
	// the empty interface itself — nanoGo's `var x any` (no initializer)
	// currently produces a zero-value placeholder rather than a true nil
	// (a separate, pre-existing gap), so get a genuine nil via a function
	// result instead.
	out := runAndCapture(t, `
package main
import "fmt"
func nothing() any { return nil }
func main() {
	x := nothing()
	_, ok := x.(int)
	fmt.Println(ok)
	_, ok2 := x.(any)
	fmt.Println(ok2)
}
`)
	want := "false\nfalse\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestTypeAssertTypedNilStructPointerFailsWithoutPanicking(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
type Point struct{ X int }
func main() {
	var point *Point
	var value any = point
	_, ok := value.(Point)
	fmt.Println(ok)
}`)
	if got, want := out, "false\n"; got != want {
		t.Fatalf("typed nil assertion = %q, want %q", got, want)
	}
}

func TestTypeAssertErrorInterfaceFromNativeError(t *testing.T) {
	// Calling .Error() on the resulting value isn't exercised here: nanoGo
	// has no generic method dispatch for arbitrary native Go values (only
	// for its own registered types, see vm.types), which is a separate,
	// pre-existing limitation unrelated to the type assertion itself.
	out := runAndCapture(t, `
package main
import (
	"fmt"
	"strconv"
)
func main() {
	_, err := strconv.Atoi("not-a-number")
	var x any = err
	_, ok := x.(error)
	fmt.Println(ok)
}
`)
	want := "true\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestTypeAssertErrorInterfaceFailsForNonError(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	var x any = 42
	_, ok := x.(error)
	fmt.Println(ok)
}
`)
	want := "false\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestTypeAssertUserDefinedInterface(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
type Stringer interface {
	String() string
}
type Named struct {
	Name string
}
func (n Named) String() string { return n.Name }
type Unnamed struct {
	Value int
}
func main() {
	var x any = Named{Name: "alice"}
	s, ok := x.(Stringer)
	fmt.Println(ok)
	fmt.Println(s.String())

	var y any = Unnamed{Value: 1}
	_, ok2 := y.(Stringer)
	fmt.Println(ok2)
}
`)
	want := "true\nalice\nfalse\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestTypeAssertSlice(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	var x any = []int{1, 2, 3}
	s, ok := x.([]int)
	fmt.Println(ok, len(s))
	_, ok2 := x.([]string)
	fmt.Println(ok2)
}
`)
	want := "true 3\nfalse\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}

func TestTypeAssertMap(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	var x any = map[string]int{"a": 1}
	m, ok := x.(map[string]int)
	fmt.Println(ok, len(m))
	_, ok2 := x.(map[string]string)
	fmt.Println(ok2)
}
`)
	want := "true 1\nfalse\n"
	if out != want {
		t.Errorf("want %q, got %q", want, out)
	}
}
