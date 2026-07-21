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
//
// mu guards Funcs/Types/Vars against concurrent access: a *Package built by
// PackageScope.Exports can be hot-swapped (see PackageScope.Replace, called
// by interp/loader's ReplaceFunction) while a separate goroutine is
// concurrently calling into that same package through a package-qualified
// selector (resolvePackageSelector) — hot-swapping during live execution is
// an explicitly supported scenario, so both sides must synchronize.
// Built-in packages (see RegisterBuiltinPackages) are populated once at
// startup and never mutated afterward, so they never contend on mu in
// practice, but every access still goes through it uniformly for safety.
type Package struct {
	Name  string
	Funcs map[string]*Function
	Types map[string]*TypeDef
	Vars  map[string]any

	mu sync.RWMutex
}

// Interpreter holds global state: functions, types, packages, natives.
type Interpreter struct {
	globals  *Env
	types    map[string]*TypeDef
	funcs    map[string]*Function
	natives  map[string]func(args []any) (any, error)
	packages map[string]*Package

	// internalNatives holds natives registered via RegisterInternalNative/
	// RegisterInternalNativeContext: reachable from other Go code in this
	// package (see hostNative, used by the fs/http/storage package wrappers
	// in packages.go) but never resolved as a bare guest identifier — unlike
	// natives, which evalExpr's *ast.Ident case falls back to for any name
	// not otherwise declared, making every entry there guest-callable by
	// name regardless of whether RegisterNative's own vm.declare call ran.
	internalNatives map[string]func(args []any) (any, error)

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
		internalNatives:  map[string]func(args []any) (any, error){},
		packages:         map[string]*Package{},
		VFS:              vfs,
		Args:             []string{"nanogo"},
		MaxContainerSize: DefaultMaxContainerSize,
		Limits:           DefaultExecutionLimits,
	}
}

// RegisterNative registers f under name AND declares name as a directly
// callable guest identifier — guest code can call name(...) as a bare
// function, with no capability check of any kind. Use this for a custom
// function a host wants guest programs to call directly (see
// examples/host_bridge).
//
// Do not use this for a primitive that must only ever be reachable through a
// capability-gated wrapper (e.g. a raw file-read or HTTP primitive that
// fs.ReadFile/http.GetText check a Capabilities policy before calling) —
// since guest code could then call name(...) directly and skip that check
// entirely, regardless of the host's configured Capabilities. Use
// RegisterInternalNative for those instead.
func (vm *Interpreter) RegisterNative(name string, f func(args []any) (any, error)) {
	vm.natives[name] = f
	vm.declare(name, &Function{Name: name, Native: f}, vm.globals)
}

// RegisterNativeContext registers a host native that receives the execution
// context. It is the safe choice for I/O and all potentially blocking work.
// Like RegisterNative, it also declares name as a directly callable guest
// identifier with no capability check — see RegisterNative's doc comment,
// and use RegisterInternalNativeContext instead for a capability-gated
// primitive.
func (vm *Interpreter) RegisterNativeContext(name string, f func(context.Context, []any) (any, error)) {
	vm.natives[name] = func(args []any) (any, error) {
		return f(vm.Context(), args)
	}
	vm.declare(name, &Function{Name: name, NativeContext: f}, vm.globals)
}

// RegisterInternalNative registers f under name for other native Go code to
// call (typically a curated package's own capability-checked wrapper
// function — see RegisterBuiltinPackages' fs.ReadFile, which checks
// vm.Capabilities before calling the "HostReadFile" native) without
// declaring name as a directly callable guest identifier and without adding
// it to vm's plain natives table, which evalExpr's identifier resolution
// falls back to for ANY undeclared name — an entry there is guest-callable
// by bare name regardless of whether RegisterNative's own vm.declare call
// ran. Use this instead of RegisterNative whenever f must only ever be
// reachable through a capability check: registering a capability-gated
// primitive with RegisterNative instead would let guest code call name(...)
// directly and bypass that check entirely.
func (vm *Interpreter) RegisterInternalNative(name string, f func(args []any) (any, error)) {
	vm.internalNatives[name] = f
}

// RegisterInternalNativeContext is RegisterInternalNative's
// context-propagating counterpart, mirroring RegisterNativeContext.
func (vm *Interpreter) RegisterInternalNativeContext(name string, f func(context.Context, []any) (any, error)) {
	vm.internalNatives[name] = func(args []any) (any, error) {
		return f(vm.Context(), args)
	}
}

// hostNative resolves a host-registered native by name for a curated
// package wrapper's own internal use (see packages.go's fs/http/storage
// functions) — checking internalNatives (RegisterInternalNative) first,
// then falling back to the guest-visible natives table (RegisterNative), so
// a wrapper works with either registration style. It deliberately never
// backs guest-visible identifier resolution (see evalExpr's *ast.Ident
// case, which only ever consults natives, never internalNatives).
func (vm *Interpreter) hostNative(name string) (func(args []any) (any, error), bool) {
	if f, ok := vm.internalNatives[name]; ok {
		return f, true
	}
	f, ok := vm.natives[name]
	return f, ok
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
	// Probe each ancestor with a shared RLock first — as get() does — and
	// only escalate to an exclusive Lock at the one scope that actually
	// holds the key. Env.Vars entries are never deleted anywhere in this
	// package (only declare/set add or update them), so once the RLock
	// probe sees the key present, it cannot have vanished by the time the
	// exclusive lock is acquired; no re-check under the write lock is
	// needed. This keeps every ancestor scope that doesn't own the key
	// fully concurrent-reader-friendly instead of unconditionally blocking
	// other goroutines' get() calls there.
	for e := env; e != nil; e = e.Parent {
		e.mu.RLock()
		_, ok := e.Vars[name]
		e.mu.RUnlock()
		if !ok {
			continue
		}
		e.mu.Lock()
		e.Vars[name] = val
		e.mu.Unlock()
		return
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
