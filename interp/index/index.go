// Package index provides static, typeless analysis over a VFS-resident
// nanoGo module: one FunctionEntry per function/method, with a best-effort
// call graph and simple AST-based complexity metrics. It uses only
// go/parser and go/ast — no go/types, no process spawning — so it compiles
// with GOOS=js GOARCH=wasm like every other nanoGo package.
//
// index does its own lightweight recursive directory walk rather than
// depending on interp/loader's entry-driven, import-graph BFS: Scan is meant
// to cover every package under root, not just those reachable from one
// entry point, so the traversal shapes differ enough that sharing loader's
// discovery would be the wrong fit. It does share interp.ParsePackageDirFull
// and interp.BuiltinImportPaths with the loader package, though, so both
// stay consistent about what counts as a "local" vs. "builtin" import.
package index

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"path"
	"sort"
	"strings"

	"simonwaldherr.de/go/nanogo/interp"
)

// Options configures Scan. Reserved for future filtering (e.g. excluding
// directories); empty today.
type Options struct{}

// FunctionEntry describes one function or method declaration.
type FunctionEntry struct {
	ID        string // "pkg.Name", or "pkg.(Receiver).Name" for a method
	Name      string
	Package   string
	File      string
	LineStart int
	LineEnd   int
	Signature string
	Doc       string
	Code      string
	Receiver  string
	Calls     []string
	CalledBy  []string
	Tests     []string
	Metrics   Metrics
}

// Metrics are simple, purely AST-based complexity indicators.
type Metrics struct {
	CyclomaticComplexity int
	MaxNestingDepth      int
	LOC                  int
}

// callRef is one call-expression reference collected while scanning a
// function body, resolved against known symbols in a later pass.
type callRef struct {
	Qualifier string // import alias / receiver-expression identifier, or "" for a bare call
	Name      string
}

// Scan walks every package directory under root (recursively) and returns
// one FunctionEntry per function/method declaration across the whole tree,
// including _test.go files, with Calls/CalledBy/Tests populated by matching
// CallExpr nodes against known symbols on a best-effort basis (no type
// information is available to disambiguate an identically-named method on
// two different receiver types).
func Scan(vfs *interp.VFS, root string, opts Options) ([]FunctionEntry, error) {
	dirs, err := discoverPackageDirs(vfs, path.Clean(root))
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)

	var entries []FunctionEntry
	var refs [][]callRef

	for _, dir := range dirs {
		files, testFiles, fset, err := interp.ParsePackageDirFull(vfs, dir)
		if err != nil {
			return nil, fmt.Errorf("nanogo/index: parsing %s: %w", dir, err)
		}
		pkgName := packageName(files, testFiles)
		if pkgName == "" {
			continue
		}
		e, r := scanFuncs(dir, pkgName, files, fset)
		entries = append(entries, e...)
		refs = append(refs, r...)
		e, r = scanFuncs(dir, pkgName, testFiles, fset)
		entries = append(entries, e...)
		refs = append(refs, r...)
	}

	buildCallGraph(entries, refs)
	return entries, nil
}

func packageName(files, testFiles []*ast.File) string {
	for _, f := range files {
		return f.Name.Name
	}
	for _, f := range testFiles {
		return f.Name.Name
	}
	return ""
}

func discoverPackageDirs(vfs *interp.VFS, root string) ([]string, error) {
	var dirs []string
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := vfs.ReadDir(dir)
		if err != nil {
			return err
		}
		hasGo := false
		var subdirs []string
		for _, e := range entries {
			if e.IsDir {
				subdirs = append(subdirs, path.Join(dir, e.Name))
				continue
			}
			if strings.HasSuffix(e.Name, ".go") {
				hasGo = true
			}
		}
		if hasGo {
			dirs = append(dirs, dir)
		}
		for _, sub := range subdirs {
			if err := walk(sub); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return dirs, nil
}

func scanFuncs(dir, pkgName string, files []*ast.File, fset *token.FileSet) ([]FunctionEntry, [][]callRef) {
	var entries []FunctionEntry
	var refs [][]callRef
	for _, file := range files {
		for _, decl := range file.Decls {
			d, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			receiver := ""
			id := pkgName + "." + d.Name.Name
			if d.Recv != nil && len(d.Recv.List) > 0 {
				receiver = strings.TrimPrefix(exprString(d.Recv.List[0].Type), "*")
				id = pkgName + ".(" + receiver + ")." + d.Name.Name
			}

			start := fset.Position(d.Pos())
			end := fset.Position(d.End())

			doc := ""
			if d.Doc != nil {
				doc = strings.TrimSpace(d.Doc.Text())
			}

			sigDecl := &ast.FuncDecl{Recv: d.Recv, Name: d.Name, Type: d.Type}
			var sigBuf bytes.Buffer
			signature := ""
			if err := format.Node(&sigBuf, fset, sigDecl); err == nil {
				signature = sigBuf.String()
			}

			var codeBuf bytes.Buffer
			code := ""
			if err := format.Node(&codeBuf, fset, d); err == nil {
				code = codeBuf.String()
			}

			entries = append(entries, FunctionEntry{
				ID:        id,
				Name:      d.Name.Name,
				Package:   pkgName,
				File:      start.Filename,
				LineStart: start.Line,
				LineEnd:   end.Line,
				Signature: signature,
				Doc:       doc,
				Code:      code,
				Receiver:  receiver,
				Metrics:   computeMetricsWithLOC(d, start.Line, end.Line),
			})
			refs = append(refs, collectCallRefs(d))
		}
	}
	return entries, refs
}

func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.SelectorExpr:
		return exprString(t.Sel)
	case *ast.IndexExpr:
		return exprString(t.X)
	}
	return ""
}
