// interp/environment.go
package interp

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
)

// Env is a lexical scope chaining to a parent environment. Vars starts out
// nil (see NewEnv) and is allocated lazily on first declare(): reading a nil
// map is well-defined Go (returns the zero value, ok=false), so get() needs
// no special-casing, and a huge fraction of scopes created per evaluation —
// a for-loop body block that only assigns to outer variables, an if-body
// that just returns — never declare anything locally and so never pay for a
// map allocation at all. This was previously the single largest allocation
// source in the interpreter (see interp/bench_test.go's benchmarks).
type Env struct {
	Vars map[string]any
	// A call or block scope frequently holds exactly one integer binding (a
	// recursive parameter or a loop counter). Keeping that one binding here
	// avoids a second allocation per frame. Further values use Vars, retaining
	// Env's compact size instead of making every scope permanently larger.
	inlineIntVar intVar
	// inlineFloatVars keeps hot floating-point locals unboxed. Numerical inner
	// loops otherwise allocate once for every assignment merely to fit a
	// float64 in any.
	inlineFloats *inlineFloatEnv
	// inlineVars does for non-integer bindings what inlineIntVar does for
	// integers. A loop body that declares a struct, a slice, or a string —
	// `p := P{...}` inside a for — used to allocate a whole Vars map per
	// iteration, which allocation profiling showed to be the single largest
	// object source for struct-heavy guest code. Entries are only ever added
	// while Vars is nil, so a name is never in both tables and lookup order
	// never has to disambiguate between them; once a scope overflows into
	// Vars, everything new goes there.
	inlineVars [envInlineVars]valueVar
	inlineLen  uint8
	Parent     *Env
	// shared marks a package scope that hosts may hot-swap while guest code is
	// running. It is fixed when the scope is created, so callers can lock this
	// one boundary without making short-lived function/block scopes pay locks.
	shared bool
	mu     sync.RWMutex
	frame  *callFrame
}

// intVar is one binding in Env's small integer table. A tiny linear scan beats a
// map[string]int here: function-call and block scopes almost always hold
// only a handful of int locals (loop counters, a couple of params), and for
// that size a map's header-plus-bucket allocation and hashing cost more than
// scanning a few slice entries by direct string comparison — this used to be
// the largest allocation source in the interpreter (see bench_test.go's
// BenchmarkFibRecursive, which is nearly all short-lived call-scope
// allocation). Falls back to no special-casing for large scopes; nanoGo's
// guest programs don't have call frames with dozens of locals in practice.
type intVar struct {
	name string
	val  int
}

const envInlineFloats = 3

type floatVar struct {
	name string
	val  float64
}

// inlineFloatEnv stays separate from Env so integer-only call frames keep the
// compact layout they had before float specialization. Pooled scopes retain
// and clear this small backing store for later numerical blocks.
type inlineFloatEnv struct {
	vars [envInlineFloats]floatVar
	len  uint8
}

func lookupFloatVar(env *Env, name string) (float64, bool) {
	floats := env.inlineFloats
	if floats == nil {
		return 0, false
	}
	for i := 0; i < int(floats.len); i++ {
		if floats.vars[i].name == name {
			return floats.vars[i].val, true
		}
	}
	return 0, false
}

func setOrAppendFloatVar(env *Env, name string, value float64) {
	floats := env.inlineFloats
	if floats == nil {
		floats = &inlineFloatEnv{}
		env.inlineFloats = floats
	}
	for i := 0; i < int(floats.len); i++ {
		if floats.vars[i].name == name {
			floats.vars[i].val = value
			return
		}
	}
	if int(floats.len) < len(floats.vars) {
		floats.vars[floats.len] = floatVar{name, value}
		floats.len++
		return
	}
	if env.Vars == nil {
		env.Vars = make(map[string]any, 4)
	}
	env.Vars[name] = value
}

func removeFloatVar(env *Env, name string) {
	floats := env.inlineFloats
	if floats == nil {
		return
	}
	for i := 0; i < int(floats.len); i++ {
		if floats.vars[i].name == name {
			last := int(floats.len) - 1
			floats.vars[i] = floats.vars[last]
			floats.vars[last] = floatVar{}
			floats.len--
			return
		}
	}
}

func clearInlineFloatVars(env *Env) {
	floats := env.inlineFloats
	if floats == nil {
		return
	}
	for i := 0; i < int(floats.len); i++ {
		floats.vars[i] = floatVar{}
	}
	floats.len = 0
}

func lookupIntVar(env *Env, name string) (int, bool) {
	if env.inlineIntVar.name == name && name != "" {
		return env.inlineIntVar.val, true
	}
	return 0, false
}

func setOrAppendIntVar(env *Env, name string, value int) {
	if env.inlineIntVar.name == name && name != "" {
		env.inlineIntVar.val = value
		return
	}
	if env.inlineIntVar.name == "" {
		env.inlineIntVar = intVar{name, value}
		return
	}
	if env.Vars == nil {
		env.Vars = make(map[string]any, 4)
	}
	env.Vars[name] = value
}

func removeIntVar(env *Env, name string) {
	if env.inlineIntVar.name == name {
		env.inlineIntVar = intVar{}
	}
}

// envInlineVars is how many non-integer bindings a scope keeps inline before
// it allocates a Vars map. Three covers the ordinary shapes — a loop body
// binding one or two values, a call scope holding a couple of non-integer
// parameters — while keeping Env small enough that the fast-call env pool
// still recycles a compact object.
const envInlineVars = 3

// valueVar is one entry in Env's inline table of non-integer bindings. As
// with intVar, a short linear scan of direct string comparisons beats a map
// at this size: no hashing, no bucket allocation, and the names being
// compared are usually the identical string header the parser produced.
type valueVar struct {
	name string
	val  any
}

func lookupInlineVar(env *Env, name string) (any, bool) {
	for i := 0; i < int(env.inlineLen); i++ {
		if env.inlineVars[i].name == name {
			return env.inlineVars[i].val, true
		}
	}
	return nil, false
}

// storeValueInEnv writes a non-integer binding into env, preferring the
// inline table. New names only go inline while Vars is nil, which is what
// keeps "a name lives in exactly one table" true without any cross-checking:
// a scope that has already spilled to a map puts everything new in the map.
func storeValueInEnv(env *Env, name string, val any) {
	for i := 0; i < int(env.inlineLen); i++ {
		if env.inlineVars[i].name == name {
			env.inlineVars[i].val = val
			return
		}
	}
	if env.Vars == nil {
		if int(env.inlineLen) < len(env.inlineVars) {
			env.inlineVars[env.inlineLen] = valueVar{name, val}
			env.inlineLen++
			return
		}
		env.Vars = make(map[string]any, 4)
	}
	env.Vars[name] = val
}

// removeInlineVar drops name from the inline table, closing the gap by moving
// the last entry into its place. Nothing depends on the table's order: it is
// scanned by name, and collectLocalVars folds it into a map.
func removeInlineVar(env *Env, name string) {
	for i := 0; i < int(env.inlineLen); i++ {
		if env.inlineVars[i].name == name {
			last := int(env.inlineLen) - 1
			env.inlineVars[i] = env.inlineVars[last]
			env.inlineVars[last] = valueVar{}
			env.inlineLen--
			return
		}
	}
}

// clearInlineVars drops every inline binding, releasing the values it holds
// so a pooled Env cannot keep them alive.
func clearInlineVars(env *Env) {
	for i := 0; i < int(env.inlineLen); i++ {
		env.inlineVars[i] = valueVar{}
	}
	env.inlineLen = 0
}

func NewEnv(parent *Env) *Env {
	env := &Env{Parent: parent}
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

	// builtinsEnabled records that a host called RegisterBuiltinPackages.
	// The curated packages are constructed on first use rather than there
	// (see ensureBuiltinPackage), so this flag is what separates "this host
	// opted into the builtins, that package just hasn't been built yet" from
	// "this host never enabled them" — the latter must stay an unresolved
	// import, exactly as before.
	builtinsEnabled bool

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
	// safe to execute concurrently. execution remains atomic for lifecycle
	// operations that may originate outside the evaluator (Kill and Context).
	runMu     sync.Mutex
	execution atomic.Pointer[execution]
	// activeExecution is the evaluator-only counterpart to execution. runMu
	// serializes its lifetime, and it is cleared only after every guest
	// goroutine joins, so evaluator checkpoints can avoid an atomic-pointer
	// lookup for each AST node. External lifecycle operations (Kill, Context,
	// IsRunning) continue to use execution.
	activeExecution *execution
	tracer          atomic.Pointer[Tracer]
	// breakpoints is an immutable line set swapped atomically between runs.
	// It is intentionally separate from the tracer: hosts can configure
	// source breakpoints once, then opt into recording only when a run needs
	// debug evidence.
	breakpoints atomic.Pointer[breakpointSet]
	// runtimeTraceAnnotations mirrors the selected high-level nanoGo events
	// into the host's runtime/trace stream. It stays opt-in because a normal
	// interpreter run must not pay tracing's formatting cost.
	runtimeTraceAnnotations atomic.Bool
	lineProfile             atomic.Pointer[LineProfile]
	// variableTracker is an opt-in, bounded-by-symbol-count snapshot used by
	// debugger UIs. The normal interpreter path pays only one nil pointer load
	// at explicit variable-write sites; hosts enable it per interpreter.
	variableTracker atomic.Pointer[VariableTracker]
	// debugController is an opt-in live pause/step session (see debugger.go).
	// Unlike Tracer/breakpoints (record-only), a statement checkpoint may
	// actually block the calling goroutine here until a host resumes it, so
	// a host that attaches one must call Run/RunContext from a goroutine it
	// can afford to block and issue resume calls from another.
	debugController atomic.Pointer[DebugController]
	// stmtHooks is true whenever any of breakpoints, lineProfile or
	// debugController is installed. evalStmt runs for every single guest
	// statement, and testing those three pointers there meant three separate
	// atomic loads from three different cache lines on a path that, for an
	// ordinary run, has nothing to report to any of them. One flag collapses
	// that to a single load; the individual pointers are only consulted once
	// it says at least one hook exists. Maintained by refreshStmtHooks, which
	// every installer calls after storing.
	stmtHooks atomic.Bool
	// fastEnvPool recycles call scopes for frame-free functions that cannot
	// create closures. Recursive and call-heavy guest code otherwise creates
	// one heap object per invocation even though each scope dies on return.
	fastEnvPool sync.Pool
	// stackFramesRequired becomes true once parsed guest code may inspect the
	// dynamic call stack (debug.Stack/debug.Vars). It keeps every caller on
	// the full call path for that interpreter, preserving complete stack
	// chains even when only a deeply nested callee performs the inspection.
	stackFramesRequired atomic.Bool

	// lastSteps preserves the final step counter of the most recently ended
	// execution so hosts can report deterministic cost after Run returns
	// (StepCount only works while an execution is active).
	lastSteps atomic.Uint64
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
		// globals never stays empty — host natives, each imported package's
		// alias (written straight into this map by installImportedPackage),
		// and every guest package-level declaration land here — so it is
		// allocated directly instead of going through NewEnv's lazy path.
		// The hint is modest on purpose: curated packages are built on
		// first use now (see RegisterBuiltinPackages), so a typical program
		// binds a couple of imports and a handful of natives rather than the
		// twenty-odd aliases the old eager registration put here.
		globals:          &Env{Vars: make(map[string]any, 16)},
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

// lookupValueInEnv reads name directly from env (not its ancestors),
// checking the unboxed inline integer slot before the general Vars map. Callers
// hold whatever lock (or none, on the single-goroutine fast path) is
// appropriate for env; this only touches the two maps.
func lookupValueInEnv(env *Env, name string) (any, bool) {
	if n, ok := lookupIntVar(env, name); ok {
		return n, true
	}
	if f, ok := lookupFloatVar(env, name); ok {
		return f, true
	}
	if v, ok := lookupInlineVar(env, name); ok {
		return v, true
	}
	v, ok := env.Vars[name]
	return v, ok
}

// get, set, and declare all check vm.activeExecution's concurrent flag
// before deciding whether to lock. execution.reserveGoroutine publishes true
// before it launches a child, and releaseGoroutine clears it only after the
// final child returns. That gives every overlapping parent/child access the
// required synchronization while letting a root evaluator regain the
// lock-free path after it has joined all of its worker goroutines.
func (vm *Interpreter) get(name string, env *Env) (any, bool) {
	exec := vm.activeExecution
	// As with getInt/getFloat, a guest goroutine cannot begin overlapping this
	// lookup without reserveGoroutine publishing concurrency first. Snapshot
	// once instead of doing an atomic load for every lexical parent.
	concurrent := exec != nil && exec.concurrent.Load()
	for e := env; e != nil; e = e.Parent {
		if e.shared || concurrent {
			e.mu.RLock()
			v, ok := lookupValueInEnv(e, name)
			e.mu.RUnlock()
			if ok {
				return v, true
			}
			continue
		}
		if v, ok := lookupValueInEnv(e, name); ok {
			return v, true
		}
	}
	return nil, false
}

// getInt is the allocation-free counterpart to get for evaluator paths that
// can prove they only need an int. The general get method must return an any,
// which boxes large ints; tight arithmetic loops use this helper instead.
func (vm *Interpreter) getInt(name string, env *Env) (int, bool) {
	exec := vm.activeExecution
	// A guest cannot become concurrent halfway through this lookup: the go
	// statement sets concurrent before its child can execute. Snapshot it once
	// instead of issuing the same atomic load for every lexical parent; integer
	// arithmetic commonly walks two scopes (loop counter plus accumulator).
	concurrent := exec != nil && exec.concurrent.Load()
	for e := env; e != nil; e = e.Parent {
		if e.shared || concurrent {
			e.mu.RLock()
			n, ok := lookupIntVar(e, name)
			e.mu.RUnlock()
			if ok {
				return n, true
			}
			continue
		}
		if n, ok := lookupIntVar(e, name); ok {
			return n, true
		}
	}
	return 0, false
}

// getFloat is getInt's float64 counterpart. It keeps numerical evaluator
// paths from materializing a temporary interface value for each read.
func (vm *Interpreter) getFloat(name string, env *Env) (float64, bool) {
	exec := vm.activeExecution
	concurrent := exec != nil && exec.concurrent.Load()
	for e := env; e != nil; e = e.Parent {
		if e.shared || concurrent {
			e.mu.RLock()
			f, ok := lookupFloatVar(e, name)
			e.mu.RUnlock()
			if ok {
				return f, true
			}
			continue
		}
		if f, ok := lookupFloatVar(e, name); ok {
			return f, true
		}
	}
	return 0, false
}

// getLocal looks up a binding only in env itself. PackageScope uses it when
// exporting package-level declarations; unlike get, it must not fall through
// to imported/global parent scopes.
func (vm *Interpreter) getLocal(name string, env *Env) (any, bool) {
	env.mu.RLock()
	v, ok := lookupValueInEnv(env, name)
	env.mu.RUnlock()
	return v, ok
}

// hasLocalBinding reports whether name belongs to this exact scope. Short
// declarations use this rather than get(), because an outer binding is
// deliberately shadowed by `x := ...` in an inner block.
func (vm *Interpreter) hasLocalBinding(name string, env *Env) bool {
	exec := vm.activeExecution
	concurrent := exec != nil && exec.concurrent.Load()
	if env.shared || concurrent {
		env.mu.RLock()
		intOK, floatOK, valueOK := lookupOwnershipInEnv(env, name)
		env.mu.RUnlock()
		return intOK || floatOK || valueOK
	}
	intOK, floatOK, valueOK := lookupOwnershipInEnv(env, name)
	return intOK || floatOK || valueOK
}

// lookupOwnershipInEnv reports whether name is stored directly in env, in
// either table, without reading the (possibly large, for Vars) value —
// set uses this to find which ancestor owns a name before deciding how to
// update it.
func lookupOwnershipInEnv(env *Env, name string) (intOK, floatOK, valueOK bool) {
	_, intOK = lookupIntVar(env, name)
	if intOK {
		return true, false, false
	}
	_, floatOK = lookupFloatVar(env, name)
	if floatOK {
		return false, true, false
	}
	if _, ok := lookupInlineVar(env, name); ok {
		return false, false, true
	}
	_, valueOK = env.Vars[name]
	return
}

// updateInEnv overwrites name's value in env, which the caller has already
// established (via lookupOwnershipInEnv) owns it. intOK selects which table
// name currently lives in; storing a value of the "wrong" kind for that
// table moves it to the other one (e.g. assigning a string over what was
// previously an int local).
func updateInEnv(env *Env, name string, val any, intOK, floatOK bool) {
	if intOK {
		if n, ok := val.(int); ok {
			setOrAppendIntVar(env, name, n)
			return
		}
		removeIntVar(env, name)
	}
	if floatOK {
		if f, ok := val.(float64); ok {
			setOrAppendFloatVar(env, name, f)
			return
		}
		removeFloatVar(env, name)
	}
	if f, ok := val.(float64); ok {
		removeInlineVar(env, name)
		delete(env.Vars, name)
		setOrAppendFloatVar(env, name, f)
		return
	}
	storeValueInEnv(env, name, val)
}

// declareInEnv stores a brand-new binding directly in env (not an ancestor):
// used by both declare and set's "not found anywhere" fallback.
func declareInEnv(env *Env, name string, val any) {
	if n, ok := val.(int); ok {
		removeFloatVar(env, name)
		removeInlineVar(env, name)
		delete(env.Vars, name)
		setOrAppendIntVar(env, name, n)
		return
	}
	if f, ok := val.(float64); ok {
		removeIntVar(env, name)
		removeInlineVar(env, name)
		delete(env.Vars, name)
		setOrAppendFloatVar(env, name, f)
		return
	}
	removeIntVar(env, name)
	removeFloatVar(env, name)
	storeValueInEnv(env, name, val)
}

func (vm *Interpreter) set(name string, val any, env *Env) {
	// A loaded PackageScope may be replaced by the host while its child
	// function scopes execute. Lock per scope so those shared ancestors stay
	// safe without making purely local assignments pay synchronization.
	exec := vm.activeExecution
	concurrent := exec != nil && exec.concurrent.Load()
	for e := env; e != nil; e = e.Parent {
		if e.shared || concurrent {
			// Taking the writer lock once avoids an RLock/RUnlock/Lock sequence
			// for every successful concurrent assignment. It also makes the
			// ownership check and the update one atomic operation, so another
			// goroutine cannot change a binding's storage kind between them.
			e.mu.Lock()
			intOK, floatOK, valueOK := lookupOwnershipInEnv(e, name)
			if !intOK && !floatOK && !valueOK {
				e.mu.Unlock()
				continue
			}
			updateInEnv(e, name, val, intOK, floatOK)
			e.mu.Unlock()
			return
		}

		intOK, floatOK, valueOK := lookupOwnershipInEnv(e, name)
		if !intOK && !floatOK && !valueOK {
			continue
		}
		updateInEnv(e, name, val, intOK, floatOK)
		return
	}
	if env.shared || concurrent {
		env.mu.Lock()
		declareInEnv(env, name, val)
		env.mu.Unlock()
		return
	}
	declareInEnv(env, name, val)
}

func (vm *Interpreter) declare(name string, val any, env *Env) {
	if env.shared {
		env.mu.Lock()
		declareInEnv(env, name, val)
		env.mu.Unlock()
		return
	}
	if exec := vm.activeExecution; exec == nil || !exec.concurrent.Load() {
		declareInEnv(env, name, val)
		return
	}
	env.mu.Lock()
	declareInEnv(env, name, val)
	env.mu.Unlock()
}

// undeclare removes name from env's own bindings directly — it never
// searches env's parent chain, mirroring declare's scope. execStmtList
// uses it before a goto restarts execution at an earlier label: a := (or
// var/const) between that label and the goto forgets its previous binding
// so it can legally redeclare on the next pass, exactly as it would if the
// loop got a brand-new Env per iteration (the normal case everywhere else
// — see blockNeedsOwnScope — which a goto-driven restart bypasses since it
// reuses the same Env instead of allocating a fresh one).
func (vm *Interpreter) undeclare(name string, env *Env) {
	if env.shared {
		env.mu.Lock()
		removeIntVar(env, name)
		removeFloatVar(env, name)
		removeInlineVar(env, name)
		delete(env.Vars, name)
		env.mu.Unlock()
		return
	}
	if exec := vm.activeExecution; exec == nil || !exec.concurrent.Load() {
		removeIntVar(env, name)
		removeFloatVar(env, name)
		removeInlineVar(env, name)
		delete(env.Vars, name)
		return
	}
	env.mu.Lock()
	removeIntVar(env, name)
	removeFloatVar(env, name)
	removeInlineVar(env, name)
	delete(env.Vars, name)
	env.mu.Unlock()
}

func (vm *Interpreter) declareInt(name string, value int, env *Env) {
	if env.shared {
		env.mu.Lock()
		removeInlineVar(env, name)
		removeFloatVar(env, name)
		delete(env.Vars, name)
		setOrAppendIntVar(env, name, value)
		env.mu.Unlock()
		return
	}
	if exec := vm.activeExecution; exec == nil || !exec.concurrent.Load() {
		removeInlineVar(env, name)
		removeFloatVar(env, name)
		delete(env.Vars, name)
		setOrAppendIntVar(env, name, value)
		return
	}
	env.mu.Lock()
	removeInlineVar(env, name)
	removeFloatVar(env, name)
	delete(env.Vars, name)
	setOrAppendIntVar(env, name, value)
	env.mu.Unlock()
}

func (vm *Interpreter) declareFloat(name string, value float64, env *Env) {
	if env.shared {
		env.mu.Lock()
		removeIntVar(env, name)
		removeInlineVar(env, name)
		delete(env.Vars, name)
		setOrAppendFloatVar(env, name, value)
		env.mu.Unlock()
		return
	}
	if exec := vm.activeExecution; exec == nil || !exec.concurrent.Load() {
		removeIntVar(env, name)
		removeInlineVar(env, name)
		delete(env.Vars, name)
		setOrAppendFloatVar(env, name, value)
		return
	}
	env.mu.Lock()
	removeIntVar(env, name)
	removeInlineVar(env, name)
	delete(env.Vars, name)
	setOrAppendFloatVar(env, name, value)
	env.mu.Unlock()
}

// setInt updates an existing integer binding without boxing it. false means
// the name is either undefined or currently holds a dynamic (non-int) value.
func (vm *Interpreter) setInt(name string, value int, env *Env) bool {
	exec := vm.activeExecution
	// See getInt: one execution cannot gain a second guest goroutine before
	// reserveGoroutine has published concurrent, so one snapshot is enough for
	// this complete lexical update.
	concurrent := exec != nil && exec.concurrent.Load()
	for e := env; e != nil; e = e.Parent {
		if e.shared || concurrent {
			e.mu.Lock()
			_, ok := lookupIntVar(e, name)
			if !ok {
				e.mu.Unlock()
				continue
			}
			setOrAppendIntVar(e, name, value)
			e.mu.Unlock()
			return true
		}
		if _, ok := lookupIntVar(e, name); ok {
			setOrAppendIntVar(e, name, value)
			return true
		}
	}
	return false
}

// setFloat updates only an existing unboxed float binding. A false result
// leaves the caller to use the fully dynamic assignment path.
func (vm *Interpreter) setFloat(name string, value float64, env *Env) bool {
	exec := vm.activeExecution
	concurrent := exec != nil && exec.concurrent.Load()
	for e := env; e != nil; e = e.Parent {
		if e.shared || concurrent {
			e.mu.Lock()
			_, ok := lookupFloatVar(e, name)
			if ok {
				setOrAppendFloatVar(e, name, value)
				e.mu.Unlock()
				return true
			}
			e.mu.Unlock()
			continue
		}
		if _, ok := lookupFloatVar(e, name); ok {
			setOrAppendFloatVar(e, name, value)
			return true
		}
	}
	return false
}

// addInt updates an existing integer binding by delta and returns the new value.
// It avoids a separate get+set roundtrip for hot loops that only mutate one
// integer lvalue via ++/--.
func (vm *Interpreter) addInt(name string, delta int, env *Env) (int, bool) {
	exec := vm.activeExecution
	concurrent := exec != nil && exec.concurrent.Load()
	for e := env; e != nil; e = e.Parent {
		if e.shared || concurrent {
			e.mu.Lock()
			cur, ok := lookupIntVar(e, name)
			if !ok {
				e.mu.Unlock()
				continue
			}
			next := cur + delta
			setOrAppendIntVar(e, name, next)
			e.mu.Unlock()
			return next, true
		}
		cur, ok := lookupIntVar(e, name)
		if !ok {
			continue
		}
		next := cur + delta
		setOrAppendIntVar(e, name, next)
		return next, true
	}
	return 0, false
}

// --------------- Lvalue references for assignments ---------------

type Ref interface {
	Get() any
	Set(any) error
}

// lvalueRef is the evaluator's concrete lvalue representation. Assignment
// used to return the exported Ref interface here, forcing every indexed or
// field assignment through an interface value (and usually a tiny heap
// object). Keeping the kind and its payload in one value lets the evaluator
// dispatch directly while retaining Ref below for compatibility with callers
// that still need the generic abstraction.
type lvalueRefKind uint8

const (
	lvalueVar lvalueRefKind = iota
	lvalueSliceIndex
	lvalueMapIndex
	lvalueField
)

type lvalueRef struct {
	kind lvalueRefKind
	vm   *Interpreter
	env  *Env
	name string
	s    *SliceVal
	i    int
	m    *MapVal
	k    any
	sv   *StructVal
}

func (r lvalueRef) get() any {
	switch r.kind {
	case lvalueVar:
		v, _ := r.vm.get(r.name, r.env)
		return v
	case lvalueSliceIndex:
		return r.s.Data[r.i]
	case lvalueMapIndex:
		v, _ := r.m.getByKey(r.k)
		return v
	case lvalueField:
		v, _ := r.sv.field(r.name)
		return v
	default:
		return nil
	}
}

func (r lvalueRef) set(v any) error {
	switch r.kind {
	case lvalueVar:
		r.vm.set(r.name, v, r.env)
	case lvalueSliceIndex:
		r.s.Data[r.i] = v
	case lvalueMapIndex:
		if v == nil {
			r.m.deleteByKey(r.k)
		} else {
			r.m.setByKey(r.k, v)
		}
	case lvalueField:
		r.sv.setField(r.name, v)
	}
	return nil
}

func (r lvalueRef) Get() any        { return r.get() }
func (r lvalueRef) Set(v any) error { return r.set(v) }

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

func (r *fieldRef) Get() any        { v, _ := r.s.field(r.name); return v }
func (r *fieldRef) Set(v any) error { r.s.setField(r.name, v); return nil }

// ------------------- Call frames for defer/panic ------------------

// callFrame tracks one active function invocation. Besides its own defers,
// it links to the call site's frame (caller), forming a call stack that
// debug.Stack() walks. That link is set once at construction (see
// callFunction) and never mutated afterward, so it is safe to read from a
// goroutine other than the one that created it — the only case where that
// happens is a spawned goroutine's frame pointing back at its launching
// call's frame, which by then is already fully built.
//
// panicking/panicVal implement the guest-visible recover() builtin. A frame
// is only ever read or written by the single goroutine that owns it (the
// same one running its defers), so no locking is needed even though the
// struct is also linked into other goroutines' `caller` chains for
// debug.Stack(). recover() works by checking env.frame.caller.panicking:
// that is true only while the CURRENT function is executing as a deferred
// call of the panicking frame (caller is set to the panicking frame exactly
// in that case — see callFunction/DeferStmt), which is what makes it match
// Go's "recover must be called directly by a deferred function" rule
// without any extra bookkeeping.
type callFrame struct {
	defers    []func()
	funcName  string
	caller    *callFrame
	panicking bool
	panicVal  any
	// depth is caller.depth+1 (0 for a goroutine's outermost call), set once
	// at construction. DebugController uses frame *identity* (not depth) to
	// decide step-over/into/out, but depth is cheap to keep around for
	// display in DebugPauseInfo/stack traces without re-walking the chain.
	depth int
	// namedResult is the current function's single named result variable
	// (see Function.Results), or "" if it has none — nanoGo's return value
	// model only ever carries one logical value, so a function with two or
	// more named results still gets them declared as ordinary locals (so
	// referencing them by name works) but does not get this wired up: only
	// the single-named-result case feeds back into what the function
	// actually returns. Set once by callFunction; ReturnStmt reads it via
	// env.frame to know which variable a naked `return` should read, and
	// to make an explicit `return expr` also update it (so a later defer —
	// notably one that calls recover() — can still change the value the
	// caller ultimately receives, exactly as Go's own named-result +
	// deferred-mutation semantics work).
	namedResult string
}

// collectLocalVars gathers every binding visible from env, innermost scope
// shadowing outer ones, stopping at (and excluding) globals — used by
// debug.Vars() to give guest code a snapshot of its own local state.
func (vm *Interpreter) collectLocalVars(env *Env) map[string]any {
	out := make(map[string]any)
	exec := vm.activeExecution
	for e := env; e != nil && e != vm.globals; e = e.Parent {
		locked := e.shared || (exec != nil && exec.concurrent.Load())
		if locked {
			e.mu.RLock()
		}
		if iv := e.inlineIntVar; iv.name != "" {
			if _, seen := out[iv.name]; !seen {
				out[iv.name] = iv.val
			}
		}
		if floats := e.inlineFloats; floats != nil {
			for i := 0; i < int(floats.len); i++ {
				fv := floats.vars[i]
				if _, seen := out[fv.name]; !seen {
					out[fv.name] = fv.val
				}
			}
		}
		for i := 0; i < int(e.inlineLen); i++ {
			vv := e.inlineVars[i]
			if _, seen := out[vv.name]; !seen {
				out[vv.name] = vv.val
			}
		}
		for name, v := range e.Vars {
			if _, seen := out[name]; !seen {
				out[name] = v
			}
		}
		if locked {
			e.mu.RUnlock()
		}
	}
	return out
}
