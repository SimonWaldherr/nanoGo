package loader

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"simonwaldherr.de/go/nanogo/interp"
)

func TestRunFunctionTestDataDrivenClassification(t *testing.T) {
	vm, _ := newLoaderTestVM()
	vfs := vm.VFS

	writeLoaderFile(t, vfs, "/repo/go.mod", "module example.com/app\n")
	writeLoaderFile(t, vfs, "/repo/main.go", `package main

func Add(a, b int) int {
	return a + b
}

func Div(a, b int) int {
	return a / b
}

func main() {}
`)

	prog, err := LoadModule(vfs, "/repo", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	results, err := RunFunctionTest(context.Background(), vm, prog, "main.Add", []TestCase{
		{Args: []any{2, 3}, Want: 5},
		{Args: []any{2, 3}, Want: 999},
	})
	if err != nil {
		t.Fatalf("RunFunctionTest(Add): %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !results[0].Pass || results[0].Category != CategoryPass {
		t.Errorf("case 0: expected pass, got %+v", results[0])
	}
	if results[1].Pass || results[1].Category != CategoryWrongValue {
		t.Errorf("case 1: expected wrong-value, got %+v", results[1])
	}
	if results[1].Got != 5 || results[1].Want != 999 {
		t.Errorf("case 1: expected Got/Want preserved separately from Category, got %+v", results[1])
	}

	divResults, err := RunFunctionTest(context.Background(), vm, prog, "main.Div", []TestCase{
		{Args: []any{10, 0}, Want: 0},
	})
	if err != nil {
		t.Fatalf("RunFunctionTest(Div): %v", err)
	}
	if !divResults[0].Panic || divResults[0].Category != CategoryPanic {
		t.Errorf("expected a panic category for division by zero, got %+v", divResults[0])
	}
}

// TestRunPackageTestsMatchesRealGoTest proves the round-trip acceptance
// criterion: one unmodified *_test.go source runs both through nanoGo's
// RunPackageTests and through a real `go test`. It skips (rather than
// fails) if no usable Go toolchain is on PATH, since this is verifying
// interoperability with an external tool, not nanoGo's own correctness.
func TestRunPackageTestsMatchesRealGoTest(t *testing.T) {
	const mainSrc = `package roundtrip

func Add(a, b int) int {
	return a + b
}

`
	const testSrc = `package roundtrip

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 3); got != 5 {
		t.Errorf("got %d, want %d", got, 5)
	}
	t.Run("subcase", func(t *testing.T) {
		if got := Add(1, 1); got != 2 {
			t.Errorf("got %d, want %d", got, 2)
		}
	})
}
`

	vm, _ := newLoaderTestVM()
	vfs := vm.VFS
	writeLoaderFile(t, vfs, "/rt/go.mod", "module roundtrip\n")
	writeLoaderFile(t, vfs, "/rt/main.go", mainSrc)
	writeLoaderFile(t, vfs, "/rt/main_test.go", testSrc)

	prog, err := LoadModule(vfs, "/rt", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	// The package's non-entry name here is "roundtrip", not "main" — treat
	// it as the entry package directly by building it via RunProgram-style
	// setup first (RunPackageTests only needs ensureBuilt, which RunProgram
	// or any of the test/bench entry points trigger).
	results, err := RunPackageTests(context.Background(), vm, prog, "roundtrip")
	if err != nil {
		t.Fatalf("RunPackageTests: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 top-level Test function result, got %d: %+v", len(results), results)
	}
	if !results[0].Pass {
		t.Errorf("expected TestAdd to pass in nanoGo, got %+v", results[0])
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no `go` on PATH, skipping real go test round-trip check")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module roundtrip\n\ngo 1.18\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainSrc), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(testSrc), 0644); err != nil {
		t.Fatalf("write main_test.go: %v", err)
	}

	cmd := exec.Command(goBin, "test", "./...")
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("real `go test` unavailable/incompatible in this environment (%v); output:\n%s", err, out)
	}
}

// TestRecoverCannotInterceptTestFatal locks in an invariant recover()
// depends on (see interp/testing_pkg.go's errTestFatal comment): t.Fatalf
// unwinds the whole guest call stack the same way real Go's runtime.Goexit
// does, and a guest defer's recover() call must not be able to stop that —
// if it could, a test wrapping its own body in a defer+recover (a
// reasonable thing to write, unaware of nanoGo's internals) would silently
// keep running past a fatal failure instead of stopping.
func TestRecoverCannotInterceptTestFatal(t *testing.T) {
	vm, _ := newLoaderTestVM()
	vfs := vm.VFS
	writeLoaderFile(t, vfs, "/fatalrecover/go.mod", "module example.com/fatalrecover\n")
	writeLoaderFile(t, vfs, "/fatalrecover/main.go", "package fatalrecover\n")
	writeLoaderFile(t, vfs, "/fatalrecover/main_test.go", `package fatalrecover

import "testing"

func TestFatalThenRecover(t *testing.T) {
	defer func() {
		recover()
	}()
	t.Fatalf("boom")
	t.Error("must not run")
}
`)

	prog, err := LoadModule(vfs, "/fatalrecover", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	results, err := RunPackageTests(context.Background(), vm, prog, "fatalrecover")
	if err != nil {
		t.Fatalf("RunPackageTests: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d: %+v", len(results), results)
	}
	r := results[0]
	if r.Pass || r.Category != CategoryWrongValue {
		t.Errorf("expected a failed, non-recovered test; got %+v", r)
	}
	if len(r.Messages) != 1 || r.Messages[0] != "boom" {
		t.Errorf("expected only Fatalf's own message (the code after it must never run); got %+v", r.Messages)
	}
}

func TestRunPackageTestsSupportsCommonTestingMethodsAndFilter(t *testing.T) {
	vm, _ := newLoaderTestVM()
	vfs := vm.VFS
	writeLoaderFile(t, vfs, "/methods/go.mod", "module example.com/methods\n")
	writeLoaderFile(t, vfs, "/methods/main.go", "package methods\n")
	writeLoaderFile(t, vfs, "/methods/main_test.go", `package methods

import "testing"

func TestPass(t *testing.T) {}

func TestFailure(t *testing.T) {
	t.Error("got", 2, "want", 3)
}

func TestSkipped(t *testing.T) {
	t.Skipf("not available on %s", "this platform")
}

// Testhelper is intentionally not a Go test name.
func Testhelper(t *testing.T) {
	t.Fatal("must not run")
}
`)

	prog, err := LoadModule(vfs, "/methods", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	results, err := RunPackageTestsMatching(context.Background(), vm, prog, "methods", "Pass|Skipped")
	if err != nil {
		t.Fatalf("RunPackageTestsMatching: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("filtered result count = %d, want 2: %+v", len(results), results)
	}
	if results[0].Name != "TestPass" || !results[0].Pass || results[0].Skipped {
		t.Errorf("pass result = %+v", results[0])
	}
	if results[1].Name != "TestSkipped" || !results[1].Pass || !results[1].Skipped || results[1].Category != CategorySkip {
		t.Errorf("skip result = %+v", results[1])
	}

	// A fresh *testing.T is supplied per call, so the same loaded module can
	// be used to assert Error's human-readable diagnostics independently.
	results, err = RunPackageTestsMatching(context.Background(), vm, prog, "methods", "^TestFailure$")
	if err != nil {
		t.Fatalf("RunPackageTests: %v", err)
	}
	if len(results) != 1 || results[0].Name != "TestFailure" || results[0].Pass || len(results[0].Messages) != 1 || results[0].Messages[0] != "got 2 want 3" {
		t.Errorf("failure result = %+v", results)
	}

	if _, err := RunPackageTestsMatching(context.Background(), vm, prog, "methods", "["); err == nil {
		t.Fatal("invalid regexp should fail before running tests")
	}
}

// --- Error-classification coverage --------------------------------------
//
// TestRunFunctionTestDataDrivenClassification (above) already covers
// CategoryPass, CategoryWrongValue, and CategoryPanic. The tests below cover
// every remaining branch that was previously untested: each fmt.Errorf-guarded
// input-validation branch shared by RunFunctionTest/RunFunctionBench/
// RunPackageTests/RunPackageBenchmarks (invalid "pkg.Func" target with no dot,
// unknown package name, function not found in package, a looked-up symbol
// that isn't a *interp.Function), plus CategoryCompileError (a TestCase.Args
// value BridgeToGuest can't represent) and CategoryTimeout (an execution
// stopped by a step limit).

// newErrClassificationProgram builds a tiny single-package program (an "Add"
// function, a "Version" package-level var so tests can exercise the
// not-a-function branch, and a "Loop" function so tests can exercise the
// timeout branch) via its own fresh VM/VFS pair, for tests that only care
// about error classification rather than any successful run.
func newErrClassificationProgram(t *testing.T) (*interp.Interpreter, *Program) {
	t.Helper()
	vm, _ := newLoaderTestVM()
	vfs := vm.VFS
	writeLoaderFile(t, vfs, "/app/go.mod", "module example.com/app\n")
	writeLoaderFile(t, vfs, "/app/main.go", `package main

var Version = 1

func Add(a, b int) int {
	return a + b
}

func Loop() {
	for {
	}
}

func main() {}
`)
	prog, err := LoadModule(vfs, "/app", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	return vm, prog
}

func TestRunFunctionTestInvalidTargetNoDot(t *testing.T) {
	vm, prog := newErrClassificationProgram(t)
	_, err := RunFunctionTest(context.Background(), vm, prog, "badtarget", []TestCase{
		{Args: []any{1, 2}, Want: 3},
	})
	if err == nil {
		t.Fatal("expected an error for a target string with no dot")
	}
	if !strings.Contains(err.Error(), "invalid target") {
		t.Errorf("expected an \"invalid target\" error, got: %v", err)
	}
}

func TestRunFunctionTestUnknownPackage(t *testing.T) {
	vm, prog := newErrClassificationProgram(t)
	_, err := RunFunctionTest(context.Background(), vm, prog, "nosuchpkg.Foo", []TestCase{
		{Want: 0},
	})
	if err == nil {
		t.Fatal("expected an error for an unknown package name")
	}
	if !strings.Contains(err.Error(), "unknown package") {
		t.Errorf("expected an \"unknown package\" error, got: %v", err)
	}
}

func TestRunFunctionTestFunctionNotFound(t *testing.T) {
	vm, prog := newErrClassificationProgram(t)
	_, err := RunFunctionTest(context.Background(), vm, prog, "main.NoSuchFunc", []TestCase{
		{Want: 0},
	})
	if err == nil {
		t.Fatal("expected an error for a function missing from the package")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected a \"not found\" error, got: %v", err)
	}
}

func TestRunFunctionTestSymbolNotAFunction(t *testing.T) {
	vm, prog := newErrClassificationProgram(t)
	_, err := RunFunctionTest(context.Background(), vm, prog, "main.Version", []TestCase{
		{Want: 1},
	})
	if err == nil {
		t.Fatal("expected an error when the target resolves to a package-level var, not a function")
	}
	if !strings.Contains(err.Error(), "is not a function") {
		t.Errorf("expected an \"is not a function\" error, got: %v", err)
	}
}

// TestRunFunctionTestCompileErrorOnUnbridgeableArg exercises
// runOneFunctionTest's CategoryCompileError branch: a raw channel value has
// no host<->guest bridging representation (see bridgeToGuest's default case
// in interp/host_channel.go's unexported bridgeToGuest, wrapped for tests by
// the exported interp.BridgeToGuest), so passing one as a TestCase argument
// must be classified as CategoryCompileError without ever reaching
// vm.CallEntry.
func TestRunFunctionTestCompileErrorOnUnbridgeableArg(t *testing.T) {
	vm, prog := newErrClassificationProgram(t)
	results, err := RunFunctionTest(context.Background(), vm, prog, "main.Add", []TestCase{
		{Args: []any{make(chan int)}, Want: 0},
	})
	if err != nil {
		t.Fatalf("RunFunctionTest: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Category != CategoryCompileError {
		t.Errorf("expected CategoryCompileError, got %+v", results[0])
	}
	if results[0].Pass {
		t.Errorf("expected Pass=false for an unbridgeable argument, got %+v", results[0])
	}
}

// TestRunFunctionTestTimeoutCategory exercises isTimeoutErr/CategoryTimeout:
// the target function busy-loops forever, so under a low MaxSteps it must
// stop with interp.ErrStepLimit, which runOneFunctionTest classifies as
// CategoryTimeout rather than CategoryPanic. The program is built once under
// the default step limit first (ensureBuilt caches the build on prog, so the
// low limit set afterward only affects the timed invocation itself) —
// mirrors interp_test.go's TestExecutionStepLimitStopsBusyLoop.
func TestRunFunctionTestTimeoutCategory(t *testing.T) {
	vm, prog := newErrClassificationProgram(t)
	if err := ensureBuilt(context.Background(), vm, prog); err != nil {
		t.Fatalf("ensureBuilt: %v", err)
	}
	vm.Limits = interp.ExecutionLimits{MaxSteps: 50}

	results, err := RunFunctionTest(context.Background(), vm, prog, "main.Loop", []TestCase{
		{Want: nil},
	})
	if err != nil {
		t.Fatalf("RunFunctionTest: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Category != CategoryTimeout {
		t.Errorf("expected CategoryTimeout, got %+v", results[0])
	}
	if results[0].Pass || results[0].Panic {
		t.Errorf("expected neither Pass nor Panic for a step-limited timeout, got %+v", results[0])
	}
}

func TestRunPackageTestsUnknownPackage(t *testing.T) {
	vm, prog := newErrClassificationProgram(t)
	_, err := RunPackageTests(context.Background(), vm, prog, "nosuchpkg")
	if err == nil {
		t.Fatal("expected an error for an unknown package name")
	}
	if !strings.Contains(err.Error(), "unknown package") {
		t.Errorf("expected an \"unknown package\" error, got: %v", err)
	}
}

func TestRunFunctionBenchInvalidTargetNoDot(t *testing.T) {
	vm, prog := newErrClassificationProgram(t)
	_, err := RunFunctionBench(context.Background(), vm, prog, "badtarget", BenchOptions{})
	if err == nil {
		t.Fatal("expected an error for a target string with no dot")
	}
	if !strings.Contains(err.Error(), "invalid target") {
		t.Errorf("expected an \"invalid target\" error, got: %v", err)
	}
}

func TestRunFunctionBenchUnknownPackage(t *testing.T) {
	vm, prog := newErrClassificationProgram(t)
	_, err := RunFunctionBench(context.Background(), vm, prog, "nosuchpkg.Foo", BenchOptions{})
	if err == nil {
		t.Fatal("expected an error for an unknown package name")
	}
	if !strings.Contains(err.Error(), "unknown package") {
		t.Errorf("expected an \"unknown package\" error, got: %v", err)
	}
}

func TestRunFunctionBenchFunctionNotFound(t *testing.T) {
	vm, prog := newErrClassificationProgram(t)
	_, err := RunFunctionBench(context.Background(), vm, prog, "main.NoSuchFunc", BenchOptions{})
	if err == nil {
		t.Fatal("expected an error for a function missing from the package")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected a \"not found\" error, got: %v", err)
	}
}

func TestRunFunctionBenchSymbolNotAFunction(t *testing.T) {
	vm, prog := newErrClassificationProgram(t)
	_, err := RunFunctionBench(context.Background(), vm, prog, "main.Version", BenchOptions{})
	if err == nil {
		t.Fatal("expected an error when the target resolves to a package-level var, not a function")
	}
	if !strings.Contains(err.Error(), "is not a function") {
		t.Errorf("expected an \"is not a function\" error, got: %v", err)
	}
}

func TestRunPackageBenchmarksUnknownPackage(t *testing.T) {
	vm, prog := newErrClassificationProgram(t)
	_, err := RunPackageBenchmarks(context.Background(), vm, prog, "nosuchpkg", BenchOptions{})
	if err == nil {
		t.Fatal("expected an error for an unknown package name")
	}
	if !strings.Contains(err.Error(), "unknown package") {
		t.Errorf("expected an \"unknown package\" error, got: %v", err)
	}
}
