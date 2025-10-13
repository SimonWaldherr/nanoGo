// interp/environment.go
package interp

import "sync"

// Env is a lexical scope chaining to a parent environment.
type Env struct {
	Vars   map[string]any
	Parent *Env
}

func NewEnv(parent *Env) *Env { return &Env{Vars: map[string]any{}, Parent: parent} }

// Package represents a very small package object (functions, types, vars).
type Package struct {
	Name  string
	Funcs map[string]*Function
	Types map[string]*TypeDef
	Vars  map[string]any
}

// Interpreter holds global state: functions, types, packages, natives.
type Interpreter struct {
	globals  *Env
	types    map[string]*TypeDef
	funcs    map[string]*Function
	natives  map[string]func(args []any) (any, error)
	packages map[string]*Package

	// frames is a stack of call frames for defer/panic handling.
	frames []*callFrame

	// For optional coarse locking if user runs many goroutines touching shared state.
	mu sync.Mutex
}

func NewInterpreter() *Interpreter {
	return &Interpreter{
		globals:  NewEnv(nil),
		types:    map[string]*TypeDef{},
		funcs:    map[string]*Function{},
		natives:  map[string]func(args []any) (any, error){},
		packages: map[string]*Package{},
		frames:   []*callFrame{},
	}
}

func (vm *Interpreter) RegisterNative(name string, f func(args []any) (any, error)) {
	vm.natives[name] = f
	vm.globals.Vars[name] = &Function{Name: name, Native: f}
}

func (vm *Interpreter) RegisterPackage(alias string, pkg *Package) {
	vm.packages[alias] = pkg
	vm.globals.Vars[alias] = pkg
}

func (vm *Interpreter) get(name string, env *Env) (any, bool) {
	for e := env; e != nil; e = e.Parent {
		if v, ok := e.Vars[name]; ok { return v, true }
	}
	return nil, false
}

func (vm *Interpreter) set(name string, val any, env *Env) {
	for e := env; e != nil; e = e.Parent {
		if _, ok := e.Vars[name]; ok { e.Vars[name] = val; return }
	}
	// If not found, create in current scope.
	env.Vars[name] = val
}

func (vm *Interpreter) declare(name string, val any, env *Env) { env.Vars[name] = val }

// --------------- Lvalue references for assignments ---------------

type Ref interface { Get() any; Set(any) error }

type varRef struct{ vm *Interpreter; env *Env; name string }
func (r *varRef) Get() any { v,_ := r.vm.get(r.name, r.env); return v }
func (r *varRef) Set(v any) error { r.vm.set(r.name, v, r.env); return nil }

type sliceIndexRef struct{ s *SliceVal; i int }
func (r *sliceIndexRef) Get() any { return r.s.Data[r.i] }
func (r *sliceIndexRef) Set(v any) error { r.s.Data[r.i] = v; return nil }

type mapIndexRef struct{ m *MapVal; k any }
func (r *mapIndexRef) Get() any { v,_ := r.m.getByKey(r.k); return v }
func (r *mapIndexRef) Set(v any) error {
	if v == nil { r.m.deleteByKey(r.k) } else { r.m.setByKey(r.k, v) }
	return nil
}

type fieldRef struct{ s *StructVal; name string }
func (r *fieldRef) Get() any { return r.s.Fields[r.name] }
func (r *fieldRef) Set(v any) error { r.s.Fields[r.name] = v; return nil }

// ------------------- Call frames for defer/panic ------------------

type callFrame struct {
	defers []func()
}

func (vm *Interpreter) pushFrame() *callFrame {
	fr := &callFrame{defers: []func(){}}
	vm.frames = append(vm.frames, fr)
	return fr
}
func (vm *Interpreter) currentFrame() *callFrame {
	if len(vm.frames) == 0 { return nil }
	return vm.frames[len(vm.frames)-1]
}
func (vm *Interpreter) popFrame() *callFrame {
	if len(vm.frames) == 0 { return nil }
	fr := vm.frames[len(vm.frames)-1]
	vm.frames = vm.frames[:len(vm.frames)-1]
	return fr
}
