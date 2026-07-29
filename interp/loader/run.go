package loader

import (
	"context"
	"fmt"

	"simonwaldherr.de/go/nanogo/interp"
)

// ensureBuilt builds every package in prog against vm exactly once, in
// dependency order (prog.Order): import wiring, two-phase CollectDecls/
// EvalDecls across every file, then RunInit. The whole sequence runs inside
// one vm.WithExecution, so package-level var initializers and init()
// functions are covered by the same step-limit and cancellation guarantees
// as any other execution — not left to run unbounded. The resulting
// PackageScopes are cached on prog, so RunProgram/RunFunctionTest/
// RunFunctionBench/ReplaceFunction all share one build.
//
// A later call to RunFunctionTest or RunFunctionBench invokes the target
// function through its own, separate vm.CallEntry — deliberately a new
// execution with a fresh step budget per call, matching how `go test` runs
// each test in isolation rather than sharing one budget with package
// initialization or with other test cases.
func ensureBuilt(ctx context.Context, vm *interp.Interpreter, prog *Program) error {
	if prog.built != nil {
		return nil
	}
	built := map[string]*interp.PackageScope{}
	if err := buildPackageScopes(ctx, vm, prog, prog.Order, built); err != nil {
		return err
	}
	prog.built = built
	return nil
}

// ensureTestDependencies adds packages referenced only from _test.go files to
// an already-built production graph. It is deliberately lazy: RunProgram and
// RunFunctionTest never initialize a test helper package merely because a
// test happens to import it.
func ensureTestDependencies(ctx context.Context, vm *interp.Interpreter, prog *Program) error {
	if err := ensureBuilt(ctx, vm, prog); err != nil {
		return err
	}
	return buildPackageScopes(ctx, vm, prog, prog.TestOrder, prog.built)
}

func buildPackageScopes(ctx context.Context, vm *interp.Interpreter, prog *Program, order []string, built map[string]*interp.PackageScope) error {
	entryPkg, ok := prog.Packages[prog.Entry]
	if !ok {
		return fmt.Errorf("nanogo/loader: entry package %s not found", prog.Entry)
	}
	return vm.WithExecution(ctx, entryPkg.FSet, func() error {
		for _, dir := range order {
			if _, exists := built[dir]; exists {
				continue
			}
			pp := prog.Packages[dir]
			ps := vm.NewPackageScope(pp.Name)

			if err := bindImports(vm, ps, pp.Imports, built, dir); err != nil {
				return err
			}

			for _, f := range pp.Files {
				if err := ps.CollectDecls(f, pp.FSet); err != nil {
					return fmt.Errorf("nanogo/loader: %s: %w", dir, err)
				}
			}
			for _, f := range pp.Files {
				if err := ps.EvalDecls(ctx, f); err != nil {
					return fmt.Errorf("nanogo/loader: %s: %w", dir, err)
				}
			}
			if err := ps.RunInit(ctx); err != nil {
				return fmt.Errorf("nanogo/loader: %s: %w", dir, err)
			}

			built[dir] = ps
		}
		return nil
	})
}

// bindImports wires one package scope to its already-built local dependencies
// and its registered curated packages. It is shared by normal and external
// test packages, which deliberately have distinct import environments.
func bindImports(vm *interp.Interpreter, ps *interp.PackageScope, imports []ImportEdge, built map[string]*interp.PackageScope, dir string) error {
	for _, imp := range imports {
		var pkg *interp.Package
		if imp.Builtin {
			p, ok := vm.Package(imp.Path)
			if !ok {
				return fmt.Errorf("nanogo/loader: builtin package %q is not registered on this Interpreter (call interp.RegisterBuiltinPackages first)", imp.Path)
			}
			pkg = p
		} else {
			depScope, ok := built[imp.Dir]
			if !ok {
				return fmt.Errorf("nanogo/loader: internal error: dependency %s not built before %s", imp.Dir, dir)
			}
			pkg = depScope.Exports()
		}
		ps.Import(imp.Alias, pkg)
	}
	return nil
}

// ensureInternalTests initializes the package foo test overlay lazily. The
// production package is always built first; only a test run evaluates its
// test-only variables or invokes test-only init functions.
func ensureInternalTests(ctx context.Context, vm *interp.Interpreter, prog *Program, dir string) (*interp.PackageScope, error) {
	if err := ensureTestDependencies(ctx, vm, prog); err != nil {
		return nil, err
	}
	if prog.testsBuilt != nil && prog.testsBuilt[dir] {
		return prog.built[dir], nil
	}
	pp := prog.Packages[dir]
	ps := prog.built[dir]
	err := vm.WithExecution(ctx, pp.FSet, func() error {
		if err := bindImports(vm, ps, pp.TestImports, prog.built, dir); err != nil {
			return err
		}
		for _, f := range pp.TestFiles {
			if err := ps.CollectDecls(f, pp.FSet); err != nil {
				return fmt.Errorf("nanogo/loader: %s (test): %w", dir, err)
			}
		}
		for _, f := range pp.TestFiles {
			if err := ps.EvalDecls(ctx, f); err != nil {
				return fmt.Errorf("nanogo/loader: %s (test): %w", dir, err)
			}
		}
		return ps.RunInit(ctx)
	})
	if err != nil {
		return nil, err
	}
	if prog.testsBuilt == nil {
		prog.testsBuilt = map[string]bool{}
	}
	prog.testsBuilt[dir] = true
	return ps, nil
}

// ensureExternalTests initializes package foo_test as a distinct scope. It
// sees foo only through its exported package object, matching a real external
// Go test and preventing accidental use of unexported production symbols.
func ensureExternalTests(ctx context.Context, vm *interp.Interpreter, prog *Program, dir string) (*interp.PackageScope, error) {
	if err := ensureTestDependencies(ctx, vm, prog); err != nil {
		return nil, err
	}
	pp := prog.Packages[dir]
	if len(pp.ExternalTestFiles) == 0 {
		return nil, nil
	}
	if prog.externalTestScopes != nil {
		if ps := prog.externalTestScopes[dir]; ps != nil {
			return ps, nil
		}
	}
	ps := vm.NewPackageScope(pp.ExternalTestName)
	err := vm.WithExecution(ctx, pp.FSet, func() error {
		if err := bindImports(vm, ps, pp.ExternalTestImports, prog.built, dir); err != nil {
			return err
		}
		for _, f := range pp.ExternalTestFiles {
			if err := ps.CollectDecls(f, pp.FSet); err != nil {
				return fmt.Errorf("nanogo/loader: %s (external test): %w", dir, err)
			}
		}
		for _, f := range pp.ExternalTestFiles {
			if err := ps.EvalDecls(ctx, f); err != nil {
				return fmt.Errorf("nanogo/loader: %s (external test): %w", dir, err)
			}
		}
		return ps.RunInit(ctx)
	})
	if err != nil {
		return nil, err
	}
	if prog.externalTestScopes == nil {
		prog.externalTestScopes = map[string]*interp.PackageScope{}
	}
	prog.externalTestScopes[dir] = ps
	return ps, nil
}

// RunProgram builds every package in prog (see ensureBuilt) and calls the
// named entry function (default "main") in the entry package, exactly like
// RunContext's own main() invocation.
func RunProgram(ctx context.Context, vm *interp.Interpreter, prog *Program, entry string) error {
	if entry == "" {
		entry = "main"
	}
	if err := ensureBuilt(ctx, vm, prog); err != nil {
		return err
	}
	entryScope := prog.built[prog.Entry]
	v, ok := entryScope.Lookup(entry)
	if !ok {
		return fmt.Errorf("nanogo/loader: entry function %q not found in package %s", entry, prog.Packages[prog.Entry].Name)
	}
	fn, ok := v.(*interp.Function)
	if !ok {
		return fmt.Errorf("nanogo/loader: %q is not a function", entry)
	}
	_, err := vm.CallEntry(ctx, prog.Packages[prog.Entry].FSet, fn, nil)
	return err
}

// findPackageDirByName returns the VFS directory of the first discovered
// package whose declared Go package name matches name.
func findPackageDirByName(prog *Program, name string) (string, bool) {
	for _, dir := range prog.Order {
		if prog.Packages[dir].Name == name {
			return dir, true
		}
	}
	return "", false
}

// splitTarget splits "pkg.Func" into its package and function name.
func splitTarget(target string) (pkg, fn string, ok bool) {
	for i := len(target) - 1; i >= 0; i-- {
		if target[i] == '.' {
			return target[:i], target[i+1:], target[:i] != "" && target[i+1:] != ""
		}
	}
	return "", "", false
}
