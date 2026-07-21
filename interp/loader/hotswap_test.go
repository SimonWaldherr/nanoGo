package loader

import (
	"context"
	"testing"
	"time"
)

func TestReplaceFunctionVisibleThroughExistingImportAlias(t *testing.T) {
	vm, _ := newLoaderTestVM()
	vfs := vm.VFS

	writeLoaderFile(t, vfs, "/hs/go.mod", "module example.com/hs\n")
	writeLoaderFile(t, vfs, "/hs/lib/lib.go", `package lib

func Add(a, b int) int {
	return a + b
}
`)
	writeLoaderFile(t, vfs, "/hs/main.go", `package main

import "example.com/hs/lib"

func UseLib(a, b int) int {
	return lib.Add(a, b)
}

func main() {}
`)

	prog, err := LoadModule(vfs, "/hs", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	if err := RunProgram(context.Background(), vm, prog, "main"); err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	before, err := RunFunctionTest(context.Background(), vm, prog, "main.UseLib", []TestCase{{Args: []any{2, 3}, Want: 5}})
	if err != nil {
		t.Fatalf("RunFunctionTest before swap: %v", err)
	}
	if !before[0].Pass {
		t.Fatalf("expected UseLib(2,3)==5 before the swap, got %+v", before[0])
	}

	if err := ReplaceFunction(vm, prog, "lib", "Add", `func Add(a, b int) int {
	return a*b
}`); err != nil {
		t.Fatalf("ReplaceFunction: %v", err)
	}

	after, err := RunFunctionTest(context.Background(), vm, prog, "main.UseLib", []TestCase{{Args: []any{2, 3}, Want: 6}})
	if err != nil {
		t.Fatalf("RunFunctionTest after swap: %v", err)
	}
	if !after[0].Pass {
		t.Errorf("expected UseLib(2,3)==6 after hot-swapping lib.Add without reloading, got %+v", after[0])
	}

	// Calling lib.Add directly (not through main's alias) must also observe
	// the swap, proving Replace mutated the one shared Exports() map rather
	// than a per-importer copy.
	directResults, err := RunFunctionTest(context.Background(), vm, prog, "lib.Add", []TestCase{{Args: []any{4, 5}, Want: 20}})
	if err != nil {
		t.Fatalf("RunFunctionTest(lib.Add) after swap: %v", err)
	}
	if !directResults[0].Pass {
		t.Errorf("expected lib.Add(4,5)==20 after the swap, got %+v", directResults[0])
	}
}

func TestReplaceFunctionRejectsMismatchedName(t *testing.T) {
	vm, _ := newLoaderTestVM()
	vfs := vm.VFS
	writeLoaderFile(t, vfs, "/hs2/go.mod", "module example.com/hs2\n")
	writeLoaderFile(t, vfs, "/hs2/main.go", `package main

func Add(a, b int) int { return a + b }
func main() {}
`)
	prog, err := LoadModule(vfs, "/hs2", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	if err := RunProgram(context.Background(), vm, prog, "main"); err != nil {
		t.Fatalf("RunProgram: %v", err)
	}
	if err := ReplaceFunction(vm, prog, "main", "Add", `func NotAdd(a, b int) int { return a - b }`); err == nil {
		t.Error("expected an error when newSource doesn't declare the target function name")
	}
}

// TestReplaceFunctionDuringConcurrentExecution hot-swaps lib.Add repeatedly
// while a long-running call into it is still in flight on another
// goroutine, mirroring TestKillStopsRunningProgram's pattern (in
// interp/interp_test.go) of starting a blocking execution in a background
// goroutine and synchronizing on vm.IsRunning().
//
// This exercises a scenario package_scope.go's own doc comment on Replace
// says is supported ("taking effect for every future call ... including
// calls made through another package's import alias"): PackageScope.Replace
// writes to the cached Exports() *Package's Funcs map while
// resolvePackageSelector (interp/packages.go), the read path every
// package-qualified call like lib.Add(...) goes through, reads that same
// map — both now synchronized via Package.mu (see interp/environment.go's
// Package and interp/package_scope.go's Replace).
//
// Run this test with `go test -race` to double-check there is no remaining
// data race:
//
//	go test -race -run TestReplaceFunctionDuringConcurrentExecution ./interp/loader/...
func TestReplaceFunctionDuringConcurrentExecution(t *testing.T) {
	vm, _ := newLoaderTestVM()
	vfs := vm.VFS

	writeLoaderFile(t, vfs, "/hs3/go.mod", "module example.com/hs3\n")
	writeLoaderFile(t, vfs, "/hs3/lib/lib.go", `package lib

func Add(a, b int) int {
	return a + b
}
`)
	writeLoaderFile(t, vfs, "/hs3/main.go", `package main

import "example.com/hs3/lib"

// Loop calls lib.Add on every iteration so a long-running background call
// keeps exercising resolvePackageSelector's read of lib's exported Funcs
// map for the whole run -- the read side of the race this test targets.
func Loop(n int) int {
	sum := 0
	for i := 0; i < n; i++ {
		sum = lib.Add(sum, 1)
	}
	return sum
}

func main() {}
`)

	prog, err := LoadModule(vfs, "/hs3", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	if err := RunProgram(context.Background(), vm, prog, "main"); err != nil {
		t.Fatalf("RunProgram: %v", err)
	}

	// Large enough to run for a few hundred milliseconds on this
	// tree-walking interpreter (measured ~0.5-1s), well under the default
	// 10,000,000-step execution limit (interp.DefaultLimits), so it gives
	// the concurrent ReplaceFunction loop below a real window to overlap
	// with live execution rather than just racing the goroutine's startup.
	const iterations = 500000

	type testOutcome struct {
		results []TestResult
		err     error
	}
	outcome := make(chan testOutcome, 1)
	go func() {
		results, err := RunFunctionTest(context.Background(), vm, prog, "main.Loop", []TestCase{{Args: []any{iterations}}})
		outcome <- testOutcome{results: results, err: err}
	}()

	startDeadline := time.After(2 * time.Second)
	for !vm.IsRunning() {
		select {
		case <-startDeadline:
			t.Fatal("main.Loop never started running")
		case <-time.After(time.Millisecond):
		}
	}

	// Hot-swap lib.Add back and forth while main.Loop is live. The loop
	// condition (vm.IsRunning()) naturally spreads the swaps across the
	// whole execution window: it keeps firing until the background call
	// completes, with a generous safety cap in case IsRunning never clears.
	adds := []string{
		`func Add(a, b int) int { return a + b }`,
		`func Add(a, b int) int { return a + b + 1 }`,
	}
	swaps := 0
	for i := 0; vm.IsRunning() && i < 1_000_000; i++ {
		if err := ReplaceFunction(vm, prog, "lib", "Add", adds[i%len(adds)]); err != nil {
			t.Fatalf("ReplaceFunction while main.Loop was running: %v", err)
		}
		swaps++
	}
	if swaps == 0 {
		t.Fatal("no ReplaceFunction calls landed while main.Loop was running (no concurrency was actually exercised)")
	}
	t.Logf("performed %d concurrent ReplaceFunction calls while main.Loop was running", swaps)

	select {
	case res := <-outcome:
		if res.err != nil {
			t.Fatalf("RunFunctionTest(main.Loop) returned an unexpected error: %v", res.err)
		}
		if len(res.results) != 1 {
			t.Fatalf("expected exactly 1 result, got %d", len(res.results))
		}
		if res.results[0].Category == CategoryPanic {
			t.Fatalf("main.Loop panicked while lib.Add was hot-swapped concurrently: %+v", res.results[0])
		}
		// The final sum is intentionally not asserted against a fixed
		// Want: which Add implementation each of the loop's 500000 calls
		// observes depends on real-time interleaving with the concurrent
		// ReplaceFunction calls above. That nondeterminism is the expected
		// consequence of a supported concurrent hot-swap, not a bug --
		// this test's job is to confirm the run completes cleanly (and,
		// under `go test -race`, that no data race is reported), not to
		// pin down a specific final value.
	case <-time.After(20 * time.Second):
		t.Fatal("main.Loop did not finish within the timeout while lib.Add was being hot-swapped concurrently")
	}
}
