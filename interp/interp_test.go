package interp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func newTestVM() (*Interpreter, *strings.Builder) {
	vm := NewInterpreter()
	// Most interpreter tests exercise package behaviour rather than the
	// capability policy itself; policy-specific tests use a fresh zero-policy VM.
	vm.Capabilities = FullCapabilities()
	var buf strings.Builder
	var bufMu sync.Mutex

	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			bufMu.Lock()
			buf.WriteString(ToString(args[0]))
			buf.WriteByte('\n')
			bufMu.Unlock()
		}
		return nil, nil
	})
	vm.RegisterNative("ConsoleWarn", func(args []any) (any, error) { return nil, nil })
	vm.RegisterNative("ConsoleError", func(args []any) (any, error) { return nil, nil })
	vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		format := ToString(args[0])
		fmtArgs := make([]any, 0, len(args)-1)
		for _, a := range args[1:] {
			fmtArgs = append(fmtArgs, a)
		}
		return fmt.Sprintf(format, fmtArgs...), nil
	})

	RegisterBuiltinPackages(vm)
	return vm, &buf
}

func runAndCapture(t *testing.T, src string) string {
	t.Helper()
	vm, buf := newTestVM()
	if err := vm.Run(src); err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	return buf.String()
}

func safeIndex(s []string, i int) string {
	if i < len(s) {
		return s[i]
	}
	return "<missing>"
}

func TestHelloWorld(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() { fmt.Println("hello world") }
`)
	if !strings.Contains(out, "hello world") {
		t.Errorf("expected 'hello world', got %q", out)
	}
}

func TestVariablesAndArithmetic(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	x := 10
	y := 3
	fmt.Println(x + y)
	fmt.Println(x - y)
	fmt.Println(x * y)
	fmt.Println(x % y)
}

`)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{"13", "7", "30", "1"}
	for i, want := range expected {
		if i >= len(lines) || strings.TrimSpace(lines[i]) != want {
			t.Errorf("line %d: want %q, got %q", i, want, safeIndex(lines, i))
		}
	}
}

func TestShortDeclarationReusesAndShadowsByScope(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	x := 1
	x, y := 2, 3
	{
		x := 10
		x, z := 11, 12
		fmt.Println(x, z)
	}
	fmt.Println(x, y)
}
`)
	if got, want := strings.TrimSpace(out), "11 12\n2 3"; got != want {
		t.Fatalf("short declaration scope = %q, want %q", got, want)
	}
}

func TestShortDeclarationRequiresNewName(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`package main
func main() {
	x := 1
	x := 2
	_ = x
}`)
	if err == nil || !strings.Contains(err.Error(), "no new variables on left side of :=") {
		t.Fatalf("second short declaration error = %v, want no-new-variable error", err)
	}
}

func TestIntegerBindingsBeyondInlineSlot(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	a, b, c := 1, 2, 3
	b++
	c += 4
	fmt.Println(a, b, c)
}
`)
	if got, want := strings.TrimSpace(out), "1 3 7"; got != want {
		t.Fatalf("integer bindings beyond inline slot = %q, want %q", got, want)
	}
}

// TestIntegerDivisionTruncates guards against a regression where the '/'
// operator always performed float64 division regardless of operand types,
// silently breaking any int arithmetic that divides (midpoints, averages,
// pagination, ...) with no visible error — `7 / 2` printed "3.5" instead of
// Go's "3". Division must match Go's int/int truncation-toward-zero, while
// still promoting to float64 division when either operand is float64.
func TestIntegerDivisionTruncates(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	a, b := 7, 2
	fmt.Println(a / b)
	fmt.Println(-7 / 2)
	var x float64 = 7
	fmt.Println(x / 2)
	fmt.Println(7.0 / 2)
}
`)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{"3", "-3", "3.5", "3.5"}
	for i, want := range expected {
		if i >= len(lines) || strings.TrimSpace(lines[i]) != want {
			t.Errorf("line %d: want %q, got %q", i, want, safeIndex(lines, i))
		}
	}
}

func TestIntegerDivisionByZero(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`
package main
func main() {
	a, b := 1, 0
	_ = a / b
}
`)
	if err == nil {
		t.Fatal("expected an error for integer division by zero")
	}
}

func TestIntegerBindingCanChangeToDynamicValue(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	x := 42
	x = "updated"
	fmt.Println(x)
}
`)
	if !strings.Contains(out, "updated") {
		t.Errorf("expected dynamic reassignment to survive int fast path, got %q", out)
	}
}

func TestFloatArithmetic(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	a := 3.14
	b := 2.0
	fmt.Println(a + b)
}
`)
	if !strings.Contains(out, "5.14") {
		t.Errorf("expected '5.14', got %q", out)
	}
}

func TestFunctionCallAndReturn(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func add(a int, b int) int { return a + b }
func main() { fmt.Println(add(3, 7)) }
`)
	if !strings.Contains(out, "10") {
		t.Errorf("expected '10', got %q", out)
	}
}

func TestRecursion(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func fib(n int) int {
	if n <= 1 { return n }
	return fib(n-1) + fib(n-2)
}
func main() { fmt.Println(fib(10)) }
`)
	if !strings.Contains(out, "55") {
		t.Errorf("expected '55', got %q", out)
	}
}

func TestIfElse(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	x := 5
	if x > 3 {
		fmt.Println("big")
	} else {
		fmt.Println("small")
	}
}
`)
	if !strings.Contains(out, "big") {
		t.Errorf("expected 'big', got %q", out)
	}
}

func TestForLoop(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	sum := 0
	for i := 0; i < 5; i++ { sum = sum + i }
	fmt.Println(sum)
}
`)
	if !strings.Contains(out, "10") {
		t.Errorf("expected '10', got %q", out)
	}
}

func TestForBreakContinue(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	sum := 0
	for i := 0; i < 10; i++ {
		if i == 7 { break }
		if i % 2 == 0 { continue }
		sum = sum + i
	}
	fmt.Println(sum)
}
`)
	if !strings.Contains(out, "9") {
		t.Errorf("expected '9', got %q", out)
	}
}

func TestSwitch(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	x := 2
	switch x {
	case 1:
		fmt.Println("one")
	case 2:
		fmt.Println("two")
	default:
		fmt.Println("other")
	}
}
`)
	if !strings.Contains(out, "two") {
		t.Errorf("expected 'two', got %q", out)
	}
}

func TestSliceLiteral(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	s := []int{10, 20, 30}
	fmt.Println(len(s))
	fmt.Println(s[1])
}
`)
	if !strings.Contains(out, "3") || !strings.Contains(out, "20") {
		t.Errorf("expected '3' and '20', got %q", out)
	}
}

func TestSliceAppend(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	s := []int{1, 2}
	s = append(s, 3, 4)
	fmt.Println(len(s))
}
`)
	if !strings.Contains(out, "4") {
		t.Errorf("expected '4', got %q", out)
	}
}

// TestSliceCopyOverlapping proves builtinCopy is overlap-safe (memmove-based,
// like real Go's copy()) rather than a naive forward-only element loop,
// which would corrupt data on the classic "shift right to insert" idiom
// exercised here — dst and src alias the same backing array via slicing.
func TestSliceCopyOverlapping(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	s := []int{1, 2, 3, 4, 0}
	copy(s[1:], s[0:4])
	fmt.Println(s[0], s[1], s[2], s[3], s[4])
}
`)
	if !strings.Contains(out, "1 1 2 3 4") {
		t.Errorf("expected '1 1 2 3 4', got %q", out)
	}
}

func TestMapLiteral(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	m := map[string]int{"a": 1, "b": 2}
	fmt.Println(m["a"])
	fmt.Println(len(m))
}
`)
	if !strings.Contains(out, "1") || !strings.Contains(out, "2") {
		t.Errorf("expected '1' and '2', got %q", out)
	}
}

func TestMapDelete(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	m := map[string]int{"x": 10, "y": 20}
	delete(m, "x")
	fmt.Println(len(m))
}

func TestStringMapLazilyStoresOriginalKeys(t *testing.T) {
	m := &MapVal{KeyType: "string", ElementType: "int", Data: make(map[string]any)}
	m.setByKey("alpha", 1)
	if m.Keys != nil {
		t.Fatal("string-only map allocated redundant original-key table")
	}
	m.setByKey(7, 2) // nanoGo permits dynamic key values at runtime.
	if m.Keys == nil || m.originalKey("alpha") != "alpha" || m.originalKey(m.hash(7)) != 7 {
		t.Fatalf("dynamic key fallback lost original keys: %#v", m.Keys)
	}
	native := ToNativeValue(m).(map[string]any)
	if native["alpha"] != 1 || native["7"] != 2 {
		t.Fatalf("native string map = %#v, want alpha and dynamic key values", native)
	}
}
`)
	if !strings.Contains(out, "1") {
		t.Errorf("expected '1', got %q", out)
	}
}

func TestStructAndMethod(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
type Rect struct {
	W int
	H int
}
func (r Rect) Area() int { return r.W * r.H }
func main() {
	r := Rect{W: 3, H: 4}
	fmt.Println(r.Area())
}
`)
	if !strings.Contains(out, "12") {
		t.Errorf("expected '12', got %q", out)
	}
}

func TestFuncLitClosure(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	x := 10
	add := func(y int) int { return x + y }
	fmt.Println(add(5))
}
`)
	if !strings.Contains(out, "15") {
		t.Errorf("expected '15', got %q", out)
	}
}

func TestChannelSendReceive(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	ch := make(chan int, 1)
	ch <- 42
	v := <-ch
	fmt.Println(v)
}
`)
	if !strings.Contains(out, "42") {
		t.Errorf("expected '42', got %q", out)
	}
}

func TestChannelRange(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
import "sync"
func main() {
	ch := make(chan int, 3)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ch <- 1
		ch <- 2
		ch <- 3
		close(ch)
	}()
	for v := range ch {
		fmt.Println(v)
	}
	wg.Wait()
}
`)
	if !strings.Contains(out, "1") || !strings.Contains(out, "2") || !strings.Contains(out, "3") {
		t.Errorf("expected 1,2,3, got %q", out)
	}
}

func TestEnsureNativeWaitGroupInitializesOnceConcurrently(t *testing.T) {
	value := &StructVal{TypeName: "WaitGroup", Fields: map[string]any{}}
	const workers = 32
	start := make(chan struct{})
	instances := make([]*nativeWaitGroup, workers)
	var workersWG sync.WaitGroup
	for i := range instances {
		workersWG.Add(1)
		go func(i int) {
			defer workersWG.Done()
			<-start
			instances[i] = ensureNativeWG(value)
		}(i)
	}
	close(start)
	workersWG.Wait()
	first := instances[0]
	if first == nil {
		t.Fatal("ensureNativeWG returned nil")
	}
	for i, instance := range instances[1:] {
		if instance != first {
			t.Fatalf("worker %d received a different WaitGroup backing object", i+1)
		}
	}
}

func TestStringPackage(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
import "strings"
func main() {
	fmt.Println(strings.ToUpper("hello"))
	fmt.Println(strings.Contains("foobar", "oba"))
}
`)
	if !strings.Contains(out, "HELLO") {
		t.Errorf("expected 'HELLO', got %q", out)
	}
}

func TestMathPackage(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
import "math"
func main() {
	fmt.Println(math.Sqrt(144.0))
	fmt.Println(math.Pow(2.0, 8.0))
}
`)
	if !strings.Contains(out, "12") {
		t.Errorf("expected '12', got %q", out)
	}
	if !strings.Contains(out, "256") {
		t.Errorf("expected '256', got %q", out)
	}
}

func TestFmtSprintf(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	s := fmt.Sprintf("x=%d y=%d", 10, 20)
	fmt.Println(s)
}

func TestHostSprintfKeepsDynamicFormatArgumentsUnchanged(t *testing.T) {
	args := []any{42, "value"}
	var received []any
	result, err := callHostSprintf(func(values []any) (any, error) {
		received = append([]any(nil), values...)
		return "ok", nil
	}, args, "42")
	if err != nil || result != "ok" {
		t.Fatalf("callHostSprintf = %v, %v", result, err)
	}
	if got, want := args[0], any(42); got != want {
		t.Fatalf("original format argument = %#v, want %#v", got, want)
	}
	if got, want := received[0], any("42"); got != want {
		t.Fatalf("host format argument = %#v, want %#v", got, want)
	}
}
`)
	if !strings.Contains(out, "x=10 y=20") {
		t.Errorf("expected 'x=10 y=20', got %q", out)
	}
}

func TestPanicError(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`
package main
func main() { panic("boom") }
`)
	if err == nil {
		t.Fatal("expected an error from panic")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Errorf("expected panic message 'boom', got %q", err.Error())
	}
}

func TestNoMainFunc(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`
package main
func helper() {}
`)
	if err == nil {
		t.Fatal("expected error for missing main()")
	}
}

func TestWrongPackageName(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`
package foo
func main() {}
`)
	if err == nil {
		t.Fatal("expected error for non-main package")
	}
}

func TestUndefinedVariable(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`
package main
import "fmt"
func main() { fmt.Println(xyz) }
`)
	if err == nil {
		t.Fatal("expected error for undefined variable")
	}
}

func TestIndexOutOfRange(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`
package main
func main() {
	s := []int{1, 2, 3}
	_ = s[10]
}
`)
	if err == nil {
		t.Fatal("expected index out of range error")
	}
}

func TestToInt(t *testing.T) {
	cases := []struct {
		in   any
		want int
	}{
		{42, 42},
		{int64(99), 99},
		{3.7, 3},
		{true, 1},
		{false, 0},
		{"123", 123},
		{"-5", -5},
		{nil, 0},
	}
	for _, c := range cases {
		got := ToInt(c.in)
		if got != c.want {
			t.Errorf("ToInt(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestToFloat(t *testing.T) {
	cases := []struct {
		in   any
		want float64
	}{
		{3.14, 3.14},
		{42, 42.0},
		{true, 1.0},
		{"2.5", 2.5},
	}
	for _, c := range cases {
		got := ToFloat(c.in)
		if got != c.want {
			t.Errorf("ToFloat(%v) = %f, want %f", c.in, got, c.want)
		}
	}
}

func TestToBool(t *testing.T) {
	if ToBool(0) != false {
		t.Error("ToBool(0) should be false")
	}
	if ToBool(1) != true {
		t.Error("ToBool(1) should be true")
	}
	if ToBool("") != false {
		t.Error("ToBool empty string should be false")
	}
	if ToBool("hello") != true {
		t.Error("ToBool hello should be true")
	}
	if ToBool(nil) != false {
		t.Error("ToBool(nil) should be false")
	}
}

func TestToString(t *testing.T) {
	if ToString(42) != "42" {
		t.Errorf("ToString(42) = %q", ToString(42))
	}
	if ToString(3.14) != "3.14" {
		t.Errorf("ToString(3.14) = %q", ToString(3.14))
	}
	if ToString(true) != "true" {
		t.Errorf("ToString(true) = %q", ToString(true))
	}
}

func TestEnvScoping(t *testing.T) {
	vm := NewInterpreter()

	// Vars is allocated lazily on first declare() (see NewEnv's doc comment)
	// rather than eagerly by NewEnv itself, so scoping must be exercised
	// through declare rather than by writing the map directly.
	parent := NewEnv(nil)
	vm.declare("x", 10, parent)
	child := NewEnv(parent)
	vm.declare("y", 20, child)

	v, ok := vm.get("x", child)
	if !ok || v != 10 {
		t.Errorf("expected x=10 from parent, got %v ok=%v", v, ok)
	}

	v, ok = vm.get("y", child)
	if !ok || v != 20 {
		t.Errorf("expected y=20, got %v ok=%v", v, ok)
	}

	_, ok = vm.get("z", child)
	if ok {
		t.Error("expected z to be undefined")
	}
}

func TestConstDeclaration(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
const Pi = 3
func main() { fmt.Println(Pi) }
`)
	if !strings.Contains(out, "3") {
		t.Errorf("expected '3', got %q", out)
	}
}

func TestDefer(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func greet() {
	defer fmt.Println("world")
	fmt.Println("hello")
}
func main() { greet() }
`)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), out)
	}
	if strings.TrimSpace(lines[0]) != "hello" {
		t.Errorf("line 0: want 'hello', got %q", lines[0])
	}
	if strings.TrimSpace(lines[1]) != "world" {
		t.Errorf("line 1: want 'world', got %q", lines[1])
	}
}

// TestDeferBuiltinClose covers `defer close(ch)`: prepareCall must resolve a
// bare builtin identifier in a defer's call expression instead of only
// accepting real *Function values, or this fails with "undefined: close".
func TestDeferBuiltinClose(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func work(ch chan int) {
	defer close(ch)
	ch <- 1
}
func main() {
	ch := make(chan int, 1)
	work(ch)
	v, ok := <-ch
	fmt.Println(v)
	fmt.Println(ok)
	_, ok2 := <-ch
	fmt.Println(ok2)
}
`)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected 3 lines, got %d: %q", len(lines), out)
	}
	if strings.TrimSpace(lines[0]) != "1" {
		t.Errorf("line 0: want '1', got %q", lines[0])
	}
	if strings.TrimSpace(lines[1]) != "true" {
		t.Errorf("line 1: want 'true', got %q", lines[1])
	}
	if strings.TrimSpace(lines[2]) != "false" {
		t.Errorf("line 2: want 'false' (channel should be closed by the deferred close), got %q", lines[2])
	}
}

// TestDeferBuiltinDelete covers `defer delete(m, k)`, a second bare-builtin
// defer that additionally checks defer's "capture args now, execute later"
// semantics: the map and key are evaluated when the defer statement runs,
// but delete itself only happens once work returns.
func TestDeferBuiltinDelete(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func work(m map[string]int) {
	defer delete(m, "a")
	fmt.Println(len(m))
}
func main() {
	m := map[string]int{"a": 1, "b": 2}
	work(m)
	fmt.Println(len(m))
}
`)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), out)
	}
	if strings.TrimSpace(lines[0]) != "2" {
		t.Errorf("line 0: want '2' (both keys still present while work's body runs), got %q", lines[0])
	}
	if strings.TrimSpace(lines[1]) != "1" {
		t.Errorf("line 1: want '1' (key \"a\" removed by the deferred delete), got %q", lines[1])
	}
}

// TestGoBuiltinClose covers `go close(ch)`: the GoStmt case shares
// prepareCall with defer, so a bare builtin identifier must resolve there
// too instead of failing with "undefined: close".
func TestGoBuiltinClose(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	ch := make(chan int)
	go close(ch)
	_, ok := <-ch
	fmt.Println(ok)
}
`)
	if strings.TrimSpace(out) != "false" {
		t.Errorf("expected 'false' (channel closed via go close(ch)), got %q", out)
	}
}

func TestForRangeSlice(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	nums := []int{10, 20, 30}
	sum := 0
	for _, v := range nums { sum = sum + v }
	fmt.Println(sum)
}
`)
	if !strings.Contains(out, "60") {
		t.Errorf("expected '60', got %q", out)
	}
}

func TestForRangeAssignmentUsesOuterBindings(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	values := []int{4, 5, 6}
	index, value, sum := -1, -1, 0
	for index, value = range values { sum = sum + index + value }
	fmt.Println(index, value, sum)
}
`)
	if strings.TrimSpace(out) != "2 6 18" {
		t.Errorf("expected outer range bindings to be updated, got %q", out)
	}
}

func TestForRangeMap(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	m := map[string]int{"a": 1, "b": 2}
	sum := 0
	for _, v := range m { sum = sum + v }
	fmt.Println(sum)
}
`)
	if !strings.Contains(out, "3") {
		t.Errorf("expected '3', got %q", out)
	}
}

func TestStringMapCounterReadModifyWrite(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	counts := map[string]int{}
	for i := 0; i < 5; i++ { counts["hits"] = counts["hits"] + i }
	fmt.Println(counts["hits"])
}
`)
	if strings.TrimSpace(out) != "10" {
		t.Errorf("map counter = %q, want 10", out)
	}
}

func TestStringIndexAndSlice(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	s := "hello"
	fmt.Println(len(s))
	fmt.Println(s[0:2])
}
`)
	if !strings.Contains(out, "5") {
		t.Errorf("expected '5', got %q", out)
	}
	if !strings.Contains(out, "he") {
		t.Errorf("expected 'he', got %q", out)
	}
}

func TestBitwiseOps(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	fmt.Println(6 & 3)
	fmt.Println(6 | 3)
	fmt.Println(6 ^ 3)
	fmt.Println(1 << 4)
}
`)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{"2", "7", "5", "16"}
	for i, want := range expected {
		if i >= len(lines) || strings.TrimSpace(lines[i]) != want {
			t.Errorf("bitwise line %d: want %q, got %q", i, want, safeIndex(lines, i))
		}
	}
}

func TestSortInts(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
import "sort"
func main() {
	s := []int{3, 1, 2}
	sort.Ints(s)
	fmt.Println(s[0])
	fmt.Println(s[1])
	fmt.Println(s[2])
}
`)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{"1", "2", "3"}
	for i, want := range expected {
		if i >= len(lines) || strings.TrimSpace(lines[i]) != want {
			t.Errorf("sort line %d: want %q, got %q", i, want, safeIndex(lines, i))
		}
	}
}

func TestIsZero(t *testing.T) {
	if !IsZero(nil) {
		t.Error("nil should be zero")
	}
	if !IsZero(0) {
		t.Error("0 should be zero")
	}
	if !IsZero("") {
		t.Error("empty string should be zero")
	}
	if !IsZero(false) {
		t.Error("false should be zero")
	}
	if IsZero(1) {
		t.Error("1 should not be zero")
	}
}

func TestHashKey(t *testing.T) {
	h1 := hashKey(42)
	h2 := hashKey(42)
	if h1 != h2 {
		t.Errorf("same int should hash the same: %q vs %q", h1, h2)
	}
	h3 := hashKey("hello")
	h4 := hashKey("hello")
	if h3 != h4 {
		t.Errorf("same string should hash the same: %q vs %q", h3, h4)
	}
	if h1 == h3 {
		t.Error("int and string should hash differently")
	}
	if got, want := hashKey(1.5), "f:1.5"; got != want {
		t.Errorf("float hash = %q, want %q", got, want)
	}
	if hashKey(1) == hashKey(1.0) {
		t.Error("int and float should hash differently")
	}
}

// ---------- select statement ----------

func TestSelectReceiveReady(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	ch := make(chan int, 1)
	ch <- 42
	select {
	case v := <-ch:
		fmt.Println(v)
	default:
		fmt.Println("default")
	}
}
`)
	if !strings.Contains(out, "42") {
		t.Errorf("expected '42', got %q", out)
	}
}

func TestSelectDefault(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	ch := make(chan int, 1)
	select {
	case v := <-ch:
		fmt.Println(v)
	default:
		fmt.Println("default")
	}
}
`)
	if !strings.Contains(out, "default") {
		t.Errorf("expected 'default', got %q", out)
	}
}

func TestSelectSend(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	ch := make(chan int, 1)
	select {
	case ch <- 7:
		fmt.Println("sent")
	default:
		fmt.Println("blocked")
	}
	fmt.Println(<-ch)
}
`)
	if !strings.Contains(out, "sent") || !strings.Contains(out, "7") {
		t.Errorf("expected 'sent' and '7', got %q", out)
	}
}

func TestSelectReceiveOK(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	ch := make(chan string, 1)
	ch <- "hello"
	close(ch)
	select {
	case v, ok := <-ch:
		fmt.Println(v)
		fmt.Println(ok)
	}
}
`)
	if !strings.Contains(out, "hello") {
		t.Errorf("expected 'hello', got %q", out)
	}
}

// ---------- strconv package ----------

func TestStrconvItoaAtoi(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
import "strconv"
func main() {
	s := strconv.Itoa(99)
	fmt.Println(s)
	n, _ := strconv.Atoi("42")
	fmt.Println(n)
}
`)
	if !strings.Contains(out, "99") || !strings.Contains(out, "42") {
		t.Errorf("expected '99' and '42', got %q", out)
	}
}

func TestStrconvFormatBoolParseBool(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
import "strconv"
func main() {
	fmt.Println(strconv.FormatBool(true))
	b, _ := strconv.ParseBool("false")
	fmt.Println(b)
}
`)
	if !strings.Contains(out, "true") || !strings.Contains(out, "false") {
		t.Errorf("expected 'true' and 'false', got %q", out)
	}
}

func TestStrconvFormatFloatAcceptsStringAndByteFormat(t *testing.T) {
	out := runAndCapture(t, `
package main
import (
	"fmt"
	"strconv"
)
func main() {
	fmt.Println(strconv.FormatFloat(3.5, "f", 1, 64))
	fmt.Println(strconv.FormatFloat(3.5, 101, 1, 64))
}
`)
	if strings.TrimSpace(out) != "3.5\n3.5e+00" {
		t.Errorf("FormatFloat output = %q, want string and byte format forms", out)
	}
}

// ---------- strings package additions ----------

func TestStringsHasPrefixSuffix(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
import "strings"
func main() {
	fmt.Println(strings.HasPrefix("foobar", "foo"))
	fmt.Println(strings.HasSuffix("foobar", "bar"))
	fmt.Println(strings.HasPrefix("foobar", "baz"))
}
`)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{"true", "true", "false"}
	for i, want := range expected {
		if i >= len(lines) || strings.TrimSpace(lines[i]) != want {
			t.Errorf("line %d: want %q, got %q", i, want, safeIndex(lines, i))
		}
	}
}

func TestStringsTrimPrefixSuffix(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
import "strings"
func main() {
	fmt.Println(strings.TrimPrefix("foobar", "foo"))
	fmt.Println(strings.TrimSuffix("foobar", "bar"))
	fmt.Println(strings.Count("cheese", "e"))
	fmt.Println(strings.Index("foobar", "bar"))
	fmt.Println(strings.Repeat("ab", 3))
}
`)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{"bar", "foo", "3", "3", "ababab"}
	for i, want := range expected {
		if i >= len(lines) || strings.TrimSpace(lines[i]) != want {
			t.Errorf("line %d: want %q, got %q", i, want, safeIndex(lines, i))
		}
	}
}

// ---------- math package additions ----------

func TestMathFloorCeilRound(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
import "math"
func main() {
	fmt.Println(math.Floor(3.7))
	fmt.Println(math.Ceil(3.2))
	fmt.Println(math.Round(3.5))
	fmt.Println(math.Max(2.0, 5.0))
	fmt.Println(math.Min(2.0, 5.0))
}
`)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	expected := []string{"3", "4", "4", "5", "2"}
	for i, want := range expected {
		if i >= len(lines) || strings.TrimSpace(lines[i]) != want {
			t.Errorf("math line %d: want %q, got %q", i, want, safeIndex(lines, i))
		}
	}
}

func TestMathPiConstant(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
import "math"
func main() {
	fmt.Println(math.Pi > 3.14)
}
`)
	if !strings.Contains(out, "true") {
		t.Errorf("expected 'true' for math.Pi > 3.14, got %q", out)
	}
}

// ---------- sort additions ----------

func TestSortStrings(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
import "sort"
func main() {
	s := []string{"banana", "apple", "cherry"}
	sort.Strings(s)
	fmt.Println(s[0])
}
`)
	if !strings.Contains(out, "apple") {
		t.Errorf("expected 'apple', got %q", out)
	}
}

// ---------- Persistent VM behaviour (REPL scenario) ----------

func TestPersistentVMFunctionAccess(t *testing.T) {
	// Simulate the REPL's persistent VM: declare a function, then call it in a
	// separate Run invocation on the same VM.
	vm, buf := newTestVM()
	if err := vm.Run("package main\nfunc greet() string { return \"hello\" }\nfunc main() {}\n"); err != nil {
		t.Fatalf("declare greet: %v", err)
	}
	if err := vm.Run("package main\nimport \"fmt\"\nfunc main() { fmt.Println(greet()) }\n"); err != nil {
		t.Fatalf("call greet: %v", err)
	}
	if !strings.Contains(buf.String(), "hello") {
		t.Errorf("expected 'hello', got %q", buf.String())
	}
}

func TestPersistentVMVarPersistence(t *testing.T) {
	// Simulate the REPL's var conversion: declare a top-level var, then read it.
	vm, buf := newTestVM()
	if err := vm.Run("package main\nvar x = 42\nfunc main() {}\n"); err != nil {
		t.Fatalf("declare x: %v", err)
	}
	if err := vm.Run("package main\nimport \"fmt\"\nfunc main() { fmt.Println(x) }\n"); err != nil {
		t.Fatalf("read x: %v", err)
	}
	if !strings.Contains(buf.String(), "42") {
		t.Errorf("expected '42', got %q", buf.String())
	}
}

// --------------- VFS tests ---------------

func TestVFSReadWrite(t *testing.T) {
	fs := NewVFS()
	if err := fs.WriteFile("/tmp/hello.txt", []byte("hello vfs"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	data, err := fs.ReadFile("/tmp/hello.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello vfs" {
		t.Errorf("expected 'hello vfs', got %q", string(data))
	}
}

func TestVFSWriteFileOverwriteUpdatesExistingNode(t *testing.T) {
	fs := NewVFS()
	if err := fs.WriteFile("/tmp/rewrite.txt", []byte("old"), 0600); err != nil {
		t.Fatalf("initial WriteFile: %v", err)
	}
	before := fs.Revision()
	if err := fs.WriteFile("/tmp/rewrite.txt", []byte("new value"), 0640); err != nil {
		t.Fatalf("overwrite WriteFile: %v", err)
	}
	data, err := fs.ReadFile("/tmp/rewrite.txt")
	if err != nil || string(data) != "new value" {
		t.Fatalf("overwritten content = %q, %v", data, err)
	}
	info, err := fs.Stat("/tmp/rewrite.txt")
	if err != nil || info.Mode != 0640 || fs.Revision() != before+1 {
		t.Fatalf("overwrite metadata = %+v, revision=%d, err=%v", info, fs.Revision(), err)
	}
}

func TestVFSMkdirAndReadDir(t *testing.T) {
	fs := NewVFS()
	if err := fs.MkdirAll("/tmp/a/b/c", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	_ = fs.WriteFile("/tmp/a/file.txt", []byte("x"), 0644)
	entries, err := fs.ReadDir("/tmp/a")
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := make(map[string]bool)
	for _, e := range entries {
		names[e.Name] = true
	}
	if !names["b"] || !names["file.txt"] {
		t.Errorf("expected 'b' and 'file.txt' in /tmp/a, got %v", names)
	}
}

func TestVFSStat(t *testing.T) {
	fs := NewVFS()
	_ = fs.WriteFile("/tmp/s.txt", []byte("data"), 0644)
	fi, err := fs.Stat("/tmp/s.txt")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if fi.IsDir || fi.Size != 4 || fi.Name != "s.txt" {
		t.Errorf("unexpected stat: %+v", fi)
	}
	di, err := fs.Stat("/tmp")
	if err != nil {
		t.Fatalf("Stat /tmp: %v", err)
	}
	if !di.IsDir {
		t.Errorf("expected /tmp to be a directory")
	}
}

func TestVFSRemove(t *testing.T) {
	fs := NewVFS()
	_ = fs.WriteFile("/tmp/rm.txt", []byte("bye"), 0644)
	if err := fs.Remove("/tmp/rm.txt"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := fs.ReadFile("/tmp/rm.txt"); err == nil {
		t.Error("expected error after removal")
	}
}

// TestVFSRemoveRejectsNonEmptyDir proves Remove's "directory not empty"
// check (now backed by the children index's O(1) len check instead of a
// full-map scan) still correctly rejects removing a directory that has
// children, and that removing the child first lets a subsequent Remove of
// the (now empty) parent succeed.
func TestVFSRemoveRejectsNonEmptyDir(t *testing.T) {
	fs := NewVFS()
	if err := fs.Mkdir("/tmp/nonempty", 0755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	if err := fs.WriteFile("/tmp/nonempty/child.txt", []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := fs.Remove("/tmp/nonempty"); err == nil {
		t.Fatal("expected Remove to reject a non-empty directory")
	}
	if err := fs.Remove("/tmp/nonempty/child.txt"); err != nil {
		t.Fatalf("Remove child: %v", err)
	}
	if err := fs.Remove("/tmp/nonempty"); err != nil {
		t.Errorf("expected Remove to succeed once the directory is empty, got %v", err)
	}
}

// TestVFSRemoveAllNestedTreeAndReadDirIsolation proves the children-index
// bookkeeping (addChildLocked/removeChildLocked/removeSubtreeLocked) stays
// correct for a multi-level tree: RemoveAll deletes every descendant (not
// just the top-level node), and ReadDir on a surviving sibling directory
// never leaks entries from the removed subtree or from unrelated directories
// elsewhere in the VFS (the exact bug an incorrectly-maintained parent-path
// index could introduce).
func TestVFSRemoveAllNestedTreeAndReadDirIsolation(t *testing.T) {
	fs := NewVFS()
	if err := fs.MkdirAll("/tmp/tree/a/b", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := fs.WriteFile("/tmp/tree/a/b/leaf.txt", []byte("x"), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := fs.MkdirAll("/tmp/sibling", 0755); err != nil {
		t.Fatalf("MkdirAll sibling: %v", err)
	}
	if err := fs.WriteFile("/tmp/sibling/keep.txt", []byte("keep"), 0644); err != nil {
		t.Fatalf("WriteFile sibling: %v", err)
	}

	if err := fs.RemoveAll("/tmp/tree"); err != nil {
		t.Fatalf("RemoveAll: %v", err)
	}
	for _, p := range []string{"/tmp/tree", "/tmp/tree/a", "/tmp/tree/a/b", "/tmp/tree/a/b/leaf.txt"} {
		if _, err := fs.Stat(p); err == nil {
			t.Errorf("expected %s to be gone after RemoveAll, but Stat succeeded", p)
		}
	}
	// /tmp itself must survive with exactly its one remaining child.
	entries, err := fs.ReadDir("/tmp")
	if err != nil {
		t.Fatalf("ReadDir /tmp: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "sibling" {
		t.Errorf("expected /tmp to contain only 'sibling' after RemoveAll, got %v", entries)
	}
	siblingEntries, err := fs.ReadDir("/tmp/sibling")
	if err != nil {
		t.Fatalf("ReadDir /tmp/sibling: %v", err)
	}
	if len(siblingEntries) != 1 || siblingEntries[0].Name != "keep.txt" {
		t.Errorf("expected /tmp/sibling to still contain keep.txt, got %v", siblingEntries)
	}
}

func TestVFSEnv(t *testing.T) {
	fs := NewVFS()
	fs.Setenv("FOO", "bar")
	if fs.Getenv("FOO") != "bar" {
		t.Errorf("expected 'bar', got %q", fs.Getenv("FOO"))
	}
}

func TestVFSMountFSSnapshotsReadOnlyFiles(t *testing.T) {
	vfs := NewVFS()
	source := fstest.MapFS{
		"hello.txt":         &fstest.MapFile{Data: []byte("hello from embed")},
		"nested/config.txt": &fstest.MapFile{Data: []byte("enabled=true")},
	}
	if err := vfs.MountFS("/assets", source); err != nil {
		t.Fatalf("MountFS: %v", err)
	}
	if data, err := vfs.ReadFile("/assets/nested/config.txt"); err != nil || string(data) != "enabled=true" {
		t.Fatalf("mounted read = %q, %v", data, err)
	}
	if err := vfs.WriteFile("/assets/hello.txt", []byte("mutated"), 0644); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("mounted write error = %v, want read-only", err)
	}
	if err := vfs.RemoveAll("/assets"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("mounted remove error = %v, want read-only", err)
	}
}

func TestVFSImportDirAndReaderHaveBoundedSnapshots(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/nested", 0755); err != nil {
		t.Fatalf("prepare directory: %v", err)
	}
	if err := os.WriteFile(dir+"/nested/file.txt", []byte("from directory"), 0644); err != nil {
		t.Fatalf("prepare file: %v", err)
	}
	vfs := NewVFS()
	if err := vfs.ImportDir("/import", dir, VFSImportOptions{ReadOnly: true}); err != nil {
		t.Fatalf("ImportDir: %v", err)
	}
	if data, err := vfs.ReadFile("/import/nested/file.txt"); err != nil || string(data) != "from directory" {
		t.Fatalf("directory import read = %q, %v", data, err)
	}
	if err := vfs.ImportReader(context.Background(), "/input/request.txt", strings.NewReader("reader data"), 64); err != nil {
		t.Fatalf("ImportReader: %v", err)
	}
	if data, err := vfs.ReadFile("/input/request.txt"); err != nil || string(data) != "reader data" {
		t.Fatalf("reader import read = %q, %v", data, err)
	}
	if err := vfs.ImportReader(context.Background(), "/input/too-big.txt", strings.NewReader("1234"), 3); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("reader import limit error = %v, want limit", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := vfs.ImportReader(canceled, "/input/canceled.txt", strings.NewReader("never"), 64); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled reader import error = %v, want context.Canceled", err)
	}
}

func TestVFSImportFSHonorsConfiguredLimits(t *testing.T) {
	source := fstest.MapFS{
		"first.txt":  &fstest.MapFile{Data: []byte("one")},
		"second.txt": &fstest.MapFile{Data: []byte("two")},
	}
	if err := NewVFS().ImportFS("/assets", source, VFSImportOptions{MaxFiles: 1}); err == nil || !strings.Contains(err.Error(), "files") {
		t.Fatalf("file limit error = %v, want file limit", err)
	}
	if err := NewVFS().ImportFS("/assets", source, VFSImportOptions{MaxBytes: 3}); err == nil || !strings.Contains(err.Error(), "bytes") {
		t.Fatalf("byte limit error = %v, want byte limit", err)
	}
}

func TestMountedVFSUsesGuestFilesystemCapability(t *testing.T) {
	vfs := NewVFS()
	if err := vfs.MountFS("/assets", fstest.MapFS{"message.txt": &fstest.MapFile{Data: []byte("safe asset")}}); err != nil {
		t.Fatalf("MountFS: %v", err)
	}
	vm, out := newTestVM()
	vm.VFS = vfs
	RegisterBuiltinPackages(vm)
	vm.Capabilities = Capabilities{FileSystem: FileSystemCapabilities{Read: true, ReadPaths: []string{"/assets"}}}
	if err := vm.Run(`package main
import "fmt"
import "os"
func main() { text, _ := os.ReadFile("/assets/message.txt"); fmt.Println(text) }`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out.String(); got != "safe asset\n" {
		t.Fatalf("guest mounted file output = %q", got)
	}
}

// --------------- os package tests ---------------

func TestOsPackageReadWriteFile(t *testing.T) {
	out := runAndCapture(t, `
package main

import (
	"fmt"
	"os"
)

func main() {
	err := os.WriteFile("/tmp/wr.txt", "written content", 0644)
	if err != nil {
		fmt.Println("write error")
		return
	}
	content, err := os.ReadFile("/tmp/wr.txt")
	if err != nil {
		fmt.Println("read error")
		return
	}
	fmt.Println(content)
}
	`)
	if !strings.Contains(out, "written content") {
		t.Errorf("expected 'written content', got %q", out)
	}
}

func TestOsWriteFileAcceptsGuestByteSlice(t *testing.T) {
	out := runAndCapture(t, `
package main
import (
	"fmt"
	"os"
)
func main() {
	data := make([]byte, 4)
	data[0], data[1], data[2], data[3] = 'b', 'y', 't', 'e'
	if err := os.WriteFile("/tmp/bytes.txt", data, 0644); err != nil { panic(err) }
	text, err := os.ReadFile("/tmp/bytes.txt")
	if err != nil { panic(err) }
	fmt.Println(text)
}
`)
	if got := strings.TrimSpace(out); got != "byte" {
		t.Fatalf("os.WriteFile byte slice output = %q, want byte", got)
	}
}

func TestOsPackageMkdirAndReadDir(t *testing.T) {
	out := runAndCapture(t, `
package main

import (
	"fmt"
	"os"
)

func main() {
	err := os.MkdirAll("/tmp/testdir", 0755)
	if err != nil {
		fmt.Println("mkdir error")
		return
	}
	_ = os.WriteFile("/tmp/testdir/a.txt", "a", 0644)
	_ = os.WriteFile("/tmp/testdir/b.txt", "b", 0644)
	entries, err := os.ReadDir("/tmp/testdir")
	if err != nil {
		fmt.Println("readdir error")
		return
	}
	for _, e := range entries {
		fmt.Println(e.Name)
	}
}
`)
	if !strings.Contains(out, "a.txt") || !strings.Contains(out, "b.txt") {
		t.Errorf("expected 'a.txt' and 'b.txt', got %q", out)
	}
}

func TestOsPackageGetenv(t *testing.T) {
	out := runAndCapture(t, `
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Println(os.Getenv("HOME"))
	fmt.Println(os.TempDir())
}
`)
	if !strings.Contains(out, "/home/user") {
		t.Errorf("expected HOME=/home/user, got %q", out)
	}
	if !strings.Contains(out, "/tmp") {
		t.Errorf("expected TempDir=/tmp, got %q", out)
	}
}

func TestOsReadFileError(t *testing.T) {
	out := runAndCapture(t, `
package main

import (
	"fmt"
	"os"
)

func main() {
	_, err := os.ReadFile("/nonexistent/path.txt")
	if err != nil {
		fmt.Println("got error")
	} else {
		fmt.Println("no error")
	}
}
`)
	if !strings.Contains(out, "got error") {
		t.Errorf("expected error for missing file, got %q", out)
	}
}

func TestPathAndUTF8Packages(t *testing.T) {
	out := runAndCapture(t, `
package main

import (
	"fmt"
	"path"
	"unicode/utf8"
)

func main() {
	fmt.Println(path.Join("/api", "v1", "../users"))
	fmt.Println(path.Base("/api/users.json"), path.Ext("/api/users.json"), path.IsAbs("/api"))
	fmt.Println(utf8.RuneCountInString("Go✓"), utf8.RuneLen(0x2713), utf8.ValidString("Go✓"), utf8.ValidRune(0x2713))
}
`)
	if got, want := out, "/api/users\nusers.json .json true\n3 3 true true\n"; got != want {
		t.Fatalf("path/utf8 output = %q, want %q", got, want)
	}
}

func TestRegexpFindStringSubmatch(t *testing.T) {
	out := runAndCapture(t, `
package main
import (
    "fmt"
    "regexp"
)
func main() {
    re, err := regexp.Compile("(go)-(lang)")
    if err != nil { fmt.Println("compile failed"); return }
    values := re.FindStringSubmatch("go-lang")
    fmt.Println(len(values), values[0], values[1], values[2])
}`)
	if got, want := strings.TrimSpace(out), "3 go-lang go lang"; got != want {
		t.Fatalf("regexp submatches = %q, want %q", got, want)
	}
}

func TestGobAndTransportBridgePackages(t *testing.T) {
	vm, out := newTestVM()
	vm.RegisterInternalNative("ProtoMarshal", func(args []any) (any, error) {
		return "proto:" + ToString(args[0]), nil
	})
	vm.RegisterInternalNative("ProtoUnmarshal", func(args []any) (any, error) {
		return "decoded:" + ToString(args[0]), nil
	})
	vm.RegisterInternalNative("GRPCInvoke", func(args []any) (any, error) {
		return ToString(args[1]) + ":" + ToString(args[2]), nil
	})
	if err := vm.Run(`package main
import (
  "encoding/gob"
  "fmt"
  "grpc"
  "protobuf"
)
func main() {
  data, _ := gob.Encode("hello")
  value, _ := gob.Decode(data)
  fmt.Println(value)
  wire, _ := protobuf.Marshal("message")
  decoded, _ := protobuf.Unmarshal(wire)
  fmt.Println(decoded)
  response, _ := grpc.Invoke("https://example.com", "/echo.Service/Call", "body")
  fmt.Println(response)
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got, want := out.String(), "hello\ndecoded:proto:message\n/echo.Service/Call:body\n"; got != want {
		t.Fatalf("bridge output = %q, want %q", got, want)
	}
}

func TestMakeAnySliceKeepsNilElements(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	values := make([]any, 2)
	fmt.Println(values[0] == nil, values[1] == nil)
}`)
	if got, want := out, "true true\n"; got != want {
		t.Fatalf("make([]any) output = %q, want %q", got, want)
	}
}

// --------------- multi-return error capture tests ---------------

func TestMultiReturnErrorCapture(t *testing.T) {
	// Verify that val, err := pkg.Func() properly captures the error
	// as the second value rather than propagating it.
	out := runAndCapture(t, `
package main

import (
	"fmt"
	"strconv"
)

func main() {
	n, err := strconv.Atoi("42")
	if err != nil {
		fmt.Println("unexpected error")
		return
	}
	fmt.Println(n)
}
`)
	if !strings.Contains(out, "42") {
		t.Errorf("expected '42', got %q", out)
	}
}

func TestSharedVFSAcrossInterpreters(t *testing.T) {
	// Two interpreter instances sharing the same VFS.
	vfs := NewVFS()
	vm1 := NewInterpreterWithVFS(vfs)
	vm2 := NewInterpreterWithVFS(vfs)

	var buf strings.Builder
	registerTestNatives := func(vm *Interpreter) {
		vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
			if len(args) > 0 {
				buf.WriteString(ToString(args[0]))
				buf.WriteByte('\n')
			}
			return nil, nil
		})
		vm.RegisterNative("ConsoleWarn", func(args []any) (any, error) { return nil, nil })
		vm.RegisterNative("ConsoleError", func(args []any) (any, error) { return nil, nil })
		vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
			if len(args) == 0 {
				return "", nil
			}
			fmtArgs := make([]any, 0, len(args)-1)
			for _, a := range args[1:] {
				fmtArgs = append(fmtArgs, a)
			}
			return fmt.Sprintf(ToString(args[0]), fmtArgs...), nil
		})
		RegisterBuiltinPackages(vm)
	}

	registerTestNatives(vm1)
	registerTestNatives(vm2)
	vm1.Capabilities = FullCapabilities()
	vm2.Capabilities = FullCapabilities()

	// vm1 writes a file.
	src1 := "package main\nimport \"os\"\nfunc main() { _ = os.WriteFile(\"/tmp/shared.txt\", \"shared data\", 0644) }\n"
	if err := vm1.Run(src1); err != nil {
		t.Fatalf("vm1 Run: %v", err)
	}
	// vm2 reads the file written by vm1.
	src2 := "package main\nimport (\"fmt\";\"os\")\nfunc main() { content, _ := os.ReadFile(\"/tmp/shared.txt\"); fmt.Println(content) }\n"
	if err := vm2.Run(src2); err != nil {
		t.Fatalf("vm2 Run: %v", err)
	}
	if !strings.Contains(buf.String(), "shared data") {
		t.Errorf("expected 'shared data' read by vm2, got %q", buf.String())
	}
}

func TestParseFastDecimalInt(t *testing.T) {
	tests := []struct {
		literal string
		want    int
		ok      bool
	}{
		{literal: "0", want: 0, ok: true},
		{literal: "7", want: 7, ok: true},
		{literal: "100000", want: 100000, ok: true},
		// These forms intentionally fall back to strconv so their Go literal
		// semantics and diagnostics remain unchanged.
		{literal: "012", ok: false},
		{literal: "0x10", ok: false},
		{literal: "1_000", ok: false},
		{literal: "999999999999999999999999999999", ok: false},
	}
	for _, tt := range tests {
		got, ok := parseFastDecimalInt(tt.literal)
		if got != tt.want || ok != tt.ok {
			t.Errorf("parseFastDecimalInt(%q) = (%d, %t), want (%d, %t)", tt.literal, got, ok, tt.want, tt.ok)
		}
	}
}

func TestGoAndTinyGoScalarAndRangeCompatibility(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"

func main() {
	var zero uint16
	b := byte(260)
	i8 := int8(130)
	f := float32(1.25)
	fmt.Println(zero, b, i8, f)

	sum := 0
	for i := range 3 {
		sum = sum*10 + i
	}
	fmt.Println(sum)

	for offset, r := range "aä" {
		fmt.Println(offset, r)
	}
	fmt.Println(string([]uint8{104, 105}))
}
`)
	for _, want := range []string{
		"0 4 -126 1.25",
		"12",
		"0 97",
		"1 228",
		"hi",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
}

func TestFixedByteArraysForTinyGoStyleBuffers(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"

func main() {
	var buffer [4]byte
	buffer[0] = 'o'
	buffer[1] = 'k'
	packet := [4]byte{0: 'N', 2: 'G'}
	fmt.Println(len(buffer), cap(buffer), string(buffer[:2]))
	fmt.Println(packet[0], packet[1], packet[2], packet[3])
}
`)
	for _, want := range []string{"4 4 ok", "78 0 71 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}

	vm, _ := newTestVM()
	err := vm.Run(`package main
func main() { a := [1]byte{1}; append(a, 2) }`)
	if err == nil || !strings.Contains(err.Error(), "first argument must be a slice") {
		t.Fatalf("append(array) error = %v, want slice-only error", err)
	}
}

func TestSwitchBreakAndFallthroughCompatibility(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"

func classify(n int) string {
	switch n {
	case 0:
		fallthrough
	case 1:
		return "low"
	default:
		return "high"
	}
}

func main() {
	fmt.Println(classify(0), classify(2))
	for i := 0; i < 2; i++ {
		switch i {
		case 0:
			break
		}
		fmt.Println(i)
	}
}
`)
	for _, want := range []string{"low high", "0", "1"} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q does not contain %q", out, want)
		}
	}
}

func TestNamedScalarTypesForTinyGoStyleCode(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"

type Pin uint8
type Millis = uint16

func main() {
	var p Pin
	var wait Millis
	p = Pin(260)
	wait = Millis(70000)
	levels := make([]Pin, 2)
	fmt.Println(p, wait, levels[0], levels[1])
}
`)
	if !strings.Contains(out, "4 4464 0 0") {
		t.Errorf("output %q does not contain named scalar values", out)
	}
}

func TestRunContextStopsBusyLoopAtDeadline(t *testing.T) {
	vm, _ := newTestVM()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err := vm.RunContext(ctx, `package main
func main() { for { } }`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunContext error = %v, want deadline exceeded", err)
	}
}

func TestRunContextInterruptsBlockingGuestChannel(t *testing.T) {
	vm, _ := newTestVM()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err := vm.RunContext(ctx, `package main
func main() { ch := make(chan int); <-ch }`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunContext error = %v, want deadline exceeded", err)
	}
}

func TestRunContextInterruptsEmptySelect(t *testing.T) {
	vm, _ := newTestVM()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err := vm.RunContext(ctx, `package main
func main() { select {} }`)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("RunContext error = %v, want deadline exceeded", err)
	}
}

func TestTimerRejectsDurationOverflowBeforeStartingWorker(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`package main
import "time"
func main() { time.NewTicker(9223372036854775807) }`)
	if err == nil || !strings.Contains(err.Error(), "duration exceeds maximum") {
		t.Fatalf("Run error = %v, want duration-overflow failure", err)
	}
}

func TestRunStopsBackgroundWorkerWhenMainReturns(t *testing.T) {
	vm, _ := newTestVM()
	// Disable limits so this test proves root teardown stops an evaluator-only
	// busy loop rather than eventually relying on the step limit.
	vm.Limits = ExecutionLimits{}
	done := make(chan error, 1)
	go func() {
		done <- vm.Run(`package main
func main() {
    go func() { for { } }()
}`)
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not cancel the background guest goroutine")
	}
}

func TestGuestGoroutineFailureCancelsAndPropagates(t *testing.T) {
	vm, _ := newTestVM()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := vm.RunContext(ctx, `package main
func main() {
    started := make(chan int)
    block := make(chan int)
    go func() {
        started <- 1
        panic("worker boom")
    }()
    <-started
    <-block
}`)
	if err == nil || !strings.Contains(err.Error(), "worker boom") {
		t.Fatalf("RunContext error = %v, want worker panic", err)
	}
}

func TestKillStopsRunningProgram(t *testing.T) {
	vm, _ := newTestVM()
	done := make(chan error, 1)
	go func() {
		done <- vm.RunContext(context.Background(), `package main
func main() { ch := make(chan int); <-ch }`)
	}()

	deadline := time.After(time.Second)
	for !vm.IsRunning() {
		select {
		case <-deadline:
			t.Fatal("interpreter did not start")
		case <-time.After(time.Millisecond):
		}
	}
	if !vm.Kill() {
		t.Fatal("Kill returned false while program was running")
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrKilled) {
			t.Fatalf("RunContext error = %v, want ErrKilled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Kill did not unblock RunContext")
	}
}

func TestHostChannelBridgeRoundTrip(t *testing.T) {
	vm, _ := newTestVM()
	bridge := NewHostChannel(1)
	if err := vm.BindHostChannel("hostIn", "hostOut", bridge); err != nil {
		t.Fatalf("BindHostChannel: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- vm.RunContext(context.Background(), `package main
func main() {
    request := <-hostIn
    hostOut <- "echo: " + request
}`)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bridge.Send(ctx, "ping"); err != nil {
		t.Fatalf("bridge.Send: %v", err)
	}
	response, err := bridge.Receive(ctx)
	if err != nil {
		t.Fatalf("bridge.Receive: %v", err)
	}
	if response != "echo: ping" {
		t.Fatalf("bridge response = %#v, want echo", response)
	}
	if err := <-done; err != nil {
		t.Fatalf("RunContext: %v", err)
	}
}

func TestHostChannelIsDirectionallyProtected(t *testing.T) {
	vm, _ := newTestVM()
	bridge := NewHostChannel(1)
	if err := vm.BindHostChannel("hostIn", "hostOut", bridge); err != nil {
		t.Fatalf("BindHostChannel: %v", err)
	}
	err := vm.Run(`package main
func main() { hostIn <- "forbidden" }`)
	if err == nil || !strings.Contains(err.Error(), "receive-only host channel") {
		t.Fatalf("Run error = %v, want protected input failure", err)
	}
}

func TestHostChannelInputCloseLooksClosedToGuest(t *testing.T) {
	vm, _ := newTestVM()
	bridge := NewHostChannel(1)
	if err := vm.BindHostChannel("hostIn", "hostOut", bridge); err != nil {
		t.Fatalf("BindHostChannel: %v", err)
	}
	bridge.CloseInput()
	if err := vm.Run(`package main
func main() {
    _, open := <-hostIn
    if !open { hostOut <- "closed" }
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	value, err := bridge.Receive(ctx)
	if err != nil {
		t.Fatalf("bridge.Receive: %v", err)
	}
	if value != "closed" {
		t.Fatalf("bridge close response = %#v, want closed", value)
	}
}

func TestHostChannelRejectsSendAfterInputClose(t *testing.T) {
	bridge := NewHostChannel(1)
	bridge.CloseInput()
	if err := bridge.Send(context.Background(), "ignored"); !errors.Is(err, ErrHostChannelClosed) {
		t.Fatalf("bridge.Send after CloseInput = %v, want ErrHostChannelClosed", err)
	}
}

func TestHostChannelRejectsGuestSendAfterOutputClose(t *testing.T) {
	vm, _ := newTestVM()
	bridge := NewHostChannel(1)
	if err := vm.BindHostChannel("hostIn", "hostOut", bridge); err != nil {
		t.Fatalf("BindHostChannel: %v", err)
	}
	bridge.CloseOutput()
	err := vm.Run(`package main
func main() { hostOut <- "ignored" }`)
	if err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("Run error = %v, want closed-output failure", err)
	}
}

func TestHostChannelSelectDrainsBufferedInputBeforeClose(t *testing.T) {
	vm, _ := newTestVM()
	bridge := NewHostChannel(1)
	if err := vm.BindHostChannel("hostIn", "hostOut", bridge); err != nil {
		t.Fatalf("BindHostChannel: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := bridge.Send(ctx, "queued"); err != nil {
		t.Fatalf("bridge.Send: %v", err)
	}
	bridge.CloseInput()
	if err := vm.Run(`package main
func main() {
    select {
    case value, open := <-hostIn:
        if open { hostOut <- value } else { hostOut <- "closed" }
    }
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	value, err := bridge.Receive(ctx)
	if err != nil {
		t.Fatalf("bridge.Receive: %v", err)
	}
	if value != "queued" {
		t.Fatalf("select received %#v, want queued buffered value", value)
	}
}

func TestHostChannelOutputCloseDrainsBufferedMessages(t *testing.T) {
	vm, _ := newTestVM()
	bridge := NewHostChannel(2)
	if err := vm.BindHostChannel("hostIn", "hostOut", bridge); err != nil {
		t.Fatalf("BindHostChannel: %v", err)
	}
	if err := vm.Run(`package main
func main() {
    hostOut <- "first"
    hostOut <- "second"
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	bridge.CloseOutput()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for _, want := range []string{"first", "second"} {
		value, err := bridge.Receive(ctx)
		if err != nil {
			t.Fatalf("bridge.Receive: %v", err)
		}
		if value != want {
			t.Fatalf("bridge output = %#v, want %q", value, want)
		}
	}
	if _, err := bridge.Receive(ctx); !errors.Is(err, ErrHostChannelClosed) {
		t.Fatalf("bridge.Receive after drain = %v, want ErrHostChannelClosed", err)
	}
}

type hostContextTestKey string

func TestBindHostContextExposesOnlySelectedCopiedValues(t *testing.T) {
	const requestIDKey hostContextTestKey = "request-id"
	const flagsKey hostContextTestKey = "flags"
	const secretKey hostContextTestKey = "secret"
	flags := map[string]any{"beta": true}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ctx = context.WithValue(ctx, requestIDKey, "req-42")
	ctx = context.WithValue(ctx, flagsKey, flags)
	ctx = context.WithValue(ctx, secretKey, "must-not-leak")
	snapshot, err := ContextSnapshot(ctx, ContextField{Name: "requestID", Key: requestIDKey})
	if err != nil {
		t.Fatalf("ContextSnapshot: %v", err)
	}
	if !snapshot["hasDeadline"].(bool) || snapshot["deadlineUnixMilli"].(int64) <= time.Now().UnixMilli() {
		t.Fatalf("snapshot deadline = %#v, want a future deadline", snapshot)
	}

	vm, out := newTestVM()
	if err := vm.BindHostContext("hostContext", ctx,
		ContextField{Name: "requestID", Key: requestIDKey},
		ContextField{Name: "flags", Key: flagsKey},
	); err != nil {
		t.Fatalf("BindHostContext: %v", err)
	}
	// Binding is a snapshot. A host mutation after it returns is invisible to
	// the guest program.
	flags["beta"] = false

	err = vm.RunContext(ctx, `package main
import "fmt"
func main() {
    values := hostContext["values"]
    fmt.Println(values["requestID"])
    fmt.Println(values["flags"]["beta"])
    fmt.Println(hostContext["hasDeadline"])
    if values["secret"] != nil { panic("secret leaked") }
}`)
	if err != nil {
		t.Fatalf("RunContext: %v", err)
	}
	if got, want := out.String(), "req-42\ntrue\ntrue\n"; got != want {
		t.Fatalf("guest context output = %q, want %q", got, want)
	}
}

func TestContextSnapshotRejectsUnsafeValuesAndFields(t *testing.T) {
	const valueKey hostContextTestKey = "value"
	ctx := context.WithValue(context.Background(), valueKey, make(chan int))
	if _, err := ContextSnapshot(ctx, ContextField{Name: "value", Key: valueKey}); err == nil || !strings.Contains(err.Error(), "unsupported host channel value") {
		t.Fatalf("unsafe context value error = %v, want bridge rejection", err)
	}
	if _, err := ContextSnapshot(context.Background(), ContextField{Name: "", Key: valueKey}); err == nil {
		t.Fatal("empty context field name was accepted")
	}
	if _, err := ContextSnapshot(context.Background(), ContextField{Name: "bad", Key: []string{"not", "comparable"}}); err == nil || !strings.Contains(err.Error(), "non-comparable") {
		t.Fatalf("non-comparable context key error = %v", err)
	}
}

func TestMakeRejectsUnsafeSizes(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`package main
func main() { _ = make([]int, -1) }`)
	if err == nil || !strings.Contains(err.Error(), "negative size") {
		t.Fatalf("Run error = %v, want negative size failure", err)
	}
	err = vm.Run(`package main
func main() { _ = make(chan int, 2000000) }`)
	if err == nil || !strings.Contains(err.Error(), "interpreter limit") {
		t.Fatalf("Run error = %v, want container-limit failure", err)
	}
}

func TestExecutionStepLimitStopsBusyLoop(t *testing.T) {
	vm, _ := newTestVM()
	vm.Limits = ExecutionLimits{MaxSteps: 50}
	err := vm.Run(`package main
func main() { for { } }`)
	if !errors.Is(err, ErrStepLimit) {
		t.Fatalf("Run error = %v, want ErrStepLimit", err)
	}
}

func TestExecutionGoroutineLimitRejectsExcessGuests(t *testing.T) {
	vm, _ := newTestVM()
	vm.Limits = ExecutionLimits{MaxGoroutines: 1}
	err := vm.Run(`package main
func main() {
    block := make(chan int)
    go func() { <-block }()
    go func() { <-block }()
}`)
	if !errors.Is(err, ErrGoroutineLimit) {
		t.Fatalf("Run error = %v, want ErrGoroutineLimit", err)
	}
}

func TestExecutionRestoresSingleGoroutineFastPath(t *testing.T) {
	exec := &execution{limits: ExecutionLimits{}}
	if err := exec.reserveGoroutine(); err != nil {
		t.Fatalf("reserveGoroutine: %v", err)
	}
	if !exec.concurrent.Load() {
		t.Fatal("concurrent flag = false while worker is active")
	}
	exec.releaseGoroutine()
	if exec.concurrent.Load() {
		t.Fatal("concurrent flag stayed true after the final worker returned")
	}
}

func TestCapabilitiesDenyFilesystemAndNetworkByDefault(t *testing.T) {
	vm := NewInterpreter()
	RegisterBuiltinPackages(vm)
	vm.RegisterNative("HostReadFile", func([]any) (any, error) {
		t.Fatal("host read native must not be called when filesystem is denied")
		return "", nil
	})
	vm.RegisterNative("HTTPGetText", func([]any) (any, error) {
		t.Fatal("HTTP native must not be called when network is denied")
		return "", nil
	})

	err := vm.Run(`package main
import "fs"
func main() { fs.ReadFile("secret.txt") }`)
	if err == nil || !strings.Contains(err.Error(), "filesystem read denied") {
		t.Fatalf("filesystem error = %v, want denied", err)
	}
	err = vm.Run(`package main
import "http"
func main() { http.GetText("https://api.example.com/data") }`)
	if err == nil || !strings.Contains(err.Error(), "network access denied") {
		t.Fatalf("network error = %v, want denied", err)
	}
}

func TestCapabilitiesAllowOnlyConfiguredHosts(t *testing.T) {
	vm := NewInterpreter()
	vm.Capabilities = Capabilities{Network: NetworkCapabilities{
		HTTP:         true,
		AllowedHosts: []string{"api.example.com", "*.trusted.test"},
	}}
	vm.RegisterNative("HTTPGetText", func(args []any) (any, error) {
		return "ok:" + ToString(args[0]), nil
	})
	RegisterBuiltinPackages(vm)

	err := vm.Run(`package main
import "http"
func main() { http.GetText("https://api.example.com/v1") }`)
	if err != nil {
		t.Fatalf("allowed host error = %v", err)
	}
	err = vm.Run(`package main
import "http"
func main() { http.GetText("https://evil.example.com/") }`)
	if err == nil || !strings.Contains(err.Error(), "host evil.example.com") {
		t.Fatalf("disallowed host error = %v, want denied host", err)
	}
	err = vm.Run(`package main
import "http"
func main() { http.GetText("http://127.0.0.1:8080/") }`)
	if err == nil || !strings.Contains(err.Error(), "host 127.0.0.1") {
		t.Fatalf("private host error = %v, want denied host", err)
	}
}

func TestCapabilitiesRestrictFilesystemPaths(t *testing.T) {
	vm := NewInterpreter()
	vm.Capabilities = Capabilities{FileSystem: FileSystemCapabilities{
		Read:       true,
		Write:      true,
		ReadPaths:  []string{"/home/user/sandbox"},
		WritePaths: []string{"/home/user/sandbox"},
	}}
	if err := vm.VFS.Mkdir("/home/user/sandbox", 0755); err != nil {
		t.Fatalf("prepare VFS: %v", err)
	}
	RegisterBuiltinPackages(vm)

	if err := vm.Run(`package main
import "os"
func main() { os.WriteFile("sandbox/note.txt", "ok", 0644) }`); err != nil {
		t.Fatalf("allowed write: %v", err)
	}
	if got, err := vm.VFS.ReadFile("/home/user/sandbox/note.txt"); err != nil || string(got) != "ok" {
		t.Fatalf("allowed write result = %q, %v", got, err)
	}
	if err := vm.Run(`package main
import "os"
func main() { os.WriteFile("../escape.txt", "no", 0644) }`); err == nil || !strings.Contains(err.Error(), "filesystem write denied") {
		t.Fatalf("escaped write error = %v, want denied", err)
	}
	if err := vm.Run(`package main
import "os"
func main() { os.ReadDir("/") }`); err == nil || !strings.Contains(err.Error(), "filesystem read denied") {
		t.Fatalf("root directory read error = %v, want denied", err)
	}
}

func TestFSReadFilePassesCanonicalAuthorizedPathToHost(t *testing.T) {
	vm := NewInterpreter()
	vm.Capabilities = Capabilities{FileSystem: FileSystemCapabilities{
		Read:      true,
		ReadPaths: []string{"/home/user/sandbox"},
	}}
	vm.RegisterNative("HostReadFile", func(args []any) (any, error) {
		if len(args) != 1 || ToString(args[0]) != "/home/user/sandbox/note.txt" {
			t.Fatalf("HostReadFile path = %#v, want canonical allowed path", args)
		}
		return "ok", nil
	})
	RegisterBuiltinPackages(vm)
	if err := vm.Run(`package main
import "fs"
func main() { fs.ReadFile("sandbox/../sandbox/note.txt") }`); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestTracerCapturesQStyleDebugEvents(t *testing.T) {
	vm, _ := newTestVM()
	tracer := NewTracer(16)
	vm.SetTracer(tracer)
	if err := vm.Run(`package main
import "debug"
func main() {
    total := 40 + 2
    debug.Q(total, total*2)
    debug.Mark("after calculation")
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := tracer.Events()
	var qEvent, markEvent *TraceEvent
	for i := range events {
		switch events[i].Kind {
		case "debug_q":
			qEvent = &events[i]
		case "debug_mark":
			markEvent = &events[i]
		}
	}
	if qEvent == nil || !strings.Contains(qEvent.Message, "total = 42") || !strings.Contains(qEvent.Message, "total * 2 = 84") {
		t.Fatalf("missing q-style values in events: %#v", events)
	}
	if markEvent == nil || markEvent.Message != "after calculation" {
		t.Fatalf("missing debug marker in events: %#v", events)
	}
	if qEvent.Location.Line == 0 || qEvent.Sequence >= markEvent.Sequence {
		t.Fatalf("trace events are missing source order: q=%#v mark=%#v", qEvent, markEvent)
	}
}

func TestDebugQWithoutObserverStillEvaluatesArguments(t *testing.T) {
	vm, output := newTestVM()
	if err := vm.Run(`package main
import (
    "debug"
    "fmt"
)
var calls int
func next() int {
    calls++
    return calls
}
func main() {
    debug.Q(next())
    fmt.Println(calls)
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := output.String(); got != "1\n" {
		t.Fatalf("debug.Q skipped argument evaluation: output = %q, want %q", got, "1\\n")
	}
}

func TestTracerCapturesConfiguredBreakpointHits(t *testing.T) {
	vm, _ := newTestVM()
	tracer := NewTracer(16)
	vm.SetTracer(tracer)
	vm.SetBreakpoints([]int{5, 5, 0, -1})
	if got := vm.Breakpoints(); len(got) != 1 || got[0] != 5 {
		t.Fatalf("Breakpoints() = %#v, want [5]", got)
	}
	if err := vm.Run(`package main
func main() {
    value := 40
    value++
    value++
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var hits []TraceEvent
	for _, event := range tracer.Events() {
		if event.Kind == "breakpoint" {
			hits = append(hits, event)
		}
	}
	if len(hits) != 1 || hits[0].Function != "main" || hits[0].Location.Line != 5 {
		t.Fatalf("breakpoint events = %#v, want one main event at line 5", hits)
	}
	vm.SetBreakpoints([]int{})
	if got := vm.Breakpoints(); got != nil {
		t.Fatalf("cleared breakpoints = %v, want nil", got)
	}
}

func TestTracerUsesBoundedChronologicalRing(t *testing.T) {
	tracer := NewTracer(2)
	tracer.record(TraceEvent{Kind: "one"})
	tracer.record(TraceEvent{Kind: "two"})
	tracer.record(TraceEvent{Kind: "three"})
	events := tracer.Events()
	if len(events) != 2 || events[0].Kind != "two" || events[1].Kind != "three" {
		t.Fatalf("ring events = %#v", events)
	}
}

func TestTracerRecordsGuestGoroutineLifecycle(t *testing.T) {
	vm, _ := newTestVM()
	tracer := NewTracer(16)
	vm.SetTracer(tracer)
	if err := vm.Run(`package main
import "debug"
func main() {
    done := make(chan int)
    go func() {
        debug.Q("worker")
        done <- 1
    }()
    <-done
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	events := tracer.Events()
	seenStart, seenEnd, seenQ := false, false, false
	for _, event := range events {
		switch event.Kind {
		case "goroutine_start":
			seenStart = true
		case "goroutine_end":
			seenEnd = true
		case "debug_q":
			seenQ = true
		}
	}
	if !seenStart || !seenEnd || !seenQ {
		t.Fatalf("incomplete guest-goroutine trace: %#v", events)
	}
}

func TestDebugStackReturnsInnermostFirstCallChain(t *testing.T) {
	vm, buf := newTestVM()
	if err := vm.Run(`package main
import (
	"fmt"
	"debug"
)
func inner() string { return debug.Stack() }
func outer() string { return inner() }
func main() {
	fmt.Println(outer())
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	innerIdx := strings.Index(out, "inner")
	outerIdx := strings.Index(out, "outer")
	mainIdx := strings.Index(out, "main")
	if innerIdx == -1 || outerIdx == -1 || mainIdx == -1 {
		t.Fatalf("expected inner/outer/main in stack, got %q", out)
	}
	if !(innerIdx < outerIdx && outerIdx < mainIdx) {
		t.Fatalf("expected inner before outer before main (innermost first), got %q", out)
	}
}

func TestDebugStackAtTopLevelHasNoCaller(t *testing.T) {
	vm, buf := newTestVM()
	if err := vm.Run(`package main
import (
	"fmt"
	"debug"
)
func main() {
	fmt.Println(debug.Stack())
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "#0 main") {
		t.Fatalf("expected '#0 main' as the only frame, got %q", out)
	}
	if strings.Count(out, "#") != 1 {
		t.Fatalf("expected exactly one frame at top level, got %q", out)
	}
}

func TestDebugStackAcrossGoroutineLinksBackToLaunchSite(t *testing.T) {
	vm, buf := newTestVM()
	if err := vm.Run(`package main
import (
	"fmt"
	"debug"
)
func worker() string { return debug.Stack() }
func launch(done chan string) { done <- worker() }
func main() {
	done := make(chan string)
	go launch(done)
	fmt.Println(<-done)
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	workerIdx := strings.Index(out, "worker")
	launchIdx := strings.Index(out, "launch")
	if workerIdx == -1 || launchIdx == -1 || !(workerIdx < launchIdx) {
		t.Fatalf("expected worker's stack to chain back through its launching call, got %q", out)
	}
}

func TestDebugVarsReflectsLocalScopeAndShadowing(t *testing.T) {
	vm, buf := newTestVM()
	if err := vm.Run(`package main
import (
	"fmt"
	"debug"
)
func main() {
	x := 1
	y := "hello"
	{
		x := 99
		fmt.Println(debug.Vars())
		_ = x
	}
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "x = 99") {
		t.Fatalf("expected shadowed inner x = 99, got %q", out)
	}
	if strings.Contains(out, "x = 1") {
		t.Fatalf("expected outer x to be shadowed, not both visible, got %q", out)
	}
	if !strings.Contains(out, `y = "hello"`) {
		t.Fatalf("expected outer y to still be visible, got %q", out)
	}
}

func TestDebugVarsAtGlobalScopeIsEmpty(t *testing.T) {
	vm, buf := newTestVM()
	if err := vm.Run(`package main
import (
	"fmt"
	"debug"
)
func main() {
	fmt.Println(debug.Vars())
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(buf.String(), "<no local variables>") {
		t.Fatalf("expected no local variables at top of main, got %q", buf.String())
	}
}

func TestDebugAssertPassesSilently(t *testing.T) {
	vm, _ := newTestVM()
	if err := vm.Run(`package main
import "debug"
func main() {
	debug.Assert(1+1 == 2)
}`); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestDebugAssertFailureCarriesMessageAndTrace(t *testing.T) {
	vm, _ := newTestVM()
	tracer := NewTracer(16)
	vm.SetTracer(tracer)
	err := vm.Run(`package main
import "debug"
func main() {
	debug.Assert(1 == 2, "math is broken", 42)
}`)
	if err == nil {
		t.Fatal("expected an error from a failing assertion")
	}
	if !strings.Contains(err.Error(), "math is broken") || !strings.Contains(err.Error(), "42") {
		t.Fatalf("expected assertion message in error, got %v", err)
	}
	var sawFail bool
	for _, event := range tracer.Events() {
		if event.Kind == "debug_assert_fail" && strings.Contains(event.Message, "math is broken") {
			sawFail = true
		}
	}
	if !sawFail {
		t.Fatalf("expected debug_assert_fail trace event, got %#v", tracer.Events())
	}
}

func TestRuntimeErrorCarriesSourceLocation(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`package main

func main() {
	x := 1
	undefinedFunc(x)
}
`)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	re, ok := err.(*RuntimeError)
	if !ok {
		t.Fatalf("error type = %T, want *RuntimeError", err)
	}
	if re.Loc.Line != 5 || re.Loc.Column != 2 {
		t.Errorf("Loc = %+v, want line 5, column 2", re.Loc)
	}
	if got := err.Error(); got != "5:2: undefined: undefinedFunc" {
		t.Errorf("Error() = %q, want %q", got, "5:2: undefined: undefinedFunc")
	}
}

func TestRuntimeErrorLocationPinsInnermostFailure(t *testing.T) {
	// The failing identifier is nested three expressions deep (call args,
	// inside a binary expression, inside a call) — the reported position
	// must stay pinned to the identifier itself, not drift out to any of
	// the enclosing expressions as the error bubbles up through their own
	// evalExpr calls (see evalExpr's first-write-wins doc comment).
	vm, _ := newTestVM()
	err := vm.Run(`package main

import "fmt"

func main() {
	fmt.Println(1 + missingVar)
}
`)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	re, ok := err.(*RuntimeError)
	if !ok {
		t.Fatalf("error type = %T, want *RuntimeError", err)
	}
	if re.Loc.Line != 6 {
		t.Errorf("Loc.Line = %d, want 6", re.Loc.Line)
	}
	// "missingVar" starts after "\tfmt.Println(1 + " — column 18 (the
	// leading tab counts as one column, like the rest of go/token).
	if re.Loc.Column != 18 {
		t.Errorf("Loc.Column = %d, want 18 (pinned to missingVar, not the enclosing call/binary expr)", re.Loc.Column)
	}
}

func TestRuntimeErrorWithoutLocationHasNoPrefix(t *testing.T) {
	// NewRuntimeError, used directly by hosts/builtins outside any active
	// evalExpr/evalStmt call, must keep working exactly as before: no
	// location was ever attached, so Error() falls back to the bare message.
	err := NewRuntimeError("some host-side failure")
	if got, want := err.Error(), "some host-side failure"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestLineProfileCountsStatementHitsPerLine(t *testing.T) {
	vm, _ := newTestVM()
	profile := NewLineProfile()
	vm.SetLineProfile(profile)

	err := vm.Run(`package main

import "fmt"

func main() {
	total := 0
	for i := 0; i < 5; i++ {
		total += i
	}
	fmt.Println(total)
}
`)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	counts := profile.Counts()
	// The loop body (line 8) runs once per iteration; the for statement
	// itself (line 7) evaluates its init/cond/post each iteration too, so
	// it should show at least as many hits as the body.
	if counts[8] != 5 {
		t.Errorf("counts[8] (loop body) = %d, want 5", counts[8])
	}
	if counts[6] != 1 {
		t.Errorf("counts[6] (total := 0) = %d, want 1", counts[6])
	}
	if counts[10] != 1 {
		t.Errorf("counts[10] (fmt.Println) = %d, want 1", counts[10])
	}
	if counts[7] < 5 {
		t.Errorf("counts[7] (for statement) = %d, want >= 5", counts[7])
	}
}

func TestLineProfileNilIsSafeNoOp(t *testing.T) {
	vm, _ := newTestVM()
	// No SetLineProfile call at all: must behave exactly as before.
	if err := vm.Run(`package main
func main() { x := 1; _ = x }`); err != nil {
		t.Fatalf("Run without a profiler: %v", err)
	}

	var nilProfile *LineProfile
	nilProfile.hit(5) // must not panic
	if got := nilProfile.Counts(); got != nil {
		t.Errorf("nil profile Counts() = %v, want nil", got)
	}
	nilProfile.Reset() // must not panic
}

func TestLineProfileAccumulatesAcrossMultipleRuns(t *testing.T) {
	vm, _ := newTestVM()
	profile := NewLineProfile()
	vm.SetLineProfile(profile)
	src := `package main
func main() {
	x := 1
	_ = x
}`
	for i := 0; i < 3; i++ {
		if err := vm.Run(src); err != nil {
			t.Fatalf("Run #%d: %v", i, err)
		}
	}
	counts := profile.Counts()
	if counts[3] != 3 {
		t.Errorf("counts[3] after 3 runs = %d, want 3 (profile should accumulate, like a shared benchmark profile)", counts[3])
	}

	profile.Reset()
	if len(profile.Counts()) != 0 {
		t.Error("Counts() not empty after Reset()")
	}
}

// TestLoopBodyClosuresCapturePerIterationVariable guards the
// blockNeedsOwnScope optimization (evaluator.go's BlockStmt case): a block
// that only assigns to outer variables reuses its parent's environment
// instead of allocating its own, but a block that DOES declare a local
// (here, x := i * 10) must still fork a fresh scope every iteration so each
// closure captures its own independent binding rather than one shared
// variable that ends up holding only the loop's final value.
func TestLoopBodyClosuresCapturePerIterationVariable(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	var funcs []func() int
	for i := 0; i < 3; i++ {
		x := i * 10
		funcs = append(funcs, func() int { return x })
	}
	for _, f := range funcs {
		fmt.Println(f())
	}
}
`)
	want := "0\n10\n20\n"
	if out != want {
		t.Fatalf("closures over per-iteration block locals = %q, want %q", out, want)
	}
}

func TestForHeaderClosurePreventsScopeReuse(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	var saved func() int
	for i := 0; func() bool {
		saved = func() int { return i }
		return i < 1
	}(); i++ {}
	for j := 0; j < 5; j++ {}
	fmt.Println(saved())
}
`)
	if out != "1\n" {
		t.Fatalf("closure over for scope after later loop = %q, want %q", out, "1\n")
	}
}

func TestBridgePreservesCommonToolContainerTypes(t *testing.T) {
	cases := []struct {
		input any
		want  any
	}{
		{[]byte{1, 2, 3}, []byte{1, 2, 3}},
		{[]int{4, 5}, []int{4, 5}},
		{[]string{"a", "b"}, []string{"a", "b"}},
		{[]float64{1.5}, []float64{1.5}},
		{[]bool{true, false}, []bool{true, false}},
		{map[string]string{"kind": "tool"}, map[string]any{"kind": "tool"}},
		{map[string]int{"count": 3}, map[string]any{"count": 3}},
	}
	for _, tc := range cases {
		guest, err := BridgeToGuest(tc.input)
		if err != nil {
			t.Fatalf("BridgeToGuest(%T): %v", tc.input, err)
		}
		host, err := BridgeToHost(guest)
		if err != nil {
			t.Fatalf("BridgeToHost(%T): %v", guest, err)
		}
		if !reflect.DeepEqual(host, tc.want) {
			t.Errorf("round trip %T = %#v (%T), want %#v (%T)", tc.input, host, host, tc.want, tc.want)
		}
	}
}
