package loader

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"

	"simonwaldherr.de/go/nanogo/interp"
)

// ReplaceFunction parses newSource as a standalone top-level function
// declaration and replaces name in package pkgName with it, without
// re-parsing or rebuilding the rest of the program. The new function takes
// effect immediately for every future call — including calls made through
// another package's existing import alias, since a package's exported
// functions are looked up through one shared map (see
// interp.PackageScope.Exports), never a per-importer snapshot copy.
//
// The program must already be built (via RunProgram, RunFunctionTest, or
// RunFunctionBench) before hot-swapping into it.
func ReplaceFunction(vm *interp.Interpreter, prog *Program, pkgName, name, newSource string) error {
	if prog.built == nil {
		return fmt.Errorf("nanogo/loader: ReplaceFunction: %s.%s: program has not been built yet (call RunProgram/RunFunctionTest/RunFunctionBench first)", pkgName, name)
	}
	dir, ok := findPackageDirByName(prog, pkgName)
	if !ok {
		return fmt.Errorf("nanogo/loader: unknown package %q", pkgName)
	}
	scope, ok := prog.built[dir]
	if !ok {
		return fmt.Errorf("nanogo/loader: internal error: package %q was not built", pkgName)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name+".go", "package "+pkgName+"\n"+newSource, parser.SkipObjectResolution)
	if err != nil {
		return fmt.Errorf("nanogo/loader: ReplaceFunction: %s.%s: %w", pkgName, name, err)
	}

	var target *ast.FuncDecl
	for _, decl := range file.Decls {
		d, ok := decl.(*ast.FuncDecl)
		if !ok || d.Name.Name != name || (d.Recv != nil && len(d.Recv.List) > 0) {
			continue
		}
		target = d
		break
	}
	if target == nil {
		return fmt.Errorf("nanogo/loader: ReplaceFunction: newSource does not declare a top-level function named %q", name)
	}

	scope.Replace(name, scope.BuildFunction(target))
	return nil
}
