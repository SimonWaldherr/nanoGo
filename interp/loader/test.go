package loader

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/token"
	"reflect"
	"strings"

	"simonwaldherr.de/go/nanogo/interp"
)

// Result categories. compile-error covers a build failure discovered while
// ensureBuilt parses/loads the program (the target function's own package,
// or one of its dependencies, failed to load).
const (
	CategoryPass         = "pass"
	CategoryWrongValue   = "wrong-value"
	CategoryPanic        = "panic"
	CategoryTimeout      = "timeout"
	CategoryCompileError = "compile-error"
)

// TestCase is one data-driven invocation for RunFunctionTest: call the
// target function with Args and compare its return value to Want.
type TestCase struct {
	Args []any
	Want any
}

// TestResult classifies one TestCase's or *_test.go Test function's outcome.
// Got/Want are kept separate from Category so a caller can drop the raw
// values (e.g. before logging, for privacy) while keeping the
// classification. Line/Column point at the tested function's own
// declaration — this is a static, not a per-assertion, location; nanoGo's
// testing.T.Errorf/Fatalf are plain natives with no call-site AST access, so
// per-assertion positions are not available (see interp/testing_pkg.go and
// TraceEvent.Assertion for the structured, if position-less, assertion
// event that IS available through a Tracer).
type TestResult struct {
	Pass     bool
	Got      any
	Want     any
	Panic    bool
	Line     int
	Column   int
	Category string
}

// RunFunctionTest calls the exported function "Func" in package "pkg" (the
// target is "pkg.Func") once per TestCase — each call gets its own fresh
// execution and step budget, matching how `go test` isolates tests — and
// compares its return value against Want using a host-native, type-coercing
// equality (so e.g. an int Want matches a nanoGo int result, and a []int
// Want matches a *SliceVal result).
func RunFunctionTest(ctx context.Context, vm *interp.Interpreter, prog *Program, target string, cases []TestCase) ([]TestResult, error) {
	if err := ensureBuilt(ctx, vm, prog); err != nil {
		return nil, err
	}
	pkgName, funcName, ok := splitTarget(target)
	if !ok {
		return nil, fmt.Errorf("nanogo/loader: invalid target %q, want \"pkg.Func\"", target)
	}
	dir, ok := findPackageDirByName(prog, pkgName)
	if !ok {
		return nil, fmt.Errorf("nanogo/loader: unknown package %q", pkgName)
	}
	scope := prog.built[dir]
	v, ok := scope.Lookup(funcName)
	if !ok {
		return nil, fmt.Errorf("nanogo/loader: function %q not found in package %q", funcName, pkgName)
	}
	fn, ok := v.(*interp.Function)
	if !ok {
		return nil, fmt.Errorf("nanogo/loader: %q is not a function", target)
	}

	pp := prog.Packages[dir]
	line, col := findFuncDeclPosition(pp.Files, pp.FSet, funcName, "")

	results := make([]TestResult, len(cases))
	for i, tc := range cases {
		results[i] = runOneFunctionTest(ctx, vm, pp.FSet, fn, tc, line, col)
	}
	return results, nil
}

func runOneFunctionTest(ctx context.Context, vm *interp.Interpreter, fset *token.FileSet, fn *interp.Function, tc TestCase, line, col int) TestResult {
	args := make([]any, len(tc.Args))
	for i, a := range tc.Args {
		guestVal, err := interp.BridgeToGuest(a)
		if err != nil {
			return TestResult{Want: tc.Want, Line: line, Column: col, Category: CategoryCompileError}
		}
		args[i] = guestVal
	}

	got, err := vm.CallEntry(ctx, fset, fn, args)
	if err != nil {
		switch {
		case interp.IsTestFatal(err):
			return TestResult{Got: got, Want: tc.Want, Line: line, Column: col, Category: CategoryWrongValue}
		case isTimeoutErr(err):
			return TestResult{Want: tc.Want, Line: line, Column: col, Category: CategoryTimeout}
		default:
			// Both a real guest panic() and a plain runtime error (e.g.
			// "division by zero", "index out of range") land here: the
			// fixed 5-category enum has no separate "runtime error"
			// bucket, and both mean the same thing to a caller — the
			// function under test did not return normally.
			return TestResult{Want: tc.Want, Panic: true, Line: line, Column: col, Category: CategoryPanic}
		}
	}

	hostGot, convErr := interp.BridgeToHost(got)
	if convErr != nil {
		hostGot = got
	}
	if valuesEqual(hostGot, tc.Want) {
		return TestResult{Pass: true, Got: hostGot, Want: tc.Want, Line: line, Column: col, Category: CategoryPass}
	}
	return TestResult{Got: hostGot, Want: tc.Want, Line: line, Column: col, Category: CategoryWrongValue}
}

// RunPackageTests discovers every TestXxx(t *testing.T) function among
// pkgName's already-parsed _test.go files and runs each with a fresh
// *testing.T via its own vm.CallEntry (independent step budget, matching
// go test's per-test isolation). A test's own body is unmodified Go — the
// same file runs unchanged through a real `go test` (see interp/testing_pkg.go).
func RunPackageTests(ctx context.Context, vm *interp.Interpreter, prog *Program, pkgName string) ([]TestResult, error) {
	if err := ensureBuilt(ctx, vm, prog); err != nil {
		return nil, err
	}
	dir, ok := findPackageDirByName(prog, pkgName)
	if !ok {
		return nil, fmt.Errorf("nanogo/loader: unknown package %q", pkgName)
	}
	pp := prog.Packages[dir]
	scope := prog.built[dir]

	var results []TestResult
	for _, file := range pp.TestFiles {
		for _, decl := range file.Decls {
			d, ok := decl.(*ast.FuncDecl)
			if !ok || d.Recv != nil || !strings.HasPrefix(d.Name.Name, "Test") {
				continue
			}
			v, ok := scope.Lookup(d.Name.Name)
			if !ok {
				continue
			}
			fn, ok := v.(*interp.Function)
			if !ok {
				continue
			}
			pos := pp.FSet.Position(d.Pos())
			t := vm.NewTestT()
			_, err := vm.CallEntry(ctx, pp.FSet, fn, []any{t})
			results = append(results, classifyTestOutcome(t, err, pos.Line, pos.Column))
		}
	}
	return results, nil
}

func classifyTestOutcome(t any, err error, line, col int) TestResult {
	if err != nil && !interp.IsTestFatal(err) {
		if isTimeoutErr(err) {
			return TestResult{Line: line, Column: col, Category: CategoryTimeout}
		}
		return TestResult{Panic: true, Line: line, Column: col, Category: CategoryPanic}
	}
	if interp.TestFailed(t) {
		return TestResult{Line: line, Column: col, Category: CategoryWrongValue}
	}
	return TestResult{Pass: true, Line: line, Column: col, Category: CategoryPass}
}

// isTimeoutErr reports whether err reflects an execution stopped by a
// resource limit or cancellation, rather than a guest panic.
func isTimeoutErr(err error) bool {
	return errors.Is(err, interp.ErrStepLimit) ||
		errors.Is(err, interp.ErrGoroutineLimit) ||
		errors.Is(err, interp.ErrKilled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled)
}

func findFuncDeclPosition(files []*ast.File, fset *token.FileSet, name, receiver string) (line, col int) {
	for _, file := range files {
		for _, decl := range file.Decls {
			d, ok := decl.(*ast.FuncDecl)
			if !ok || d.Name.Name != name {
				continue
			}
			if receiver != "" && (d.Recv == nil || len(d.Recv.List) == 0) {
				continue
			}
			pos := fset.Position(d.Pos())
			return pos.Line, pos.Column
		}
	}
	return 0, 0
}

// valuesEqual compares two values after normalizing both to plain Go
// values: numeric types collapse to float64 (so a Want of 5 matches an int,
// int64, or float64 Got), and containers are compared structurally, not by
// nanoGo's Go-faithful (pointer/uncomparable) semantics.
func valuesEqual(got, want any) bool {
	return reflect.DeepEqual(normalizeValue(got), normalizeValue(want))
}

func normalizeValue(v any) any {
	switch x := v.(type) {
	case int:
		return float64(x)
	case int64:
		return float64(x)
	case float32:
		return float64(x)
	case float64:
		return x
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, vv := range x {
			out[k] = normalizeValue(vv)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, vv := range x {
			out[i] = normalizeValue(vv)
		}
		return out
	default:
		return v
	}
}
