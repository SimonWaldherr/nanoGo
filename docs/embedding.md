# Embedding and isolated execution

nanoGo remains a domain-neutral Go-subset interpreter. The existing
`RegisterNative`, `RegisterNativeContext`, `RegisterPackage`, `RunContext`,
`WithExecution`, `CallEntry`, and loader APIs remain available. New copying
adapters and prepared execution build on those mechanisms.

## Inputs and named results

Run `go run ./examples/prepared` for a complete native example. It prepares one
program and runs it with two inputs; the guest's global counter is `1` on both
runs. Each run returns its results independently of console output.

```go
program, err := loader.PrepareSource(`package main
import "nanogo/host"
func main() {
    value, err := host.Input("value")
    if err != nil { panic(err) }
    if err := host.Emit("answer", value); err != nil { panic(err) }
}`)
if err != nil { return err }
result, err := program.Run(ctx, loader.RunOptions{
    Inputs: map[string]any{"value": 42},
    Configure: func(vm *interp.Interpreter) error {
        vm.Limits.MaxCallDepth = 64
        vm.Limits.MaxAllocationUnits = 100_000
        vm.Limits.MaxOutputBytes = 64 << 10
        vm.Limits.MaxResultBytes = 1 << 20
        return nil
    },
})
// err may be non-nil even when result.Results.Events contains partial output.
if err != nil { return err }
if result.Results.Committed {
    consume(result.Results.Events)
}
```

With an existing interpreter, call `vm.BindInputs(inputs)` before execution and
read `vm.LastResults()` afterward. `EnableResults()` installs the same package
with no inputs. `host` is also exposed as a convenience global; the explicit
`import "nanogo/host"` form works with both single-file and module execution.
Inputs are looked up by name; a missing input is an ordinary guest error.
`host.Input(name)` returns `(any, error)` and copies on every access.
`host.Emit(name, value)` returns an ordinary guest error for invalid names or
unsupported data. Resource exhaustion terminates the execution even when the
guest ignores the return value.

An event is `{Name, Value}`. Names need not be unique; consumers choose their
aggregation policy. Ordering follows emission order and is unspecified between
racing guest goroutines. Values are copied at emission, and `LastResults` makes
another copy for its caller. Results are published only after all guest work has
joined. `Committed` is true only after the execution scope completes without an
error. Failed/cancelled scopes retain their partial events with `Committed=false`.
The next execution clears the previous outcome, including a pre-cancelled run.
A live event stream or automatic external side-effect rollback is not provided.

## Copying host functions

`RegisterHostFunction(name, HostFunctionSpec, callback)` adds exact argument and
result counts to `RegisterNativeContext`. The callback receives ordinary copied
Go data plus the active context. It returns `[]any` containing its declared
results; zero results, one result, and multiple results remain distinct. An
incorrect argument count is rejected before the callback; an incorrect result
count fails execution with `ErrHostContract`.

A callback's second `error` return is an interpreter failure and preserves its
error chain. To return an ordinary guest `(value, error)` pair, declare two
results and return `[]any{value, guestError}, nil`. Error-valued results are
snapshotted as plain errors containing their message; arbitrary host error
objects are not exposed. Explicit `interp.ReturnValues` also gives low-level natives declared tuple
semantics. A singleton tuple is unwrapped in expression contexts; assignment
validates its result count. Legacy raw natives retain the old two-target
`(value,error)` adapter for compatibility. New host functions and JSON APIs use
explicit tuples and strict failures, so target count never changes their contract.

Callbacks must validate their domain's argument types and honor cancellation
while blocking. Registering a function is an explicit capability grant; it does
not gain an implicit filesystem/network restriction. Existing internal-native
registration remains the correct mechanism for primitives intended only for
capability-gated wrappers. Host callbacks and their captured state must obey the
host's own concurrency rules; guest goroutines can invoke callbacks concurrently.

## Bridge values and ownership

`BridgeToGuest` and `BridgeToHost` retain their copying behavior.
`BridgeToGuestWithLimits` and `BridgeToHostWithLimits` let a host select
`BridgeLimits`. Set `Interpreter.BridgeLimits` for inputs/results and
`HostChannel.BridgeLimits` for individual channel messages before concurrent use.
These overrides provide a migration path for previously unbounded integrations.
Zero fields select defaults: 64 levels, 1,048,576 values,
16 MiB of scalar/key payload. Strings/keys count UTF-8 bytes, numbers eight bytes,
bools one byte and host byte-slice elements one byte. This is a traversal/payload
bound, not actual heap accounting. Guest byte storage uses numeric elements and
therefore may consume a larger traversal byte budget than a native byte slice.

Host inputs support nil, bool, string, int, int64, finite float64, `[]byte`,
`[]any`, scalar slices (`int`, `float64`, `bool`, `string`), and string-keyed
maps of `any`, `string`, or `int`. Guest output supports corresponding containers,
named scalar values, and guest structs as field maps. Typed nil slices and maps
retain their nil storage; empty allocated containers remain non-nil. Struct
fields beginning with `__` are internal and omitted, preserving the existing
bridge convention. JSON tags apply to the JSON facade, not this field-map bridge.

Functions, channels, arbitrary host pointers, guest pointer values, cycles,
excessive nesting, non-finite numbers and oversized values return errors wrapping
`ErrBridgeValue`. Guest maps with non-string keys are now explicitly rejected:
the old bridge could stringify distinct keys to the same host string and lose
data. Convert keys deliberately or use `json.Marshal` for its documented key
subset. Shared acyclic subvalues are copied independently. Hosts must not mutate
values concurrently with conversion, and should stop guest mutation before
calling `BridgeToHost` themselves. The result API performs conversion on the
emitting guest goroutine; other guest goroutines must synchronize shared data.

## Diagnostics

Use `interp.DiagnosticFor(err, phase)` for any API error; it returns nil on
success. `phase` is a fallback for uncategorized errors (`load`, `runtime`, or
`host`). `WithDiagnostic` adds a phase while preserving the Go error chain.
Runtime errors retain the concrete `*RuntimeError` contract. Other structured
wrappers support `errors.Is` and `errors.As`, and `Error()` retains useful text.

The JSON form contains `code`, `phase`, `message`, and optional `location`,
`stack`, and `limit`. Location is `{file,line,column}`: lines and columns start
at 1, and columns count UTF-8 **bytes**, matching `go/token`. They are not UTF-16
JavaScript indices. Zero/absent locations mean unavailable. Parser diagnostics
currently expose the first syntax location while the message may contain more
parse errors. Stack frames include function names and declaration locations,
innermost first, capped at 64. Panic replacement by a defer preserves the new
panic's origin. Native failures may have no guest source position.

| Code | Meaning |
| --- | --- |
| `parse.syntax` | Source could not be parsed |
| `load.error` | Module/import/entry preparation failed |
| `runtime.error` | Interpreter runtime error |
| `runtime.panic` | Unrecovered guest or callback panic |
| `execution.canceled`, `execution.deadline`, `execution.killed` | Cancellation reason |
| `limit.steps`, `limit.goroutines` | Existing execution limit |
| `limit.calls`, `limit.outputBytes`, `limit.allocationUnits`, `limit.resultBytes` | Additional budget |
| `limit.typeDepth`, `limit.containerSize` | Zero-value construction refused |
| `host.contract`, `host.value` | Callback contract or bridge violation |

Clients should tolerate additional codes/fields. The browser protocol carries
the same diagnostic in run/workspace results and error replies. The JS client
preserves the response on `error.response` and exposes `error.diagnostic`.
[Offline SDK documentation](offline.md) covers worker-side lifecycle codes.

## Resource scope

All new execution limits default to disabled (zero) for compatibility. Existing
step, goroutine, and maximum-container defaults remain. Set limits before a run.

| Limit | Accounting |
| --- | --- |
| `MaxSteps` | Evaluator expression/statement checkpoints, including guest initialization |
| `MaxGoroutines` | Concurrent guest goroutines within the scope |
| `MaxCallDepth` | Simultaneously active guest/native call frames, summed across goroutines; ordinary call depth with one goroutine |
| `MaxOutputBytes` | Sum of `ToString` UTF-8 argument bytes passed to registered `ConsoleLog`, `ConsoleWarn`, `ConsoleError` callbacks, before forwarding; excludes host-added separators/newlines |
| `MaxAllocationUnits` | Cumulative requested guest container slots, described below |
| `MaxResultBytes` | Copied scalar/key bytes plus result name bytes and 16 units per event; empty events still consume budget |

Container accounting covers make slice capacity/map hints/channel capacity,
append backing-store growth, slice/array/map/struct literals, new map entries,
string-to-byte/rune slices, fixed-array/struct zero construction and JSON guest
containers/returned bytes. A unit is a requested slot, not a byte. A map hint
and later entry insertion may both be charged. Append charges the requested new
backing-store size when growth is needed; Go's extra allocator capacity is not
measured. Freed containers are not refunded. Zero-value construction checks
container size and a maximum type nesting depth of 128 before recursion.

This is **not** a live-heap or hard process/WASM-memory cap. Parser/AST metadata,
string concatenation/formatting, interpreter bookkeeping, arbitrary callback
allocations and not-yet-instrumented library internals are outside that counter.
The output budget is checked before forwarding to registered console callbacks,
after any guest formatting needed to produce their arguments. It cannot bound
additional work/output performed directly by a host callback. Empty console
calls still require step/call budgets. Bridge/JSON payload limits are separate.
For adversarial workloads use a host deadline plus worker/process isolation;
a callback that ignores its context cannot be forcibly interrupted inside Go.

Existing `RunContext` uses one scope for initialization and main. Legacy
`loader.RunProgram` retains separate initialization and entry budgets and caches
mutable package scopes on its Program. `LastResults` and `LastStepCount` on that
API describe the most recent scope, usually entry. Prepared execution uses one
shared scope for initialization plus entry, including native calls, and returns
one outcome. Hosts requiring this unified contract should use prepared execution.

## Preparation, state and invalidation

`loader.PrepareSource` prepares a single document; `PrepareModule(vfs, root,
options)` prepares a module and explicitly supplied local dependencies. Preparation
uses the loader's existing parsed-metadata machinery. It takes one locked VFS
snapshot, including file contents, environment, current directory and read-only
flags. Source/import resolution and ASTs are private immutable preparation data.

Each `PreparedProgram.Run` allocates a new VM and VFS clone, registers builtins,
runs the optional Configure callback, binds copied inputs, initializes every
package, then invokes Entry (default `main`). Globals, package scopes, guest
closures, caches bound to execution, results and resource counters are fresh.
Host callbacks may intentionally share external state; that state is not cloned.
Configure must not retain and mutate the VM during execution.

Concurrent runs use independent VMs and VFSs; regression/race tests exercise
this isolation. The existing mutable `Program` and direct Interpreter APIs keep
their previous lifetime/concurrency contracts. Prepared programs expose neither
mutable ASTs nor hot-swap. Recreate preparation after source, dependencies,
module options or compatibility choices change. Edits to the original VFS do
not change an existing prepared snapshot. Per-run limits/capabilities/callbacks
are selected through Configure and do not mutate preparation. Builtin semantics
belong to the running nanoGo binary; prepared objects are not serialized across
versions. Existing `ModuleCache.Load` continues revision/options invalidation
for hosts that want automatic reloading rather than fixed snapshots.

## Migration and validation

- Existing URL browser integrations continue to work; [offline.md](offline.md)
  specifies the supplied-assets path and protocol compatibility.
- Standard JSON signatures deliberately change; [json.md](json.md) includes an
  explicit `nanogo/jsonlegacy` migration and executable examples. Assignment
  target counts never select a JSON signature.
- [go-compatibility.md](go-compatibility.md) scopes the semantic improvements and
  remaining Go-subset limitations.

Run `make test test-race test-compat test-web test-wasm test-wasm-artifact`.
`test-wasm-artifact` includes the actual SDK+worker supplied-assets smoke test.
Native benchmarks: `go test ./interp/loader -run '^$' -bench
'BenchmarkPreparedExecution|BenchmarkModuleCacheLoad' -benchmem` and
`go test ./interp -run '^$' -bench BenchmarkJSONRoundTrip -benchmem`.
`scripts/benchmark-wasm.cjs` separately measures actual WASM compile/startup,
first execution and repeated worker execution. Results are workload observations,
not native-to-WASM equivalence or an asserted speedup over earlier releases.
