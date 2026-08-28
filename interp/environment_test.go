// interp/environment_test.go
package interp

import (
	"strings"
	"testing"
)

// Env stores a scope's bindings across three places — the inline integer
// slot, the inline value table, and the Vars map — and which one a name
// occupies changes as the program assigns to it. These tests pin the
// invariant every lookup depends on: a name lives in exactly one of them at
// a time, so reading it back always yields the value most recently written,
// no matter how many bindings the scope holds or what types it moved
// through. See storeValueInEnv/declareInEnv in environment.go.

// TestScopeSpillsPastInlineValueTable declares more non-integer locals in one
// scope than the inline table can hold, forcing the overflow into Vars, and
// checks that the ones stored inline and the ones that spilled are all still
// readable and independently writable.
func TestScopeSpillsPastInlineValueTable(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	a := "a1"
	b := "b1"
	c := "c1"
	d := "d1"
	e := "e1"
	f := "f1"
	d = "d2"
	a = "a2"
	f = "f2"
	fmt.Println(a, b, c, d, e, f)
}
`)
	if got := strings.TrimSpace(out); got != "a2 b1 c1 d2 e1 f2" {
		t.Errorf("spilled scope readback = %q, want %q", got, "a2 b1 c1 d2 e1 f2")
	}
}

// TestBindingMovesBetweenIntAndValueStorage reassigns one name back and forth
// across the int/non-int boundary. Each assignment has to remove the binding
// from whichever table previously held it, or a later read finds the stale
// copy instead of the current value.
func TestBindingMovesBetweenIntAndValueStorage(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	var x interface{}
	x = 7
	fmt.Println(x)
	x = "seven"
	fmt.Println(x)
	x = 8
	fmt.Println(x)
	x = "eight"
	fmt.Println(x)
	x = 9
	fmt.Println(x)
}
`)
	want := []string{"7", "seven", "8", "eight", "9"}
	got := strings.Fields(strings.TrimSpace(out))
	for i, w := range want {
		if safeIndex(got, i) != w {
			t.Errorf("line %d = %q, want %q (full output %q)", i, safeIndex(got, i), w, out)
		}
	}
}

// TestShadowedBindingsPerScope checks that an inner scope's binding shadows
// an outer one of the same name and that the outer value is intact once the
// inner scope ends — the property that would break if an inner declaration
// were written into the wrong scope's table.
func TestShadowedBindingsPerScope(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	name := "outer"
	items := []string{"x"}
	{
		name := "inner"
		items := []string{"y", "z"}
		fmt.Println(name, len(items))
	}
	fmt.Println(name, len(items))
}
`)
	got := strings.TrimSpace(strings.ReplaceAll(out, "\r\n", "\n"))
	if got != "inner 2\nouter 1" {
		t.Errorf("shadowing readback = %q, want %q", got, "inner 2\nouter 1")
	}
}

// TestIntExprFastPathIndexAndLen covers tryEvalIntExpr's slice-index and
// len/cap cases. They evaluate `a[i]` and `len(a)` speculatively, ahead of
// the general evaluator, so this checks they agree with it: correct values,
// a real out-of-range panic rather than a silently wrong answer, and no
// confusion when the same syntax is applied to a map or a string.
func TestIntExprFastPathIndexAndLen(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	a := []int{5, 3, 9, 1}
	total := 0
	for i := 0; i < len(a); i++ { total = total + a[i] }
	fmt.Println("total", total)
	fmt.Println("cmp", a[0] > a[1], a[3] > a[2])
	fmt.Println("cap", len(a) == 4, cap(a) >= 4)

	s := "abcd"
	fmt.Println("strlen", len(s))

	m := map[string]int{"k": 11}
	fmt.Println("map", m["k"]+1, len(m))

	mixed := []interface{}{1, "two"}
	fmt.Println("mixed", mixed[0], mixed[1])
}
`)
	for _, want := range []string{
		"total 18", "cmp true false", "cap true true",
		"strlen 4", "map 12 1", "mixed 1 two",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output %q", want, out)
		}
	}
}

// TestSliceIndexOutOfRangeStillPanics makes sure the int fast path declines
// an out-of-range index instead of substituting a zero, leaving the general
// evaluator to raise the guest panic.
func TestSliceIndexOutOfRangeStillPanics(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`
package main
func main() {
	a := []int{1, 2}
	i := 5
	n := 0
	n = n + a[i]
	_ = n
}
`)
	if err == nil {
		t.Fatal("out-of-range index did not fail")
	}
	if !strings.Contains(err.Error(), "range") && !strings.Contains(err.Error(), "index") {
		t.Errorf("error = %v, want an index/range failure", err)
	}
}

// TestStepCountUnaffectedByBlockReservation guards the step accounting that
// checkpoint/chargeStep reserve in blocks: the reported total must be the
// exact number of checkpoints consumed, not the number reserved, and it must
// stay reproducible across identical runs.
func TestStepCountUnaffectedByBlockReservation(t *testing.T) {
	const src = `
package main
func main() {
	sum := 0
	for i := 0; i < 1000; i++ { sum = sum + i }
	_ = sum
}
`
	vm, _ := newTestVM()
	if err := vm.Run(src); err != nil {
		t.Fatalf("Run: %v", err)
	}
	first := vm.LastStepCount()
	if first == 0 {
		t.Fatal("LastStepCount = 0 after a counted run")
	}
	// A block reservation that leaked would show up as a total that is a
	// round multiple of stepBlock, or as drift between runs.
	for i := 0; i < 3; i++ {
		vm, _ := newTestVM()
		if err := vm.Run(src); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := vm.LastStepCount(); got != first {
			t.Fatalf("LastStepCount drifted: %d then %d", first, got)
		}
	}
}

// TestConcurrentGuestsShareStepAccounting drives several guest goroutines at
// once, with and without a step budget. checkpoint draws its steps from a
// block held in a plain (non-atomic) field, which is only sound because a
// block is never handed out while more than one goroutine is running guest
// code — see chargeStep. This is the shape that would trip the race detector
// (CI runs `make test-race`; -race needs a C compiler and does not run on a
// stock Windows dev box) if that invariant were ever broken, so it deliberately
// covers both the limited and the unlimited path, and re-enters the concurrent
// state repeatedly rather than just once.
func TestConcurrentGuestsShareStepAccounting(t *testing.T) {
	const src = `
package main
func worker(c chan int, n int) {
	s := 0
	for i := 0; i < n; i++ { s = s + i }
	c <- s
}
func main() {
	for round := 0; round < 5; round++ {
		c := make(chan int, 8)
		for w := 0; w < 8; w++ { go worker(c, 300) }
		total := 0
		for w := 0; w < 8; w++ { total = total + <-c }
		if total != 8*44850 { panic("bad total") }
	}
}
`
	for _, limits := range []ExecutionLimits{
		{MaxSteps: 0, MaxGoroutines: 0},              // unlimited: chargeStep's no-count path
		{MaxSteps: 50_000_000, MaxGoroutines: 1_024}, // counted: the block/atomic switch
	} {
		vm, _ := newTestVM()
		vm.Limits = limits
		if err := vm.Run(src); err != nil {
			t.Fatalf("limits %+v: Run: %v", limits, err)
		}
		if limits.MaxSteps == 0 {
			if got := vm.LastStepCount(); got != 0 {
				t.Errorf("unlimited run counted %d steps, want 0", got)
			}
			continue
		}
		if vm.LastStepCount() == 0 {
			t.Errorf("limits %+v: counted run reported 0 steps", limits)
		}
	}
}

// TestStepLimitFiresOnExactCheckpoint pins where the budget runs out. The
// limit must trip on checkpoint MaxSteps+1 regardless of how the counter is
// charged internally, including for budgets far smaller than one reservation
// block and for budgets straddling a block boundary.
func TestStepLimitFiresOnExactCheckpoint(t *testing.T) {
	for _, max := range []uint64{1, 2, 7, 50, stepBlock - 1, stepBlock, stepBlock + 1, 1000} {
		vm, _ := newTestVM()
		vm.Limits = ExecutionLimits{MaxSteps: max, MaxGoroutines: 1024}
		err := vm.Run(`package main
func main() { for { } }`)
		if err == nil {
			t.Fatalf("MaxSteps=%d: busy loop was not stopped", max)
		}
		if got := vm.LastStepCount(); got != max+1 {
			t.Errorf("MaxSteps=%d: LastStepCount = %d, want %d", max, got, max+1)
		}
	}
}
