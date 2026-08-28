// interp/imports.go
package interp

// BuiltinImportPaths is the curated, reusable allow-list of import paths that
// RegisterBuiltinPackages knows how to satisfy. interp/loader (module
// resolution) and interp/index (static analysis) both treat any path in this
// set as a builtin rather than a local package that must exist on the VFS.
//
// This is additive, new-code-path-only data: legacy Run/RunContext callers
// keep resolving imports through installImportedPackage directly, including
// its existing silent no-op on an unrecognized import path. Keep this map in
// sync with installImportedPackage's cases.
var BuiltinImportPaths = map[string]bool{
	"fmt":           true,
	"debug":         true,
	"time":          true,
	"math":          true,
	"math/rand":     true,
	"encoding/json": true,
	"json":          true,
	"strings":       true,
	"sort":          true,
	"strconv":       true,
	"path":          true,
	"unicode/utf8":  true,
	"sync":          true,
	"regexp":        true,
	"browser":       true,
	"text/template": true,
	"http":          true,
	"storage":       true,
	"fs":            true,
	"os":            true,
	"testing":       true,
}

// IsBuiltinImport reports whether path is one of nanoGo's curated built-in
// packages (see RegisterBuiltinPackages), as opposed to a local, VFS-resident
// package that a module-aware loader must resolve itself.
func IsBuiltinImport(path string) bool { return BuiltinImportPaths[path] }

// Package returns the built-in package registered under path (its canonical
// import path, e.g. "fmt" or "math/rand"), for hosts building multi-package
// programs that need to bind a curated package under a local import alias.
func (vm *Interpreter) Package(path string) (*Package, bool) {
	// Curated packages are built on first use, so ask for path to be
	// materialized rather than reading the registry directly — otherwise a
	// module-aware host (interp/loader's bindImports) would see a builtin as
	// unregistered purely because nothing had imported it yet.
	return vm.ensureBuiltinPackage(path)
}
