package interp

import (
	"strings"
	"testing"
)

func TestFormatSource(t *testing.T) {
	src := `package main
import "fmt"
func main(){fmt.Println("hello")}
`
	formatted, err := FormatSource(src)
	if err != nil {
		t.Fatalf("FormatSource returned error: %v", err)
	}
	// gofmt expands the body onto separate lines.
	if !strings.Contains(formatted, "func main() {") {
		t.Errorf("expected formatted output, got %q", formatted)
	}
}

func TestFormatSourceInvalidCode(t *testing.T) {
	src := `this is not valid go code`
	_, err := FormatSource(src)
	if err == nil {
		t.Error("expected error for invalid source, got nil")
	}
}

func TestVetSourceClean(t *testing.T) {
	src := `package main
import "fmt"
func main() {
	fmt.Println("hello")
}
`
	issues, err := VetSource(src)
	if err != nil {
		t.Fatalf("VetSource error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("expected no issues for clean code, got: %v", issues)
	}
}

func TestVetSourceUnreachableCode(t *testing.T) {
	src := `package main
func foo() {
	return
	println("unreachable")
}
func main() {}
`
	issues, err := VetSource(src)
	if err != nil {
		t.Fatalf("VetSource error: %v", err)
	}
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Message, "unreachable") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unreachable code issue, got: %v", issues)
	}
}

func TestVetSourceUnreachableAfterPanic(t *testing.T) {
	src := `package main
func foo() {
	panic("boom")
	println("unreachable")
}
func main() {}
`
	issues, err := VetSource(src)
	if err != nil {
		t.Fatalf("VetSource error: %v", err)
	}
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Message, "unreachable") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unreachable code after panic, got: %v", issues)
	}
}

func TestVetSourcePrintfMismatch(t *testing.T) {
	src := `package main
import "fmt"
func main() {
	fmt.Printf("%d %s", 42)
}
`
	issues, err := VetSource(src)
	if err != nil {
		t.Fatalf("VetSource error: %v", err)
	}
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Message, "fmt.Printf") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected printf mismatch issue, got: %v", issues)
	}
}

func TestVetSourcePrintfCorrect(t *testing.T) {
	src := `package main
import "fmt"
func main() {
	fmt.Printf("%d %s", 42, "hello")
}
`
	issues, err := VetSource(src)
	if err != nil {
		t.Fatalf("VetSource error: %v", err)
	}
	for _, issue := range issues {
		if strings.Contains(issue.Message, "fmt.Printf") {
			t.Errorf("unexpected printf issue on correct call: %v", issue)
		}
	}
}

func TestVetSourceSelfAssignment(t *testing.T) {
	src := `package main
func main() {
	x := 5
	x = x
	_ = x
}
`
	issues, err := VetSource(src)
	if err != nil {
		t.Fatalf("VetSource error: %v", err)
	}
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Message, "self-assignment") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected self-assignment issue, got: %v", issues)
	}
}

func TestVetSourceParseError(t *testing.T) {
	src := `this is not valid go`
	_, err := VetSource(src)
	if err == nil {
		t.Error("expected parse error for invalid source")
	}
}

func TestVetIssueString(t *testing.T) {
	issue := VetIssue{Line: 5, Column: 10, Message: "something wrong"}
	got := issue.String()
	if !strings.Contains(got, "5") || !strings.Contains(got, "10") || !strings.Contains(got, "something wrong") {
		t.Errorf("VetIssue.String() = %q, unexpected format", got)
	}
}

func TestCountPrintfVerbs(t *testing.T) {
	cases := []struct {
		format string
		want   int
	}{
		{"%d", 1},
		{"%d %s", 2},
		{"%%", 0},
		{"%d%%", 1},
		{"hello", 0},
		{"%v %v %v", 3},
		// Width and precision specifiers must count as a single verb each.
		{"%5d", 1},
		{"%.2f", 1},
		{"%-10s", 1},
		{"%05.2f", 1},
		{"%5d %s", 2},
		{"% d", 1}, // space flag
		{"%+d", 1}, // plus flag
		{"%#v", 1}, // hash flag
	}
	for _, c := range cases {
		got := countPrintfVerbs(c.format)
		if got != c.want {
			t.Errorf("countPrintfVerbs(%q) = %d, want %d", c.format, got, c.want)
		}
	}
}

func TestInspectSource(t *testing.T) {
	src := `package main

import "fmt"

func main() {
	x := 1 + 2
	fmt.Println(x)
}
`
	res, err := InspectSource(src)
	if err != nil {
		t.Fatalf("InspectSource: %v", err)
	}
	if res.Tree == nil || res.Tree.Type != "File" || res.Tree.Label != "package main" {
		t.Fatalf("unexpected root node: %+v", res.Tree)
	}
	if res.NodeCount < 10 {
		t.Errorf("NodeCount = %d, want >= 10", res.NodeCount)
	}
	if res.MaxDepth < 4 {
		t.Errorf("MaxDepth = %d, want >= 4", res.MaxDepth)
	}
	if len(res.Funcs) != 1 || res.Funcs[0] != "main" {
		t.Errorf("Funcs = %v, want [main]", res.Funcs)
	}
	if len(res.Imports) != 1 || res.Imports[0] != "fmt" {
		t.Errorf("Imports = %v, want [fmt]", res.Imports)
	}

	// Declaration children must follow source order: import decl before func
	// decl. (The File node also carries its package-name Ident as a child.)
	var declTypes []string
	for _, c := range res.Tree.Children {
		if c.Type == "GenDecl" || c.Type == "FuncDecl" {
			declTypes = append(declTypes, c.Type)
		}
	}
	if len(declTypes) != 2 || declTypes[0] != "GenDecl" || declTypes[1] != "FuncDecl" {
		t.Errorf("unexpected decl order: %v", declTypes)
	}

	if _, err := InspectSource("not go code"); err == nil {
		t.Error("expected parse error for invalid source")
	}
}

func TestLastStepCount(t *testing.T) {
	vm := NewInterpreter()
	if vm.LastStepCount() != 0 {
		t.Fatalf("fresh interpreter LastStepCount = %d, want 0", vm.LastStepCount())
	}
	if err := vm.Run("package main\nfunc main() { x := 0; for i := 0; i < 10; i++ { x += i }; _ = x }"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	first := vm.LastStepCount()
	if first == 0 {
		t.Fatal("LastStepCount = 0 after a run")
	}
	// A second identical run must report the identical deterministic cost.
	if err := vm.Run("package main\nfunc main() { x := 0; for i := 0; i < 10; i++ { x += i }; _ = x }"); err != nil {
		t.Fatalf("Run #2: %v", err)
	}
	if vm.LastStepCount() != first {
		t.Errorf("LastStepCount not deterministic: %d then %d", first, vm.LastStepCount())
	}
}

func TestAnalyzeCallGraph(t *testing.T) {
	src := `package main

import "fmt"

type Counter struct{ n int }

func (c *Counter) Inc() { c.n++ }

func (c *Counter) Report() {
	c.Inc()
	fmt.Println(c.n)
}

func helper(x int) int {
	return x * 2
}

func main() {
	c := &Counter{}
	c.Report()
	fmt.Println(helper(3))
}
`
	res, err := AnalyzeCallGraph(src)
	if err != nil {
		t.Fatalf("AnalyzeCallGraph: %v", err)
	}
	byName := map[string]CallGraphFunc{}
	for _, f := range res.Funcs {
		byName[f.Name] = f
	}
	want := []string{"Counter.Inc", "Counter.Report", "helper", "main"}
	for _, name := range want {
		if _, ok := byName[name]; !ok {
			t.Errorf("missing function %q in result: %+v", name, byName)
		}
	}

	report := byName["Counter.Report"]
	if report.Recv != "Counter" {
		t.Errorf("Counter.Report Recv = %q, want Counter", report.Recv)
	}
	foundIncCall := false
	for _, c := range report.Calls {
		if c.Resolved == "Counter.Inc" {
			foundIncCall = true
		}
	}
	if !foundIncCall {
		t.Errorf("Counter.Report calls = %+v, want a resolved call to Counter.Inc", report.Calls)
	}

	inc := byName["Counter.Inc"]
	if len(inc.CalledBy) != 1 || inc.CalledBy[0] != "Counter.Report" {
		t.Errorf("Counter.Inc CalledBy = %v, want [Counter.Report]", inc.CalledBy)
	}

	helper := byName["helper"]
	if len(helper.CalledBy) != 1 || helper.CalledBy[0] != "main" {
		t.Errorf("helper CalledBy = %v, want [main]", helper.CalledBy)
	}

	main := byName["main"]
	var mainCallNames []string
	for _, c := range main.Calls {
		mainCallNames = append(mainCallNames, c.Name)
	}
	foundReportCall, foundPrintln := false, false
	for _, c := range main.Calls {
		if c.Resolved == "Counter.Report" {
			foundReportCall = true
		}
		if c.Name == "fmt.Println" {
			foundPrintln = true
		}
	}
	if !foundReportCall {
		t.Errorf("main calls = %v, want a resolved call to Counter.Report", mainCallNames)
	}
	if !foundPrintln {
		t.Errorf("main calls = %v, want an (unresolved) fmt.Println call listed", mainCallNames)
	}

	if _, err := AnalyzeCallGraph("not go code"); err == nil {
		t.Error("expected parse error for invalid source")
	}
}

func TestAnalyzeCallGraphRecursion(t *testing.T) {
	src := `package main

func fib(n int) int {
	if n < 2 {
		return n
	}
	return fib(n-1) + fib(n-2)
}

func main() {
	fib(5)
}
`
	res, err := AnalyzeCallGraph(src)
	if err != nil {
		t.Fatalf("AnalyzeCallGraph: %v", err)
	}
	var fib *CallGraphFunc
	for i := range res.Funcs {
		if res.Funcs[i].Name == "fib" {
			fib = &res.Funcs[i]
		}
	}
	if fib == nil {
		t.Fatal("fib not found")
	}
	selfCalled := false
	for _, caller := range fib.CalledBy {
		if caller == "fib" {
			selfCalled = true
		}
	}
	if !selfCalled {
		t.Errorf("fib.CalledBy = %v, want to include fib (self-recursion)", fib.CalledBy)
	}
}
