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
	entryPkg, ok := prog.Packages[prog.Entry]
	if !ok {
		return fmt.Errorf("nanogo/loader: entry package %s not found", prog.Entry)
	}

	built := map[string]*interp.PackageScope{}
	err := vm.WithExecution(ctx, entryPkg.FSet, func() error {
		for _, dir := range prog.Order {
			pp := prog.Packages[dir]
			ps := vm.NewPackageScope(pp.Name)

			for _, imp := range pp.Imports {
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

			for _, f := range pp.Files {
				if err := ps.CollectDecls(f, pp.FSet); err != nil {
					return fmt.Errorf("nanogo/loader: %s: %w", dir, err)
				}
			}
			// _test.go files are collected as an overlay onto the same
			// scope, so test functions see the package's own private
			// symbols exactly like real Go — but only their decls; their
			// var initializers/init() are not part of the package's own
			// startup (RunPackageTests evaluates them lazily, once, the
			// first time that package's tests run).
			for _, f := range pp.TestFiles {
				if err := ps.CollectDecls(f, pp.FSet); err != nil {
					return fmt.Errorf("nanogo/loader: %s (test): %w", dir, err)
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
	if err != nil {
		return err
	}
	prog.built = built
	return nil
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
