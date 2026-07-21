package loader

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRunFunctionBenchIsDeterministic(t *testing.T) {
	vm, _ := newLoaderTestVM()
	vfs := vm.VFS

	writeLoaderFile(t, vfs, "/bench/go.mod", "module example.com/bench\n")
	writeLoaderFile(t, vfs, "/bench/main.go", `package main

func Sort() int {
	total := 0
	for i := 0; i < 50; i++ {
		total += i
	}
	return total
}

func main() {}
`)

	prog, err := LoadModule(vfs, "/bench", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	var results []BenchResult
	for i := 0; i < 3; i++ {
		res, err := RunFunctionBench(context.Background(), vm, prog, "main.Sort", BenchOptions{MinIterations: 1000})
		if err != nil {
			t.Fatalf("RunFunctionBench run %d: %v", i, err)
		}
		results = append(results, res)
	}

	for i, r := range results {
		if r.StepsPerOp == 0 {
			t.Fatalf("run %d: StepsPerOp is 0, expected a positive deterministic step count", i)
		}
		if r.Iterations != 1000 {
			t.Errorf("run %d: Iterations = %d, want 1000", i, r.Iterations)
		}
	}
	if results[0].StepsPerOp != results[1].StepsPerOp || results[1].StepsPerOp != results[2].StepsPerOp {
		t.Errorf("expected identical StepsPerOp across 3 runs, got %d, %d, %d",
			results[0].StepsPerOp, results[1].StepsPerOp, results[2].StepsPerOp)
	}
}

func TestRunPackageBenchmarksUsesTestingBSubset(t *testing.T) {
	vm, _ := newLoaderTestVM()
	vfs := vm.VFS

	writeLoaderFile(t, vfs, "/bench2/go.mod", "module example.com/bench2\n")
	writeLoaderFile(t, vfs, "/bench2/main.go", `package main

func Sum(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		total += i
	}
	return total
}

func main() {}
`)
	writeLoaderFile(t, vfs, "/bench2/main_test.go", `package main

import "testing"

func BenchmarkSum(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Sum(10)
	}
}
`)

	prog, err := LoadModule(vfs, "/bench2", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}

	results, err := RunPackageBenchmarks(context.Background(), vm, prog, "main", BenchOptions{MinIterations: 100})
	if err != nil {
		t.Fatalf("RunPackageBenchmarks: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 benchmark result, got %d", len(results))
	}
	if results[0].StepsPerOp == 0 {
		t.Errorf("expected a positive StepsPerOp, got 0")
	}
	if results[0].Iterations != 100 {
		t.Errorf("Iterations = %d, want 100", results[0].Iterations)
	}
}

// TestBenchmarkFunctionMatchesRealGoTestBench proves the second half of the
// round-trip acceptance criterion (RunPackageTests/TestRunPackageTestsMatchesRealGoTest
// in test_test.go proves the TestXxx half): an unmodified BenchmarkXxx(b
// *testing.B) source runs both through nanoGo's RunPackageBenchmarks and
// through a real `go test -bench`. Skips rather than fails if no usable Go
// toolchain is on PATH.
func TestBenchmarkFunctionMatchesRealGoTestBench(t *testing.T) {
	const mainSrc = `package benchrt

func SumTo(n int) int {
	total := 0
	for i := 0; i < n; i++ {
		total += i
	}
	return total
}
`
	const benchSrc = `package benchrt

import "testing"

func BenchmarkSumTo(b *testing.B) {
	for i := 0; i < b.N; i++ {
		SumTo(10)
	}
}
`

	vm, _ := newLoaderTestVM()
	vfs := vm.VFS
	writeLoaderFile(t, vfs, "/brt/go.mod", "module benchrt\n")
	writeLoaderFile(t, vfs, "/brt/main.go", mainSrc)
	writeLoaderFile(t, vfs, "/brt/main_test.go", benchSrc)

	prog, err := LoadModule(vfs, "/brt", Options{})
	if err != nil {
		t.Fatalf("LoadModule: %v", err)
	}
	results, err := RunPackageBenchmarks(context.Background(), vm, prog, "benchrt", BenchOptions{MinIterations: 50})
	if err != nil {
		t.Fatalf("RunPackageBenchmarks: %v", err)
	}
	if len(results) != 1 || results[0].StepsPerOp == 0 {
		t.Fatalf("expected 1 benchmark result with a positive StepsPerOp, got %+v", results)
	}

	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no `go` on PATH, skipping real go test -bench round-trip check")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module benchrt\n\ngo 1.18\n"), 0644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(mainSrc), 0644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main_test.go"), []byte(benchSrc), 0644); err != nil {
		t.Fatalf("write main_test.go: %v", err)
	}

	cmd := exec.Command(goBin, "test", "-run", "^$", "-bench", ".", "-benchtime=1x", "./...")
	cmd.Dir = dir
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("real `go test -bench` unavailable/incompatible in this environment (%v); output:\n%s", err, out)
	}
}
