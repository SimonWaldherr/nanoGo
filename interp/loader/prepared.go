package loader

import (
	"context"
	"fmt"
	"simonwaldherr.de/go/nanogo/interp"
)

// PreparedProgram owns a private immutable parsed module and filesystem
// snapshot. It reuses parsing/import resolution, never live package scopes.
// Runs may execute concurrently: each receives its own Interpreter and VFS.
// Configuration callbacks must also respect the caller's concurrency policy.
type PreparedProgram struct {
	program *Program
	fs      *interp.VFS
}

// PrepareModule snapshots all source/dependencies and configuration. Changes to
// the original VFS or Options after this call take effect only on a new Prepare.
// ModuleCache uses the same cloneProgram metadata sharing rules for warm loads.
func PrepareModule(fs *interp.VFS, root string, opts Options) (*PreparedProgram, error) {
	if fs == nil {
		return nil, interp.WithDiagnostic(fmt.Errorf("nanogo/loader: nil VFS"), "load")
	}
	snapshot := fs.Clone()
	cache := NewModuleCache(snapshot)
	program, err := cache.Load(root, opts)
	if err != nil {
		return nil, interp.WithDiagnostic(err, "load")
	}
	return &PreparedProgram{program, snapshot}, nil
}

// RunOptions is configuration for one isolated run. Configure is invoked once
// on the fresh VM, before inputs are bound; register callbacks/capabilities here.
// It must not retain and mutate the VM during execution.
type RunOptions struct {
	Inputs    map[string]any
	Entry     string
	Configure func(*interp.Interpreter) error
}
type RunResult struct {
	Results    interp.ExecutionResults `json:"results"`
	Steps      uint64                  `json:"steps"`
	Diagnostic *interp.Diagnostic      `json:"diagnostic,omitempty"`
}

// Run initializes every package afresh, then invokes Entry (default main), under
// ONE shared execution budget. Legacy RunProgram retains separate init/entry
// budgets and its mutable one-VM Program contract.
func (p *PreparedProgram) Run(ctx context.Context, opts RunOptions) (RunResult, error) {
	if p == nil || p.program == nil {
		return RunResult{}, fmt.Errorf("nanogo/loader: nil prepared program")
	}
	vm := interp.NewInterpreterWithVFS(p.fs.Clone())
	interp.RegisterBuiltinPackages(vm)
	if opts.Configure != nil {
		if err := opts.Configure(vm); err != nil {
			return RunResult{Diagnostic: interp.DiagnosticFor(err, "host")}, err
		}
	}
	if err := vm.BindInputs(opts.Inputs); err != nil {
		return RunResult{Diagnostic: interp.DiagnosticFor(err, "host")}, err
	}
	program := cloneProgram(p.program)
	entry := opts.Entry
	if entry == "" {
		entry = "main"
	}
	built := map[string]*interp.PackageScope{}
	err := vm.WithExecution(ctx, program.Packages[program.Entry].FSet, func() error {
		if err := buildPackageScopesInExecution(ctx, vm, program, program.Order, built); err != nil {
			return interp.WithDiagnostic(err, "load")
		}
		value, ok := built[program.Entry].Lookup(entry)
		if !ok {
			return fmt.Errorf("nanogo/loader: missing entry %q", entry)
		}
		fn, ok := value.(*interp.Function)
		if !ok {
			return fmt.Errorf("nanogo/loader: entry %q is not a function", entry)
		}
		_, err := vm.Invoke(fn, nil)
		return err
	})
	return RunResult{Results: vm.LastResults(), Steps: vm.LastStepCount(), Diagnostic: interp.DiagnosticFor(err, "runtime")}, err
}

// PrepareSource is the single-file convenience form of PrepareModule. Imports
// are limited to registered builtin packages; use PrepareModule for local code.
func PrepareSource(source string) (*PreparedProgram, error) {
	fs := interp.NewVFS()
	_ = fs.MkdirAll("/program", 0755)
	_ = fs.WriteFile("/program/go.mod", []byte("module nanogo.local/prepared\n"), 0644)
	_ = fs.WriteFile("/program/main.go", []byte(source), 0644)
	return PrepareModule(fs, "/program", Options{})
}
