package loader

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
	"sort"
	"strings"

	"simonwaldherr.de/go/nanogo/interp"
)

// Options configures LoadModule.
type Options struct {
	// ModulePath maps local import paths onto VFS directories (e.g.
	// "github.com/foo/bar" + import ".../utils" -> VFS path root+"/utils").
	// If root/go.mod exists, its "module" line takes precedence.
	ModulePath string

	// EntryDir is the VFS directory of the program's entry package. Empty
	// defaults to root itself.
	EntryDir string
}

// ImportEdge is one resolved import within a package.
type ImportEdge struct {
	Alias   string
	Path    string
	Builtin bool
	Dir     string // resolved local VFS directory; empty when Builtin
}

// ParsedPackage is one discovered, parsed package directory: every .go file
// (split into normal and _test.go files) plus its resolved import edges.
type ParsedPackage struct {
	Dir       string
	Name      string
	Files     []*ast.File
	TestFiles []*ast.File
	FSet      *token.FileSet
	Imports   []ImportEdge
}

// Program is the result of LoadModule: every local package reachable from
// the entry package, plus a dependency-first topological order.
type Program struct {
	ModulePath string
	Root       string
	Entry      string // Dir of the entry package
	Packages   map[string]*ParsedPackage
	Order      []string // dependency-first: a package's dependencies always precede it

	// built caches the PackageScopes produced by ensureBuilt (see run.go)
	// against whichever *interp.Interpreter first executes this Program.
	// Rebuilding against a second, different Interpreter is not supported —
	// build a fresh Program (LoadModule again) per Interpreter instead.
	built map[string]*interp.PackageScope
}

// LoadModule discovers, parses, and import-resolves every local package
// reachable from the entry package. It does not execute or evaluate
// anything — no *interp.Interpreter is involved here; see RunProgram for
// that step. Import paths are resolved against opts.ModulePath (or
// root/go.mod's module line, which wins if present) for local packages, and
// against interp.BuiltinImportPaths for curated builtins. Anything else is a
// hard, immediate error — never a silent best-effort skip.
func LoadModule(vfs *interp.VFS, root string, opts Options) (*Program, error) {
	root = path.Clean(root)
	modulePath := opts.ModulePath
	if data, err := vfs.ReadFile(path.Join(root, "go.mod")); err == nil {
		mp, perr := ParseModulePath(data)
		if perr != nil {
			return nil, fmt.Errorf("nanogo/loader: %s/go.mod: %w", root, perr)
		}
		modulePath = mp
	}
	if modulePath == "" {
		return nil, fmt.Errorf("nanogo/loader: no module path: %s has no go.mod and Options.ModulePath is empty", root)
	}

	entryDir := opts.EntryDir
	if entryDir == "" {
		entryDir = root
	}
	entryDir = path.Clean(entryDir)

	packages := map[string]*ParsedPackage{}
	queue := []string{entryDir}
	queued := map[string]bool{entryDir: true}

	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]

		files, testFiles, fset, err := interp.ParsePackageDirFull(vfs, dir)
		if err != nil {
			return nil, fmt.Errorf("nanogo/loader: parsing package at %s: %w", dir, err)
		}
		if len(files) == 0 {
			return nil, fmt.Errorf("nanogo/loader: no .go files found in package directory %s", dir)
		}
		pkgName := files[0].Name.Name
		for _, f := range files {
			if f.Name.Name != pkgName {
				return nil, fmt.Errorf("nanogo/loader: package directory %s has mixed package names %q and %q", dir, pkgName, f.Name.Name)
			}
		}

		pp := &ParsedPackage{Dir: dir, Name: pkgName, Files: files, TestFiles: testFiles, FSet: fset}

		seen := map[string]bool{}
		for _, f := range files {
			for _, spec := range f.Imports {
				importPath := strings.Trim(spec.Path.Value, `"`)
				if seen[importPath] {
					continue
				}
				seen[importPath] = true

				alias := ""
				if spec.Name != nil {
					alias = spec.Name.Name
				} else {
					segs := strings.Split(importPath, "/")
					alias = segs[len(segs)-1]
				}

				if interp.IsBuiltinImport(importPath) {
					pp.Imports = append(pp.Imports, ImportEdge{Alias: alias, Path: importPath, Builtin: true})
					continue
				}

				depDir, ok := resolveLocalImport(modulePath, root, importPath)
				if !ok {
					return nil, fmt.Errorf("nanogo/loader: import %q in package %s is neither a module-local package (module %q) nor a recognized builtin", importPath, dir, modulePath)
				}
				pp.Imports = append(pp.Imports, ImportEdge{Alias: alias, Path: importPath, Dir: depDir})
				if !queued[depDir] {
					queued[depDir] = true
					queue = append(queue, depDir)
				}
			}
		}

		packages[dir] = pp
	}

	order, err := topoSort(packages)
	if err != nil {
		return nil, err
	}

	return &Program{ModulePath: modulePath, Root: root, Entry: entryDir, Packages: packages, Order: order}, nil
}

func resolveLocalImport(modulePath, root, importPath string) (string, bool) {
	if importPath == modulePath {
		return root, true
	}
	prefix := modulePath + "/"
	if !strings.HasPrefix(importPath, prefix) {
		return "", false
	}
	rel := strings.TrimPrefix(importPath, prefix)
	return path.Join(root, rel), true
}

// topoSort returns a dependency-first order over packages: every package's
// local import dependencies appear before it. A cycle produces a clear
// error naming the chain instead of recursing forever.
func topoSort(packages map[string]*ParsedPackage) ([]string, error) {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var order []string
	var stack []string

	var visit func(dir string) error
	visit = func(dir string) error {
		switch color[dir] {
		case black:
			return nil
		case gray:
			idx := 0
			for i, d := range stack {
				if d == dir {
					idx = i
					break
				}
			}
			cycle := append(append([]string{}, stack[idx:]...), dir)
			return fmt.Errorf("nanogo/loader: import cycle: %s", strings.Join(cycle, " -> "))
		}
		color[dir] = gray
		stack = append(stack, dir)
		pkg, ok := packages[dir]
		if !ok {
			return fmt.Errorf("nanogo/loader: internal error: unknown package directory %s", dir)
		}
		for _, imp := range pkg.Imports {
			if imp.Builtin {
				continue
			}
			if err := visit(imp.Dir); err != nil {
				return err
			}
		}
		stack = stack[:len(stack)-1]
		color[dir] = black
		order = append(order, dir)
		return nil
	}

	var dirs []string
	for d := range packages {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		if err := visit(d); err != nil {
			return nil, err
		}
	}
	return order, nil
}
