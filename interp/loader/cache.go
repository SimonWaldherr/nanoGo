package loader

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"

	"simonwaldherr.de/go/nanogo/interp"
)

// ModuleCache reuses parsed multi-file module graphs while a VFS is
// unchanged. It caches parser output only: every Load returns a fresh,
// unbuilt Program, so callers may safely execute the result with different
// Interpreters and retain Program's existing one-Interpreter build contract.
//
// VFS.Revision invalidates the cache automatically after file, directory, or
// imported-tree changes. Hosts can also call Invalidate when their dependency
// configuration changes outside the VFS.
type ModuleCache struct {
	vfs *interp.VFS

	mu      sync.Mutex
	entries map[string]cachedModule
}

type cachedModule struct {
	revision uint64
	program  *Program
}

// NewModuleCache creates a parsed-module cache for vfs. A nil VFS is accepted
// so callers get a normal Load error instead of a constructor panic.
func NewModuleCache(vfs *interp.VFS) *ModuleCache {
	return &ModuleCache{vfs: vfs, entries: make(map[string]cachedModule)}
}

// Load returns a freshly cloned Program. Repeated calls without VFS mutations
// avoid directory walks, file reads, parsing, import resolution, and topo sort.
func (c *ModuleCache) Load(root string, opts Options) (*Program, error) {
	if c == nil || c.vfs == nil {
		return nil, fmt.Errorf("nanogo/loader: ModuleCache has no VFS")
	}
	key := moduleCacheKey(root, opts)

	c.mu.Lock()
	defer c.mu.Unlock()
	if entry, ok := c.entries[key]; ok && entry.revision == c.vfs.Revision() {
		return cloneProgram(entry.program), nil
	}

	// A concurrent VFS mutation during parsing cannot corrupt reads (VFS is
	// synchronized), but it can leave a mixed snapshot. Retry once so an edit
	// that lands during a load normally returns one coherent post-edit graph;
	// a continuously mutating host still receives a safe best-effort snapshot
	// and the next call invalidates it through the newer Revision.
	for attempt := 0; attempt < 2; attempt++ {
		before := c.vfs.Revision()
		prog, err := LoadModule(c.vfs, root, opts)
		if err != nil {
			return nil, err
		}
		after := c.vfs.Revision()
		if before == after || attempt == 1 {
			c.entries[key] = cachedModule{revision: after, program: prog}
			return cloneProgram(prog), nil
		}
	}
	panic("unreachable")
}

// Invalidate discards every cached module graph. It is useful when callers
// alter Options.DependencyRoots without changing VFS content.
func (c *ModuleCache) Invalidate() {
	if c == nil {
		return
	}
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
}

func cloneProgram(template *Program) *Program {
	packages := make(map[string]*ParsedPackage, len(template.Packages))
	for dir, pkg := range template.Packages {
		packages[dir] = pkg
	}
	return &Program{
		ModulePath: template.ModulePath,
		Root:       template.Root,
		Entry:      template.Entry,
		Packages:   packages,
		Order:      append([]string(nil), template.Order...),
		TestOrder:  append([]string(nil), template.TestOrder...),
	}
}

func moduleCacheKey(root string, opts Options) string {
	parts := []string{path.Clean(root), opts.ModulePath, path.Clean(opts.EntryDir)}
	modules := make([]string, 0, len(opts.DependencyRoots))
	for module, dir := range opts.DependencyRoots {
		modules = append(modules, module+"="+path.Clean(dir))
	}
	sort.Strings(modules)
	return strings.Join(append(parts, modules...), "\x00")
}
