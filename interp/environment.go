// interp/environment.go
package interp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// Env is a lexical scope chaining to a parent environment.
type Env struct {
	Vars   map[string]any
	Parent *Env
	mu     sync.RWMutex
	frame  *callFrame
}

func NewEnv(parent *Env) *Env {
	env := &Env{Vars: map[string]any{}, Parent: parent}
	if parent != nil {
		env.frame = parent.frame
	}
	return env
}

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

	// VFS is the virtual filesystem used by the os package and other file-aware builtins.
	// If nil, a fresh VFS is created automatically on first use.
	VFS *VFS

	// Args are the command-line arguments exposed as os.Args.
	Args []string

	// MaxContainerSize caps allocations made through make and append. It keeps
	// untrusted programs from turning one expression into an unbounded host
	// allocation. Set it before RunContext to raise or lower the limit.
	MaxContainerSize int

	// Limits bounds the amount of evaluator work and the number of guest
	// goroutines per RunContext call. Set either value to zero to disable that
	// specific limit for a trusted workload.
	Limits ExecutionLimits

	// Capabilities gates the curated filesystem and HTTP packages. The zero
	// value denies those capabilities; configure it before RunContext.
	Capabilities Capabilities

	// runMu serializes complete executions; a VM has mutable globals and is not
	// safe to execute concurrently. execution is atomic because every AST node
	// checks it; this keeps cancellation checks off the mutex fast path.
	runMu     sync.Mutex
	execution atomic.Pointer[execution]
	tracer    atomic.Pointer[Tracer]
}

// DefaultMaxContainerSize is the maximum slice, map hint, or channel capacity
// created by a default interpreter. Hosts with trusted workloads may raise it.
const DefaultMaxContainerSize = 1 << 20

// ExecutionLimits controls per-execution resource consumption. MaxSteps is
// measured in evaluator checkpoints (expressions and statements), not Go CPU
// instructions, so it is deterministic across machines.
type ExecutionLimits struct {
	MaxSteps      uint64
	MaxGoroutines int
}

// DefaultExecutionLimits keeps a guest from creating an unbounded number of
// goroutines or running indefinitely when a host accidentally uses Run rather
// than RunContext with a deadline.
var DefaultExecutionLimits = ExecutionLimits{
	MaxSteps:      10_000_000,
	MaxGoroutines: 1_024,
}

// ErrStepLimit and ErrGoroutineLimit identify resource-limit termination.
var (
	ErrStepLimit      = errors.New("nanogo: execution step limit exceeded")
	ErrGoroutineLimit = errors.New("nanogo: guest goroutine limit exceeded")
)

// ErrKilled is returned by RunContext after a host calls Kill.
var ErrKilled = errors.New("nanogo: execution killed")

func NewInterpreter() *Interpreter {
	return NewInterpreterWithVFS(NewVFS())
}

// NewInterpreterWithVFS creates an Interpreter that shares the given VFS.
// This allows multiple Interpreter instances (e.g. across MCP tool calls)
// to operate on the same in-memory filesystem.
func NewInterpreterWithVFS(vfs *VFS) *Interpreter {
	if vfs == nil {
		vfs = NewVFS()
	}
	return &Interpreter{
		globals:          NewEnv(nil),
		types:            map[string]*TypeDef{},
		funcs:            map[string]*Function{},
		natives:          map[string]func(args []any) (any, error){},
		packages:         map[string]*Package{},
		VFS:              vfs,
		Args:             []string{"nanogo"},
		MaxContainerSize: DefaultMaxContainerSize,
		Limits:           DefaultExecutionLimits,
	}
}

func (vm *Interpreter) RegisterNative(name string, f func(args []any) (any, error)) {
	vm.natives[name] = f
	vm.declare(name, &Function{Name: name, Native: f}, vm.globals)
}

// RegisterNativeContext registers a host native that receives the execution
// context. It is the safe choice for I/O and all potentially blocking work.
func (vm *Interpreter) RegisterNativeContext(name string, f func(context.Context, []any) (any, error)) {
	vm.natives[name] = func(args []any) (any, error) {
		return f(vm.Context(), args)
	}
	vm.declare(name, &Function{Name: name, NativeContext: f}, vm.globals)
}

func (vm *Interpreter) RegisterPackage(alias string, pkg *Package) {
	vm.packages[alias] = pkg
	vm.declare(alias, pkg, vm.globals)
}

func (vm *Interpreter) get(name string, env *Env) (any, bool) {
	for e := env; e != nil; e = e.Parent {
		e.mu.RLock()
		v, ok := e.Vars[name]
		e.mu.RUnlock()
		if ok {
			return v, true
		}
	}
	return nil, false
}

func (vm *Interpreter) set(name string, val any, env *Env) {
	for e := env; e != nil; e = e.Parent {
		e.mu.Lock()
		if _, ok := e.Vars[name]; ok {
			e.Vars[name] = val
			e.mu.Unlock()
			return
		}
		e.mu.Unlock()
	}
	// If not found, create in current scope.
	env.mu.Lock()
	env.Vars[name] = val
	env.mu.Unlock()
}

func (vm *Interpreter) declare(name string, val any, env *Env) {
	env.mu.Lock()
	env.Vars[name] = val
	env.mu.Unlock()
}

// --------------- Lvalue references for assignments ---------------

type Ref interface {
	Get() any
	Set(any) error
}

type varRef struct {
	vm   *Interpreter
	env  *Env
	name string
}

func (r *varRef) Get() any        { v, _ := r.vm.get(r.name, r.env); return v }
func (r *varRef) Set(v any) error { r.vm.set(r.name, v, r.env); return nil }

type sliceIndexRef struct {
	s *SliceVal
	i int
}

func (r *sliceIndexRef) Get() any        { return r.s.Data[r.i] }
func (r *sliceIndexRef) Set(v any) error { r.s.Data[r.i] = v; return nil }

type mapIndexRef struct {
	m *MapVal
	k any
}

func (r *mapIndexRef) Get() any { v, _ := r.m.getByKey(r.k); return v }
func (r *mapIndexRef) Set(v any) error {
	if v == nil {
		r.m.deleteByKey(r.k)
	} else {
		r.m.setByKey(r.k, v)
	}
	return nil
}

type fieldRef struct {
	s    *StructVal
	name string
}

func (r *fieldRef) Get() any        { return r.s.Fields[r.name] }
func (r *fieldRef) Set(v any) error { r.s.Fields[r.name] = v; return nil }

// ------------------- Call frames for defer/panic ------------------

type callFrame struct {
	defers []func()
}
