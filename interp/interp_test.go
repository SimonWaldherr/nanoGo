package interp

import (
	"fmt"
	"strings"
	"testing"
)

func newTestVM() (*Interpreter, *strings.Builder) {
	vm := NewInterpreter()
	var buf strings.Builder

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
	parent := NewEnv(nil)
	parent.Vars["x"] = 10
	child := NewEnv(parent)
	child.Vars["y"] = 20

	vm := NewInterpreter()

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
}
