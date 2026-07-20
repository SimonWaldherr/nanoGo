package interp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
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

func TestVFSEnv(t *testing.T) {
	fs := NewVFS()
	fs.Setenv("FOO", "bar")
	if fs.Getenv("FOO") != "bar" {
		t.Errorf("expected 'bar', got %q", fs.Getenv("FOO"))
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
