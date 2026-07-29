package loader

import (
	"fmt"
	"go/ast"
	"go/token"
	"path"
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

	// DependencyRoots maps external module paths to roots already present in
	// the VFS, for example "example.com/metrics" -> "/deps/metrics". It is
	// the dynamic, host-controlled alternative to network downloads: callers
	// may mount or update a dependency snapshot, then load it immediately.
	// The longest matching module path wins, so a nested module can override
	// a broader mapping.
	DependencyRoots map[string]string
}

// moduleResolver maps import-path module prefixes to VFS roots. It performs
// longest-prefix matching just like Go's module resolver, but only against
// source that the embedding host has explicitly placed in the VFS.
type moduleResolver struct {
	roots map[string]string
}

func newModuleResolver(modulePath, root string, dependencies map[string]string) *moduleResolver {
	r := &moduleResolver{roots: make(map[string]string, len(dependencies)+1)}
	r.add(modulePath, root)
	for module, dir := range dependencies {
		r.add(module, dir)
	}
	return r
}

func (r *moduleResolver) add(modulePath, root string) {
	if modulePath == "" || root == "" {
		return
	}
	r.roots[modulePath] = path.Clean(root)
}

func (r *moduleResolver) resolve(importPath string) (string, bool) {
	bestModule, bestRoot := "", ""
	for module, root := range r.roots {
		if importPath != module && !strings.HasPrefix(importPath, module+"/") {
			continue
		}
		if len(module) > len(bestModule) {
			bestModule, bestRoot = module, root
		}
	}
	if bestModule == "" {
		return "", false
	}
	return path.Join(bestRoot, strings.TrimPrefix(strings.TrimPrefix(importPath, bestModule), "/")), true
}

func (r *moduleResolver) rootForDir(dir string) string {
	best := ""
	for _, root := range r.roots {
		if dir == root || strings.HasPrefix(dir, root+"/") {
			if len(root) > len(best) {
				best = root
			}
		}
	}
	return best
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
	Dir                 string
	Name                string
	Files               []*ast.File
	TestFiles           []*ast.File // package foo
	ExternalTestFiles   []*ast.File // package foo_test
	ExternalTestName    string
	FSet                *token.FileSet
	Imports             []ImportEdge // production imports
	TestImports         []ImportEdge // package foo test imports
	ExternalTestImports []ImportEdge
}

// Program is the result of LoadModule: every local package reachable from
// the entry package, plus a dependency-first topological order.
type Program struct {
	ModulePath string
	Root       string
	Entry      string // Dir of the entry package
	Packages   map[string]*ParsedPackage
	Order      []string // production dependency-first order
	TestOrder  []string // production + test-only dependency-first order

	// built caches the PackageScopes produced by ensureBuilt (see run.go)
	// against whichever *interp.Interpreter first executes this Program.
	// Rebuilding against a second, different Interpreter is not supported —
	// build a fresh Program (LoadModule again) per Interpreter instead.
	built              map[string]*interp.PackageScope
	testsBuilt         map[string]bool
	externalTestScopes map[string]*interp.PackageScope
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
	var rootModule ModuleFile
	if data, err := vfs.ReadFile(path.Join(root, "go.mod")); err == nil {
		parsed, perr := ParseModuleFile(data)
		if perr != nil {
			return nil, fmt.Errorf("nanogo/loader: %s/go.mod: %w", root, perr)
		}
		rootModule = parsed
		modulePath = parsed.Path
	}
	if modulePath == "" {
		return nil, fmt.Errorf("nanogo/loader: no module path: %s has no go.mod and Options.ModulePath is empty", root)
	}
	resolver := newModuleResolver(modulePath, root, opts.DependencyRoots)
	for module, replacement := range rootModule.Replaces {
		resolver.add(module, resolveReplacementRoot(root, replacement))
	}

	entryDir := opts.EntryDir
	if entryDir == "" {
		entryDir = root
	}
	entryDir = path.Clean(entryDir)

	packages := map[string]*ParsedPackage{}
	queue := []string{entryDir}
	queued := map[string]bool{entryDir: true}
	configuredRoots := map[string]bool{root: true}

	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		if moduleRoot := resolver.rootForDir(dir); moduleRoot != "" && !configuredRoots[moduleRoot] {
			if err := configureModuleRoot(vfs, resolver, moduleRoot); err != nil {
				return nil, err
			}
			configuredRoots[moduleRoot] = true
		}

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
		internalTests := make([]*ast.File, 0, len(testFiles))
		externalTests := make([]*ast.File, 0)
		externalName := ""
		for _, f := range testFiles {
			if f.Name.Name == pkgName {
				internalTests = append(internalTests, f)
				continue
			}
			want := pkgName + "_test"
			if f.Name.Name != want {
				return nil, fmt.Errorf("nanogo/loader: test package %q in %s must be %q or %q", f.Name.Name, dir, pkgName, want)
			}
			if externalName != "" && externalName != f.Name.Name {
				return nil, fmt.Errorf("nanogo/loader: test directory %s has multiple external test packages", dir)
			}
			externalName = f.Name.Name
			externalTests = append(externalTests, f)
		}

		pp := &ParsedPackage{
			Dir: dir, Name: pkgName, Files: files, TestFiles: internalTests,
			ExternalTestFiles: externalTests, ExternalTestName: externalName, FSet: fset,
		}

		// Test files can import helper packages that production code does not.
		// Resolve internal and external test imports separately: an external
		// test is a distinct package and must not leak aliases into production.
		collectImports := func(importFiles []*ast.File, destination *[]ImportEdge) error {
			seen := map[string]bool{}
			for _, f := range importFiles {
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
						*destination = append(*destination, ImportEdge{Alias: alias, Path: importPath, Builtin: true})
						continue
					}

					depDir, ok := resolver.resolve(importPath)
					if !ok {
						return fmt.Errorf("nanogo/loader: import %q in package %s is neither a module-local package (module %q), a configured dependency, nor a recognized builtin", importPath, dir, modulePath)
					}
					*destination = append(*destination, ImportEdge{Alias: alias, Path: importPath, Dir: depDir})
					if !queued[depDir] {
						queued[depDir] = true
						queue = append(queue, depDir)
					}
				}
			}
			return nil
		}
		if err := collectImports(files, &pp.Imports); err != nil {
			return nil, err
		}
		if err := collectImports(internalTests, &pp.TestImports); err != nil {
			return nil, err
		}
		if err := collectImports(externalTests, &pp.ExternalTestImports); err != nil {
			return nil, err
		}

		packages[dir] = pp
	}

	order, err := topoSort(packages, entryDir, false)
	if err != nil {
		return nil, err
	}
	testOrder, err := topoSort(packages, entryDir, true)
	if err != nil {
		return nil, err
	}

	return &Program{ModulePath: modulePath, Root: root, Entry: entryDir, Packages: packages, Order: order, TestOrder: testOrder}, nil
}

func resolveReplacementRoot(moduleRoot, replacement string) string {
	if path.IsAbs(replacement) {
		return path.Clean(replacement)
	}
	return path.Join(moduleRoot, replacement)
}

// configureModuleRoot discovers replace directives in a dependency's own
// go.mod. A missing go.mod is allowed for an explicitly configured source
// snapshot; malformed present files fail early with their exact VFS path.
func configureModuleRoot(vfs *interp.VFS, resolver *moduleResolver, moduleRoot string) error {
	data, err := vfs.ReadFile(path.Join(moduleRoot, "go.mod"))
	if err != nil {
		return nil
	}
	mod, err := ParseModuleFile(data)
	if err != nil {
		return fmt.Errorf("nanogo/loader: %s/go.mod: %w", moduleRoot, err)
	}
	resolver.add(mod.Path, moduleRoot)
	for module, replacement := range mod.Replaces {
		resolver.add(module, resolveReplacementRoot(moduleRoot, replacement))
	}
	return nil
}

// topoSort returns a dependency-first order over packages: every package's
// local import dependencies appear before it. A cycle produces a clear
// error naming the chain instead of recursing forever.
func topoSort(packages map[string]*ParsedPackage, entry string, includeTests bool) ([]string, error) {
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
		if includeTests {
			for _, imp := range pkg.TestImports {
				if imp.Builtin {
					continue
				}
				if err := visit(imp.Dir); err != nil {
					return err
				}
			}
			// External tests execute after their production package is built, so
			// an external test importing its own package is valid, not a cycle.
			for _, imp := range pkg.ExternalTestImports {
				if imp.Builtin || imp.Dir == dir {
					continue
				}
				if err := visit(imp.Dir); err != nil {
					return err
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[dir] = black
		order = append(order, dir)
		return nil
	}

	if err := visit(entry); err != nil {
		return nil, err
	}
	return order, nil
}
