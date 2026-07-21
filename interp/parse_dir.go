// interp/parse_dir.go
package interp

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path"
	"sort"
	"strings"
)

// ParsePackageDir parses every non-test .go file directly under dir in the
// given VFS (not the host filesystem — consistent with nanoGo's capability
// model, VFS access here is unconditional since this is a parse-only, no
// I/O-to-the-host operation) and returns them alongside the FileSet used to
// parse them. Files are read in sorted-name order for deterministic
// diagnostics; declaration order across files does not matter to a
// PackageScope, since CollectDecls/EvalDecls are explicitly two-phase.
//
// _test.go files are intentionally excluded here — see ParsePackageTestFiles
// and ParsePackageDirFull for callers that also need them.
func ParsePackageDir(vfs *VFS, dir string) ([]*ast.File, *token.FileSet, error) {
	nonTest, _, err := listGoFileNames(vfs, dir)
	if err != nil {
		return nil, nil, err
	}
	fset := token.NewFileSet()
	files, err := parseFilesInto(vfs, fset, dir, nonTest)
	return files, fset, err
}

// ParsePackageTestFiles parses every _test.go file directly under dir,
// analogous to ParsePackageDir but for the test-only overlay.
func ParsePackageTestFiles(vfs *VFS, dir string) ([]*ast.File, *token.FileSet, error) {
	_, testNames, err := listGoFileNames(vfs, dir)
	if err != nil {
		return nil, nil, err
	}
	fset := token.NewFileSet()
	files, err := parseFilesInto(vfs, fset, dir, testNames)
	return files, fset, err
}

// ParsePackageDirFull parses both non-test and _test.go files directly under
// dir into one shared FileSet, so positions across a package's normal files
// and its test overlay are comparable (needed by interp/loader's test
// runner and interp/index's static analysis).
func ParsePackageDirFull(vfs *VFS, dir string) (files, testFiles []*ast.File, fset *token.FileSet, err error) {
	nonTest, testNames, err := listGoFileNames(vfs, dir)
	if err != nil {
		return nil, nil, nil, err
	}
	fset = token.NewFileSet()
	files, err = parseFilesInto(vfs, fset, dir, nonTest)
	if err != nil {
		return nil, nil, nil, err
	}
	testFiles, err = parseFilesInto(vfs, fset, dir, testNames)
	if err != nil {
		return nil, nil, nil, err
	}
	return files, testFiles, fset, nil
}

func listGoFileNames(vfs *VFS, dir string) (nonTest, test []string, err error) {
	entries, err := vfs.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if e.IsDir || !strings.HasSuffix(e.Name, ".go") {
			continue
		}
		if strings.HasSuffix(e.Name, "_test.go") {
			test = append(test, e.Name)
		} else {
			nonTest = append(nonTest, e.Name)
		}
	}
	sort.Strings(nonTest)
	sort.Strings(test)
	return nonTest, test, nil
}

func parseFilesInto(vfs *VFS, fset *token.FileSet, dir string, names []string) ([]*ast.File, error) {
	files := make([]*ast.File, 0, len(names))
	for _, name := range names {
		full := path.Join(dir, name)
		data, err := vfs.ReadFile(full)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(fset, full, data, parser.ParseComments)
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}
