package interp

import (
	"context"
	"testing"
)

// TestPackageScopeMultiFileForwardReferences proves the two-phase
// CollectDecls-then-EvalDecls split: a.go's package-level var initializer
// calls Helper(), which is only declared in b.go (alphabetically after
// a.go). Under a naive single-pass, file-order evaluation this would fail
// with "undefined: Helper"; two-phase loading collects every file's
// top-level funcs/types before evaluating any file's var initializers, so
// the forward reference resolves regardless of file order.
func TestPackageScopeMultiFileForwardReferences(t *testing.T) {
	vm := NewInterpreter()
	vm.Capabilities = FullCapabilities()

	mustWrite := func(p, src string) {
		t.Helper()
		if err := vm.VFS.MkdirAll("/pkg", 0755); err != nil {
			t.Fatalf("MkdirAll: %v", err)
		}
		if err := vm.VFS.WriteFile(p, []byte(src), 0644); err != nil {
			t.Fatalf("WriteFile %s: %v", p, err)
		}
	}

	mustWrite("/pkg/a.go", `package mypkg

var X = Helper()

func UseType() int {
	return NewPoint(2, 3).Sum()
}
`)
	mustWrite("/pkg/b.go", `package mypkg

func Helper() int {
	return 41
}
`)
	mustWrite("/pkg/c.go", `package mypkg

type Point struct {
	X int
	Y int
}

func NewPoint(x, y int) Point {
	return Point{X: x, Y: y}
}

func (p Point) Sum() int {
	return p.X + p.Y
}
`)

	files, fset, err := ParsePackageDir(vm.VFS, "/pkg")
	if err != nil {
		t.Fatalf("ParsePackageDir: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}

	ps := vm.NewPackageScope("mypkg")
	ctx := context.Background()

	err = vm.WithExecution(ctx, fset, func() error {
		for _, f := range files {
			if err := ps.CollectDecls(f, fset); err != nil {
				return err
			}
		}
		for _, f := range files {
			if err := ps.EvalDecls(ctx, f); err != nil {
				return err
			}
		}
		return ps.RunInit(ctx)
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	xVal, ok := ps.Lookup("X")
	if !ok {
		t.Fatalf("X not declared")
	}
	if ToInt(xVal) != 41 {
		t.Errorf("X = %v, want 41", xVal)
	}

	entry, ok := ps.Lookup("UseType")
	if !ok {
		t.Fatalf("UseType not declared")
	}
	fn, ok := entry.(*Function)
	if !ok {
		t.Fatalf("UseType is not a function: %T", entry)
	}
	result, err := vm.CallEntry(ctx, fset, fn, nil)
	if err != nil {
		t.Fatalf("CallEntry(UseType): %v", err)
	}
	if ToInt(result) != 5 {
		t.Errorf("UseType() = %v, want 5", result)
	}
}

// TestPackageScopeExportsCapitalizedOnly proves the export rule: only
// capitalized top-level identifiers appear in Exports(), matching Go's own
// visibility rule for a package accessed through an import alias.
func TestPackageScopeExportsCapitalizedOnly(t *testing.T) {
	vm := NewInterpreter()
	vm.Capabilities = FullCapabilities()

	src := `package mypkg

func Add(a, b int) int { return a + b }
func helper() int { return 1 }
var Shared = 7
var hidden = 8
`
	if err := vm.VFS.MkdirAll("/pkg2", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := vm.VFS.WriteFile("/pkg2/a.go", []byte(src), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	files, fset, err := ParsePackageDir(vm.VFS, "/pkg2")
	if err != nil {
		t.Fatalf("ParsePackageDir: %v", err)
	}

	ps := vm.NewPackageScope("mypkg")
	ctx := context.Background()
	err = vm.WithExecution(ctx, fset, func() error {
		for _, f := range files {
			if err := ps.CollectDecls(f, fset); err != nil {
				return err
			}
		}
		for _, f := range files {
			if err := ps.EvalDecls(ctx, f); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	exports := ps.Exports()
	if _, ok := exports.Funcs["Add"]; !ok {
		t.Errorf("expected Add to be exported")
	}
	if _, ok := exports.Funcs["helper"]; ok {
		t.Errorf("helper must not be exported")
	}
	if _, ok := exports.Vars["Shared"]; !ok {
		t.Errorf("expected Shared to be exported")
	}
	if _, ok := exports.Vars["hidden"]; ok {
		t.Errorf("hidden must not be exported")
	}
}

// TestPackageScopeRunInitStopsOnFirstFailure proves RunInit's short-circuit
// behavior: it iterates ps.inits in declaration order and returns
// immediately on the first callFunction error (see package_scope.go), so a
// failing/panicking init() must (a) surface as a non-nil error out of
// RunInit and (b) prevent every init() declared after it from ever running.
// Every pre-existing init()-related test (TestPackageScopeMultiFileForward
// References here, and interp/loader's
// TestRunProgramTwoPackagesInitAndVarOrder) only exercises init() functions
// that all succeed, so this gap was previously untested.
func TestPackageScopeRunInitStopsOnFirstFailure(t *testing.T) {
	vm := NewInterpreter()
	vm.Capabilities = FullCapabilities()

	src := `package mypkg

var Marker = 0

func init() {
	panic("boom")
}

func init() {
	Marker = 1
}
`
	if err := vm.VFS.MkdirAll("/initpkg", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := vm.VFS.WriteFile("/initpkg/a.go", []byte(src), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	files, fset, err := ParsePackageDir(vm.VFS, "/initpkg")
	if err != nil {
		t.Fatalf("ParsePackageDir: %v", err)
	}

	ps := vm.NewPackageScope("mypkg")
	ctx := context.Background()

	err = vm.WithExecution(ctx, fset, func() error {
		for _, f := range files {
			if err := ps.CollectDecls(f, fset); err != nil {
				return err
			}
		}
		for _, f := range files {
			if err := ps.EvalDecls(ctx, f); err != nil {
				return err
			}
		}
		return ps.RunInit(ctx)
	})
	if err == nil {
		t.Fatalf("expected RunInit to return an error from the panicking first init()")
	}

	marker, ok := ps.Lookup("Marker")
	if !ok {
		t.Fatalf("Marker not declared")
	}
	if ToInt(marker) != 0 {
		t.Errorf("Marker = %v, want 0 (the second init() must never run once the first one fails)", marker)
	}
}

// TestTestingPackageRunPropagatesFatalfFromSubtest proves the T.Run branch
// exercised when the CHILD subtest calls t.Fatalf, not just t.Errorf (the
// only path TestTestingPackageErrorfFatalfAndRun's TestWithSub subtest
// covers in testing_pkg_test.go). Per testing_pkg.go's T.Run native,
// errTestFatal (Fatalf's sentinel) only bounds the failing subtest —
// mirroring real Go's per-goroutine runtime.Goexit() — so:
//   - the outer CallEntry of the parent test must NOT see an error,
//   - the parent test body must keep running after t.Run returns,
//   - t.Run's own returned bool must be false (child failed),
//   - and the parent *testing.T must still end up marked failed.
func TestTestingPackageRunPropagatesFatalfFromSubtest(t *testing.T) {
	vm, _ := newTestVM()
	ctx := context.Background()

	src := `package mypkg

import "testing"

var Marker = 0

func TestWithFatalSub(t *testing.T) {
	ranOK := t.Run("child", func(t *testing.T) {
		t.Fatalf("stop")
	})
	if ranOK {
		Marker = 2
	} else {
		Marker = 1
	}
}
`
	if err := vm.VFS.MkdirAll("/subtestpkg", 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := vm.VFS.WriteFile("/subtestpkg/a.go", []byte(src), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	files, fset, err := ParsePackageDir(vm.VFS, "/subtestpkg")
	if err != nil {
		t.Fatalf("ParsePackageDir: %v", err)
	}

	ps := vm.NewPackageScope("mypkg")
	err = vm.WithExecution(ctx, fset, func() error {
		for _, f := range files {
			if err := ps.CollectDecls(f, fset); err != nil {
				return err
			}
		}
		for _, f := range files {
			if err := ps.EvalDecls(ctx, f); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	entry, ok := ps.Lookup("TestWithFatalSub")
	if !ok {
		t.Fatalf("TestWithFatalSub not declared")
	}
	fn := entry.(*Function)
	tv := vm.NewTestT()
	if _, err := vm.CallEntry(ctx, fset, fn, []any{tv}); err != nil {
		t.Fatalf("TestWithFatalSub: unexpected error %v (a Fatalf-ing subtest must not propagate an error to the parent's own call)", err)
	}
	if !TestFailed(tv) {
		t.Errorf("expected the parent test to be marked failed after a Fatalf-ing subtest")
	}

	marker, ok := ps.Lookup("Marker")
	if !ok {
		t.Fatalf("Marker not declared")
	}
	if ToInt(marker) != 1 {
		t.Errorf("Marker = %v, want 1 (t.Run must return false for a Fatalf-ing child, and the parent body must keep executing afterward)", marker)
	}
}
