// interp/package_scope.go
package interp

import (
	"context"
	"go/ast"
	"go/token"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// PackageScope is an isolated top-level scope for one loaded Go package. It
// is the primitive interp/loader builds multi-file, multi-package programs
// on top of: merge several files into one scope (two-phase, so forward
// references across files resolve regardless of file order), control what a
// package exposes to importers, and hot-swap one of its functions later.
//
// PackageScope deliberately does not replace Run/RunContext's own decl
// registration; it is new, additive machinery used only by hosts that call
// it directly (typically through interp/loader).
type PackageScope struct {
	Name string

	vm  *Interpreter
	env *Env

	// declared tracks names this package itself declared at top level
	// (functions and package-level vars/consts), separate from whatever
	// aliases Import binds into env — so Exports() never accidentally
	// re-exports an imported dependency under its own alias.
	declared map[string]bool

	// declaredTypes tracks top-level type names this package declared, so
	// Exports() knows which entries of the shared, global vm.types map
	// belong to it.
	declaredTypes map[string]bool

	// exported caches Exports()'s result: the SAME *Package object (and its
	// same underlying maps) is returned on every call, so a later Replace
	// is visible through every alias that already imported this package —
	// not just future ones.
	exported *Package

	inits []*Function

	// mu guards declared/declaredTypes against a Replace call landing
	// concurrently with itself (CollectDecls/EvalDecls/RunInit only ever run
	// single-threaded, before any concurrent Replace is possible, so they
	// don't need it).
	mu sync.Mutex
}

// NewPackageScope creates a fresh, empty top-level scope owned by vm. Its
// env chains to vm.globals, so curated builtins (fmt, time, ...) and any
// natives the host registered with RegisterNative/RegisterNativeContext/
// BindHostContext are visible to every package without a separate import
// step; user-package imports are added explicitly via Import.
func (vm *Interpreter) NewPackageScope(name string) *PackageScope {
	env := NewEnv(vm.globals)
	// PackageScope.Replace is expressly supported while a loaded package is
	// running. Mark only this host-mutable boundary as shared; function and
	// block scopes underneath keep Environment's lock-free evaluator path.
	env.shared = true
	return &PackageScope{
		Name:          name,
		vm:            vm,
		env:           env,
		declared:      map[string]bool{},
		declaredTypes: map[string]bool{},
	}
}

// CollectDecls performs phase 1 of a two-phase load: it registers every
// top-level type and func declaration from file without evaluating any
// const/var initializer or running func init(). Call it once per file in a
// package before calling EvalDecls on any file in the package, so forward
// references across files resolve regardless of file order.
//
// Struct types are registered into the interpreter's single shared, global
// type registry (matching Run/RunContext's existing behavior) — nanoGo does
// not namespace method dispatch per package, so two different packages
// defining a same-named struct type collide (last registration wins). This
// is a deliberate, documented scope cut; see the loader package docs.
func (ps *PackageScope) CollectDecls(file *ast.File, fset *token.FileSet) error {
	vm := ps.vm
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok != token.TYPE {
				continue
			}
			for _, spec := range d.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				switch tt := ts.Type.(type) {
				case *ast.StructType:
					td := &TypeDef{Name: ts.Name.Name, Kind: "struct", Fields: []FieldDef{}, Methods: map[string]*Function{}}
					for _, f := range tt.Fields.List {
						ft := typeString(f.Type)
						for _, n := range f.Names {
							td.Fields = append(td.Fields, FieldDef{Name: n.Name, Type: ft})
						}
					}
					vm.types[td.Name] = td
					ps.declaredTypes[td.Name] = true
				default:
					underlying := typeString(tt)
					if isBuiltinType(underlying) {
						vm.types[ts.Name.Name] = &TypeDef{Name: ts.Name.Name, Kind: "alias", Underlying: underlying}
						ps.declaredTypes[ts.Name.Name] = true
					}
				}
			}
		case *ast.FuncDecl:
			fn := ps.BuildFunction(d)
			if d.Recv != nil && len(d.Recv.List) > 0 {
				rcv := d.Recv.List[0]
				fn.RecvName = rcv.Names[0].Name
				fn.RecvType = strings.TrimPrefix(typeString(rcv.Type), "*")
				td := vm.types[fn.RecvType]
				if td == nil {
					td = &TypeDef{Name: fn.RecvType, Kind: "struct", Methods: map[string]*Function{}}
					vm.types[fn.RecvType] = td
				}
				td.Methods[fn.Name] = fn
				continue
			}
			// Go allows multiple func init(); they are not callable by name
			// and run in declaration order via RunInit, not through the
			// normal name lookup.
			if fn.Name == "init" {
				ps.inits = append(ps.inits, fn)
				continue
			}
			vm.declare(fn.Name, fn, ps.env)
			ps.declared[fn.Name] = true
		}
	}
	return nil
}

// BuildFunction builds a *Function from a parsed FuncDecl, bound to this
// scope's own env (so it can see the package's other vars/funcs/private
// helpers) — without registering it anywhere. CollectDecls uses this for
// every function it collects; a hot-swap host (see interp/loader's
// ReplaceFunction) uses it directly to build a standalone replacement
// function parsed on its own, then calls Replace to install it.
func (ps *PackageScope) BuildFunction(d *ast.FuncDecl) *Function {
	fn := &Function{Name: d.Name.Name, Body: d.Body, Env: ps.env}
	if d.Type.Params != nil {
		for i, f := range d.Type.Params.List {
			for _, n := range f.Names {
				fn.Params = append(fn.Params, n.Name)
			}
			if i == len(d.Type.Params.List)-1 {
				if _, ok := f.Type.(*ast.Ellipsis); ok {
					fn.IsVariadic = true
				}
			}
		}
	}
	return fn
}

// EvalDecls performs phase 2: evaluates package-level const/var initializers
// declared in file. Call it after CollectDecls has run for every file in the
// package, so initializers may reference functions, types, and vars declared
// later in file order or in a different file of the same package.
//
// Multiple const/var specs are evaluated in the order given (per file, in
// the order files are processed) — unlike a real Go compiler, nanoGo does
// not compute a dependency graph across package-level var initializers, so a
// var whose initializer references another var declared later in a later
// file will not resolve. Forward references to funcs and types (the common
// case) always work, since CollectDecls has already registered every one of
// them by the time EvalDecls runs.
func (ps *PackageScope) EvalDecls(ctx context.Context, file *ast.File) error {
	vm := ps.vm
	for _, decl := range file.Decls {
		if err := vm.executionError(); err != nil {
			return err
		}
		d, ok := decl.(*ast.GenDecl)
		if !ok || (d.Tok != token.CONST && d.Tok != token.VAR) {
			continue
		}
		for _, spec := range d.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if name.Name == "_" {
					continue
				}
				var val any
				if i < len(vs.Values) {
					v, err := vm.evalExpr(vs.Values[i], ps.env)
					if err != nil {
						return err
					}
					val = v
				} else {
					val = vm.zeroValueForType(typeString(vs.Type))
				}
				vm.declare(name.Name, val, ps.env)
				vm.recordVariable(name.Name, val, name, ps.env)
				ps.declared[name.Name] = true
			}
		}
	}
	return nil
}

// RunInit executes every func init() collected across every file this
// package's CollectDecls has processed, in declaration order.
func (ps *PackageScope) RunInit(ctx context.Context) error {
	inits := ps.inits
	ps.inits = nil
	for _, fn := range inits {
		if err := ps.vm.executionError(); err != nil {
			return err
		}
		if _, err := ps.vm.callFunction(fn, ps.env, nil, nil); err != nil {
			return err
		}
	}
	return nil
}

// Exports returns a *Package exposing only this scope's capitalized
// top-level functions, types, and vars — the object bound under an import
// alias in an importing package's scope. It builds this *Package once and
// caches it, always returning the SAME object (and the same underlying
// maps), so a later hot-swap (see Replace) is immediately visible through
// every alias that already imported this package, not just future callers.
//
// Call Exports only after every file this package will ever collect has
// been through CollectDecls/EvalDecls — interp/loader's ensureBuilt
// guarantees this by processing packages in dependency-first order before
// wiring a dependent's imports.
func (ps *PackageScope) Exports() *Package {
	if ps.exported != nil {
		return ps.exported
	}
	pkg := &Package{Name: ps.Name, Funcs: map[string]*Function{}, Types: map[string]*TypeDef{}, Vars: map[string]any{}}
	for name := range ps.declared {
		if !isExportedName(name) {
			continue
		}
		v, ok := ps.vm.getLocal(name, ps.env)
		if !ok {
			continue
		}
		if fn, ok := v.(*Function); ok {
			pkg.Funcs[name] = fn
		} else {
			pkg.Vars[name] = v
		}
	}
	for name := range ps.declaredTypes {
		if !isExportedName(name) {
			continue
		}
		if td, ok := ps.vm.types[name]; ok {
			pkg.Types[name] = td
		}
	}
	ps.exported = pkg
	return pkg
}

// Import binds pkg under alias in ps's own scope, making pkg.X reachable
// from ps's own function bodies (whose closures chain to ps.env).
func (ps *PackageScope) Import(alias string, pkg *Package) {
	ps.vm.declare(alias, pkg, ps.env)
}

// Lookup resolves a name declared directly at this package's top level
// (a function or a package-level var/const) — for finding an entry point or
// a hot-swap target.
func (ps *PackageScope) Lookup(name string) (any, bool) {
	return ps.vm.getLocal(name, ps.env)
}

// Replace overwrites a top-level function in ps with fn, taking effect for
// every future call — including calls made through another package's import
// alias, since it writes through to the same cached Exports() map every
// prior importer already holds a reference to. Replace is explicitly
// supported concurrently with a live execution that is still calling into
// ps (including through another package's alias): ps.env's own declare is
// already synchronized (Env.mu), and Replace synchronizes its two other
// writes — ps.declared (against a second, concurrent Replace) and the
// cached Exports() Package's Funcs map (against resolvePackageSelector's
// concurrent reads) — itself.
func (ps *PackageScope) Replace(name string, fn *Function) {
	ps.vm.declare(name, fn, ps.env)

	ps.mu.Lock()
	ps.declared[name] = true
	ps.mu.Unlock()

	if ps.exported != nil && isExportedName(name) {
		ps.exported.mu.Lock()
		ps.exported.Funcs[name] = fn
		ps.exported.mu.Unlock()
	}
}

func isExportedName(name string) bool {
	if name == "" {
		return false
	}
	r, _ := utf8.DecodeRuneInString(name)
	return unicode.IsUpper(r)
}
