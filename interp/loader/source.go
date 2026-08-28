package loader

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"

	"simonwaldherr.de/go/nanogo/interp"
)

// RunSourceTests runs TestXxx functions from one editor source document. It
// splits normal declarations and test functions into a temporary VFS module,
// then delegates to the normal multi-file loader/test runner. This gives a
// browser editor the same testing.T subset and result classification as a
// real VFS project without inventing a second execution path.
//
// The supplied vm must have a VFS. The temporary files are removed before
// return; callers should not retain the resulting Program (none is exposed).
func RunSourceTests(ctx context.Context, vm *interp.Interpreter, source string) ([]TestResult, error) {
	return RunSourceTestsMatching(ctx, vm, source, "")
}

// RunSourceTestsMatching is RunSourceTests with an optional `go test -run`
// style regular-expression filter for top-level TestXxx functions.
func RunSourceTestsMatching(ctx context.Context, vm *interp.Interpreter, source, match string) ([]TestResult, error) {
	if vm == nil || vm.VFS == nil {
		return nil, fmt.Errorf("nanogo/loader: RunSourceTests needs an Interpreter with a VFS")
	}
	normal, tests, packageName, err := splitSourceTests(source)
	if err != nil {
		return nil, err
	}
	if packageName == "" {
		return nil, fmt.Errorf("nanogo/loader: source has no package declaration")
	}

	const root = "/tmp/nanogo-source-tests"
	if err := vm.VFS.RemoveAll(root); err != nil {
		return nil, err
	}
	defer vm.VFS.RemoveAll(root)
	if err := vm.VFS.MkdirAll(root, 0755); err != nil {
		return nil, err
	}
	if err := vm.VFS.WriteFile(root+"/go.mod", []byte("module nanogo.local/source-tests\n"), 0644); err != nil {
		return nil, err
	}
	if err := vm.VFS.WriteFile(root+"/main.go", normal, 0644); err != nil {
		return nil, err
	}
	if err := vm.VFS.WriteFile(root+"/main_test.go", tests, 0644); err != nil {
		return nil, err
	}

	prog, err := LoadModule(vm.VFS, root, Options{})
	if err != nil {
		return nil, err
	}
	return RunPackageTestsMatching(ctx, vm, prog, packageName, match)
}

func splitSourceTests(source string) (normal, tests []byte, packageName string, err error) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "editor.go", source, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, "", err
	}
	packageName = file.Name.Name
	normalDecls := make([]ast.Decl, 0, len(file.Decls))
	testDecls := make([]ast.Decl, 0)
	for _, decl := range file.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && isTestName(fn.Name.Name) {
			testDecls = append(testDecls, decl)
			continue
		}
		normalDecls = append(normalDecls, decl)
	}

	normal, err = renderSourceFile(fset, packageName, normalDecls)
	if err != nil {
		return nil, nil, "", err
	}
	// Test files need the same imports as the editor file; keeping an unused
	// import is harmless in nanoGo's intentionally typeless subset, while it
	// lets tests refer to production imports without a hidden rewrite.
	testDecls = append(importDecls(file.Decls), testDecls...)
	tests, err = renderSourceFile(fset, packageName, testDecls)
	if err != nil {
		return nil, nil, "", err
	}
	return normal, tests, packageName, nil
}

func importDecls(decls []ast.Decl) []ast.Decl {
	imports := make([]ast.Decl, 0)
	for _, decl := range decls {
		if gd, ok := decl.(*ast.GenDecl); ok && gd.Tok == token.IMPORT {
			imports = append(imports, decl)
		}
	}
	return imports
}

func renderSourceFile(fset *token.FileSet, packageName string, decls []ast.Decl) ([]byte, error) {
	file := &ast.File{Name: ast.NewIdent(packageName), Decls: decls}
	var out bytes.Buffer
	if err := format.Node(&out, fset, file); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
