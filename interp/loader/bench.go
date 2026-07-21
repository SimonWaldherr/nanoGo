package loader

import (
	"context"
	"fmt"
	"go/ast"
	"strings"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

// BenchOptions configures a benchmark run. There is deliberately no
// wall-clock auto-scaling loop (real `go test -bench` grows N until it runs
// for about a second): that would make StepsPerOp vary run-to-run purely
// from scheduling noise. MinIterations is used as-is, defaulting to 1.
type BenchOptions struct {
	MinIterations int
}

// BenchResult reports a benchmark's cost. StepsPerOp is the primary metric:
// it is derived from the interpreter's own step counter (see
// interp.Interpreter.StepCount), which counts evaluator checkpoints, not CPU
// time — identical across machines and runs for the same code and N.
// WallNsPerOp is secondary, informational wall-clock timing.
type BenchResult struct {
	StepsPerOp  uint64
	WallNsPerOp float64
	Iterations  int
}

// RunFunctionBench calls the exported, no-argument function "Func" in
// package "pkg" (target is "pkg.Func") opts.MinIterations times inside one
// execution, and reports steps/wall-time per call.
func RunFunctionBench(ctx context.Context, vm *interp.Interpreter, prog *Program, target string, opts BenchOptions) (BenchResult, error) {
	if err := ensureBuilt(ctx, vm, prog); err != nil {
		return BenchResult{}, err
	}
	pkgName, funcName, ok := splitTarget(target)
	if !ok {
		return BenchResult{}, fmt.Errorf("nanogo/loader: invalid target %q, want \"pkg.Func\"", target)
	}
	dir, ok := findPackageDirByName(prog, pkgName)
	if !ok {
		return BenchResult{}, fmt.Errorf("nanogo/loader: unknown package %q", pkgName)
	}
	scope := prog.built[dir]
	v, ok := scope.Lookup(funcName)
	if !ok {
		return BenchResult{}, fmt.Errorf("nanogo/loader: function %q not found in package %q", funcName, pkgName)
	}
	fn, ok := v.(*interp.Function)
	if !ok {
		return BenchResult{}, fmt.Errorf("nanogo/loader: %q is not a function", target)
	}

	n := opts.MinIterations
	if n <= 0 {
		n = 1
	}

	var steps uint64
	var wallNs float64
	fset := prog.Packages[dir].FSet
	err := vm.WithExecution(ctx, fset, func() error {
		start := time.Now()
		for i := 0; i < n; i++ {
			if _, err := vm.Invoke(fn, nil); err != nil {
				return err
			}
		}
		elapsed := time.Since(start)
		steps = vm.StepCount() / uint64(n)
		wallNs = float64(elapsed.Nanoseconds()) / float64(n)
		return nil
	})
	if err != nil {
		return BenchResult{}, err
	}
	return BenchResult{StepsPerOp: steps, WallNsPerOp: wallNs, Iterations: n}, nil
}

// RunPackageBenchmarks discovers every BenchmarkXxx(b *testing.B) function
// among pkgName's already-parsed _test.go files and runs each once with a
// fresh *testing.B whose N is opts.MinIterations — the guest's own internal
// `for i := 0; i < b.N; i++ { ... }` loop performs the repetition, exactly
// like a real `go test -bench` run, just without wall-clock auto-scaling.
func RunPackageBenchmarks(ctx context.Context, vm *interp.Interpreter, prog *Program, pkgName string, opts BenchOptions) ([]BenchResult, error) {
	if err := ensureBuilt(ctx, vm, prog); err != nil {
		return nil, err
	}
	dir, ok := findPackageDirByName(prog, pkgName)
	if !ok {
		return nil, fmt.Errorf("nanogo/loader: unknown package %q", pkgName)
	}
	pp := prog.Packages[dir]
	scope := prog.built[dir]

	n := opts.MinIterations
	if n <= 0 {
		n = 1
	}

	var results []BenchResult
	for _, file := range pp.TestFiles {
		for _, decl := range file.Decls {
			d, ok := decl.(*ast.FuncDecl)
			if !ok || d.Recv != nil || !strings.HasPrefix(d.Name.Name, "Benchmark") {
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

			bv := vm.NewTestB(n)
			var steps uint64
			err := vm.WithExecution(ctx, pp.FSet, func() error {
				if _, err := vm.Invoke(fn, []any{bv}); err != nil {
					return err
				}
				steps = vm.StepCount() / uint64(n)
				return nil
			})
			if err != nil {
				return results, fmt.Errorf("nanogo/loader: %s: %w", d.Name.Name, err)
			}
			wallNs := float64(interp.BenchElapsed(bv).Nanoseconds()) / float64(n)
			results = append(results, BenchResult{StepsPerOp: steps, WallNsPerOp: wallNs, Iterations: n})
		}
	}
	return results, nil
}
