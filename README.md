# nanoGo

> **A lightweight Go interpreter designed for WebAssembly** — Bringing the power of Go to the browser with minimal overhead

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Go Version](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)](https://go.dev/)
[![WebAssembly](https://img.shields.io/badge/WebAssembly-654FF0?logo=webassembly&logoColor=white)](https://webassembly.org/)
[![DOI](https://zenodo.org/badge/1075593525.svg)](https://doi.org/10.5281/zenodo.18649874)


## 🚀 Overview

nanoGo is a **minimalist Go interpreter** written in Go. It can run Go source dynamically in a native host (CLI, REPL, or an embedding application) and, when built for `js/wasm`, in a browser. While projects like TinyGo compile Go programs to WASM, nanoGo instead compiles the interpreter to WASM and evaluates guest Go source at runtime.

**Key Distinction:** Instead of compiling Go programs ahead-of-time to WASM, nanoGo is an interpreter written in Go, compiled to WASM, that can execute Go source code dynamically at runtime.

## ✨ Why Go in the Browser (via WebAssembly)?

Running Go in the browser through WebAssembly opens up exciting possibilities that traditional JavaScript development cannot easily match:

### 🎯 **Type Safety & Performance**
- **Strong Static Typing**: Catch errors at compile-time rather than runtime, leading to more robust browser applications
- **Near-Native Performance**: WebAssembly executes at near-native speed, making Go code in the browser significantly faster than equivalent JavaScript for compute-intensive tasks
- **Predictable Performance**: Go's garbage collector and memory management provide consistent performance characteristics

### 🔧 **Developer Experience**
- **Familiar Tooling**: Use the Go toolchain, testing framework, and ecosystem you already know
- **Code Reuse**: Share business logic between backend (Go servers) and frontend (browser) without rewrites
- **Goroutines in the Browser**: Leverage Go's powerful concurrency primitives (goroutines, channels) for complex async operations
- **Standard Library Access**: Use familiar Go packages like `fmt`, `time`, `sync`, `strings`, `regexp`, and more

### 🛡️ **Safety & Security**
- **Memory Safety**: Go's memory management eliminates entire classes of security vulnerabilities (buffer overflows, use-after-free)
- **Sandboxed Execution**: WebAssembly provides a secure sandbox, and nanoGo's interpreter adds another isolation layer
- **Type Safety**: Prevent common JavaScript pitfalls like type coercion bugs and undefined behavior

### 🌐 **Universal Platform**
- **Write Once, Run Everywhere**: The same Go code can run on servers, desktop, mobile, and now browsers
- **No Transpilation Hassles**: Unlike TypeScript or other compile-to-JS languages, you're running actual Go semantics
- **Future-Proof**: WebAssembly is a W3C standard supported by all major browsers

### 📦 **Lightweight & Portable**
- **Small Binary Size**: nanoGo's interpreter is extremely compact, making it ideal for embedded playground scenarios
- **No Runtime Dependencies**: Everything needed to run Go code is bundled in the WASM module
- **Instant Load Times**: Fast initialization means your Go code starts executing quickly

### 💡 **Unique Use Cases**
- **Interactive Tutorials**: Create Go learning platforms that run entirely in the browser
- **Browser-Based IDEs**: Build web-based development environments without server-side execution
- **Client-Side Data Processing**: Perform complex computations on user data without sending it to servers
- **Live Code Demonstrations**: Showcase Go algorithms and patterns with interactive examples
- **Educational Tools**: Teach Go programming with zero installation requirements
- **Prototyping & Experimentation**: Quickly test Go ideas without local setup

## 🌟 Features

### Core Capabilities
- ✅ **Go Language Support**: Variables, functions, structs, interfaces, slices, maps
- ✅ **Concurrency**: Goroutines and channels, including cancellation-aware waits
- ✅ **Built-in Packages**: A curated subset including `fmt`, `time`, `sync`, `math`, `strings`, `regexp`, `json`, `sort`, `os`, `fs`, and `testing`
- ✅ **Browser Integration**: Special `browser` package for DOM manipulation and canvas drawing
- ✅ **HTTP Client**: Make HTTP requests from Go code in the browser
- ✅ **Template Engine**: `text/template` support for dynamic content generation
- ✅ **Browser Storage**: Persist data for the active playground session
- ✅ **Math & Random**: Full `math` and `math/rand` package support
- ✅ **Multi-Package Modules**: Load multi-file, multi-package programs from a VFS module, run/hot-swap individual functions, and run tests and benchmarks that use nanoGo's supported `testing` subset (see [interp/loader](interp/loader) and [interp/index](interp/index))

### Execution Modes
- **🌐 Web Playground**: Interactive browser-based Go editor with live execution
- **🖥️ CLI Interpreter**: Run Go scripts from the command line
- **📝 REPL Mode**: Interactive Read-Eval-Print-Loop for experimentation

### Safety Features
- **Sandboxed Execution**: Safe interpreter environment prevents malicious code
- **Controlled Natives**: Limited host function access for security
- **No Direct Host File System Access**: Guest access goes through a capability-checked VFS or explicitly registered host native

## 🎮 Quick Start

### Try It Online

Visit the **[nanoGo Web Playground](#)** to start writing Go code in your browser immediately—no installation required!

### Build Locally

```bash
# Clone the repository
git clone https://github.com/SimonWaldherr/nanoGo.git
cd nanoGo

# Build WebAssembly module for web playground
make build-wasm

# Build native CLI interpreter
make build-cli

# Run a demo
make run-demo
```

### Project Structure

```
nanoGo/
├── cmd/
│   ├── wasm/       # WebAssembly build for browser
│   ├── cli/        # Command-line interpreter
│   └── repl/       # Interactive REPL
├── interp/         # Go interpreter implementation
├── runtime/        # Runtime support (browser APIs, stdlib)
├── samples/        # Example Go programs
└── web/            # Web playground frontend
    ├── index.html
    ├── app.js
    └── nanogo.wasm
```

## 📖 Usage Examples

### Example 1: Hello World

```go
package main

import "fmt"

func main() {
    fmt.Println("Hello from Go in the browser!")
}
```

### Example 2: Buffered Channels

```go
package main

import (
    "fmt"
)

func main() {
    ch := make(chan int, 5)
    for i := 0; i < 5; i++ {
        ch <- i * 2
    }
    close(ch)

    for val := range ch {
        fmt.Println("Received:", val)
    }
    fmt.Println("Done!")
}
```

### Example 3: Browser DOM & Canvas

```go
package main

import "browser"

func main() {
    browser.SetHTML("output", "<p>Rendered by nanoGo</p>")

    browser.CanvasSize(8, 8)
    browser.CanvasSet(1, 1, true)
    browser.CanvasSet(2, 2, true)
    browser.CanvasFlush()

    browser.ConsoleLog("Canvas updated")
}
```

### Example 4: Time & Async Operations

```go
package main

import (
    "fmt"
    "time"
)

func main() {
    fmt.Println("Starting timer...")
    start := time.Now()
    
    time.Sleep(1000) // Sleep for 1 second (milliseconds in nanoGo)
    
    elapsed := time.Since(start)
    fmt.Printf("Elapsed: %v\n", elapsed)
}
```

### Example 5: JSON Processing

```go
package main

import (
    "fmt"
    "json"
)

func main() {
    data := map[string]any{
        "name": "nanoGo",
        "version": "1.0",
        "features": []string{"wasm", "browser", "lightweight"},
    }
    
    jsonStr := json.Marshal(data)
    fmt.Println("JSON:", jsonStr)
    
    parsed := json.Unmarshal(jsonStr)
    fmt.Println("Parsed:", parsed)
}
```

### Example 6: WaitGroup, Goroutine, Channel, and `defer`

This pattern waits for a worker before the result channel is closed. The
deferred `Done` call runs even when the worker returns early.

```go
package main

import (
    "fmt"
    "sync"
)

func main() {
    jobs := make(chan int, 2)
    results := make(chan int, 2)
    jobs <- 2
    jobs <- 3
    close(jobs)

    var wg sync.WaitGroup
    wg.Add(1)
    go func() {
        defer wg.Done()
        for job := range jobs {
            results <- job * job
        }
    }()

    wg.Wait()
    close(results)
    for result := range results {
        fmt.Println("square:", result)
    }
}
```

### Example 7: Deferred calls run in LIFO order

`defer` records a call when it is encountered and runs deferred calls when the
surrounding function returns, last registered first.

```go
package main

import "fmt"

func main() {
    defer fmt.Println("cleanup 1")
    defer fmt.Println("cleanup 2")
    fmt.Println("work")
}
```

### Controlled execution, timeout, and host messaging

Use `RunContext` whenever source is not fully trusted. It checks the context
while evaluating code and while waiting on nanoGo channels, `select`,
`time.Sleep`, and nanoGo's `sync.WaitGroup`. `Kill` provides the same
cooperative interruption for a currently running interpreter.

`HostChannel` is a bidirectional bridge with two intentionally separate guest
variables: `hostIn` accepts host-to-guest data, while `hostOut` sends data back
to the host. Guest code cannot close either endpoint or write to `hostIn`.

```go
ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
defer cancel()

vm := interp.NewInterpreter()
interp.RegisterBuiltinPackages(vm)

bridge := interp.NewHostChannel(16)
if err := vm.BindHostChannel("hostIn", "hostOut", bridge); err != nil {
	log.Fatal(err)
}

if err := bridge.Send(ctx, "ping"); err != nil {
	log.Fatal(err)
}
err := vm.RunContext(ctx, `package main
func main() {
	request := <-hostIn
	hostOut <- "echo: " + request
}`)
if err != nil {
	log.Fatal(err)
}
reply, err := bridge.Receive(ctx) // "echo: ping"
```

`Run` remains available for compatibility and uses a background context. An
interpreter allows one active run at a time. Cancellation is cooperative: a
legacy host native that blocks indefinitely cannot be forcibly killed by Go.
For blocking host work, register it with `RegisterNativeContext`, which receives
the current `context.Context`. The default interpreter also limits allocations
made through `make` and `append` to `DefaultMaxContainerSize` entries; set
`vm.MaxContainerSize` before execution when a trusted workload needs more.

### Request context across the host boundary

`RunContext` already passes cancellation and deadlines through the interpreter:
they interrupt evaluation, guest channel operations, and context-aware host
natives. Go context values are intentionally opaque and cannot be safely
enumerated, so nanoGo does **not** receive a raw `context.Context`. Instead,
allow-list the small set of serializable request fields that the guest needs.

```go
type contextKey string
const requestIDKey contextKey = "request-id"

ctx = context.WithValue(ctx, requestIDKey, "req-7f8c")
if err := vm.BindHostContext("hostContext", ctx,
    interp.ContextField{Name: "requestID", Key: requestIDKey},
); err != nil {
    log.Fatal(err)
}

// Use the same context, or a child of it, for execution.
if err := vm.RunContext(ctx, source); err != nil { /* ... */ }
```

The guest sees a copied map such as
`hostContext["values"]["requestID"]`, plus `hasDeadline` and
`deadlineUnixMilli`. It can mutate its local copy without changing host data.
Useful shared data is request/trace IDs, tenant-safe identifiers, locale,
feature flags, and bounded input/configuration. Keep credentials, session
tokens, raw user profiles, database handles, host functions, and capability
objects on the host side. Use `HostChannel` for live request/response events
and a deliberately scoped VFS only when persistent guest data is required.

See [examples/host_context](examples/host_context) for a runnable program.

For deterministic limits in addition to a wall-clock context, configure
`vm.Limits`. `MaxSteps` caps evaluator checkpoints and `MaxGoroutines` caps
simultaneously active guest goroutines. Set an individual value to `0` only for
trusted code where that restriction should be disabled.

```go
vm.Limits = interp.ExecutionLimits{
    MaxSteps:      5_000_000,
    MaxGoroutines: 64,
}
```

Runnable host integrations are available in [examples/host_bridge](examples/host_bridge),
[examples/host_context](examples/host_context), and
[examples/resource_limits](examples/resource_limits):

```bash
go run ./examples/host_bridge
go run ./examples/host_context
go run ./examples/resource_limits
```

### Capability policy: filesystem and network

The curated `os`, `fs`, and `http` packages are **deny-by-default**. Registering
a host native alone does not grant the matching package permission: configure
the interpreter before `RunContext`.

```go
vm.Capabilities = interp.Capabilities{
    FileSystem: interp.FileSystemCapabilities{
        Read:  true,
        Write: false, // os.WriteFile, Remove, Mkdir, Setenv stay denied
        // Optional absolute VFS roots; relative guest paths and ".." are
        // resolved before this allow-list is checked.
        ReadPaths: []string{"/home/user/project/assets"},
    },
    Network: interp.NetworkCapabilities{
        HTTP:         true,
        AllowedHosts: []string{"api.example.com", "*.trusted.example"},
        // Literal private/loopback IPs remain denied by default.
    },
}
```

`AllowedHosts` is matched against the URL host before the HTTP host-native is
called. An empty allow-list means any host after explicitly setting `HTTP: true`.
The transport native must still enforce DNS/IP egress restrictions, because DNS
can resolve a permitted hostname to an internal IP address. `FullCapabilities()`
exists for trusted development code only. Direct `RegisterNative` functions are
explicit host capabilities and must enforce their own authorization.

`ReadPaths` and `WritePaths` are optional VFS-root allow-lists. A matching root
grants access to the root and its descendants; an empty list permits every VFS
path after the respective `Read` or `Write` bit has been granted. Policy roots
must be absolute. This is also applied to host-proxied `fs.ReadFile`, which
receives the canonical, checked absolute VFS path. A host native must still
enforce the equivalent policy when it maps that VFS path onto real files.

### Virtual filesystems and safe host imports

`VFS` is an in-memory filesystem that can be shared by selected interpreter
instances with `NewInterpreterWithVFS`. For host-provided content, use a
snapshot import rather than exposing a live host directory. `MountFS` accepts
any `io/fs.FS`—including `embed.FS`—and seals the mounted tree read-only.
`ImportDir` copies a host folder, while `ImportReader` copies bounded request
data and checks its context between reads.

```go
vfs := interp.NewVFS()
assets, err := fs.Sub(embeddedAssets, "assets")
if err != nil { log.Fatal(err) }
if err := vfs.MountFS("/assets", assets); err != nil { log.Fatal(err) }

// Reader data is copied, capped, and never kept as a host Reader reference.
if err := vfs.ImportReader(ctx, "/input/request.json", request.Body, 1<<20); err != nil {
    log.Fatal(err)
}

vm := interp.NewInterpreterWithVFS(vfs)
vm.Capabilities = interp.Capabilities{FileSystem: interp.FileSystemCapabilities{
    Read: true, ReadPaths: []string{"/assets", "/input"},
}}
```

Imports default to at most `DefaultVFSImportMaxFiles` files and
`DefaultVFSImportMaxBytes` bytes; tune those values with `VFSImportOptions`.
Read-only mounts still require the guest's `Read` capability, and never grant
the guest direct access to the host directory or Reader. See
[examples/vfs_mount](examples/vfs_mount) for a runnable `embed.FS` example.

### Local debugging timeline

`debug.Q` is a q-style probe: it preserves the guest expression and its value,
but records it in a host-owned tracer rather than writing to guest stdout.
`debug.Mark` adds a named timeline marker. `Tracer.Events()` provides a bounded,
chronological trace of runs, calls, guest goroutines, denied capabilities, and
debug probes—well suited to a traceGL-like timeline or a custom local UI.

```go
tracer := interp.NewTracer(2_048)
vm.SetTracer(tracer)

// Guest source: import "debug"; debug.Q(total); debug.Mark("before send")
if err := vm.RunContext(ctx, source); err != nil { /* ... */ }
for _, event := range tracer.Events() {
    fmt.Println(event.Sequence, event.Kind, event.Location, event.Message)
}
```

The tracer is in-memory and bounded; it does not grant the guest filesystem or
network access. See [examples/capabilities](examples/capabilities) and
[examples/debug_trace](examples/debug_trace) for runnable host programs.

## 🏗️ Architecture

### Interpreter Design

nanoGo implements a **tree-walking interpreter** that parses Go source code into an Abstract Syntax Tree (AST) and evaluates it directly:

1. **Lexing & Parsing**: Go source → AST using Go's `go/parser` package
2. **Type Resolution**: Basic type checking and struct/interface definition
3. **Evaluation**: Tree-walking evaluation with environment chaining
4. **Runtime**: Native function bindings for stdlib-like functionality

### WebAssembly Integration

```
┌─────────────────┐
│   Web Browser   │
│                 │
│  ┌───────────┐  │
│  │  HTML/JS  │  │
│  └─────┬─────┘  │
│        │        │
│  ┌─────▼─────┐  │
│  │   WASM    │  │
│  │  (nanoGo) │  │
│  └─────┬─────┘  │
│        │        │
│  ┌─────▼─────┐  │
│  │ Interpreter│ │
│  └─────┬─────┘  │
│        │        │
│  ┌─────▼─────┐  │
│  │  Go Code  │  │
│  └───────────┘  │
└─────────────────┘
```

### Package System

nanoGo includes a curated set of built-in packages:

- **Core**: `fmt`, `sync`, `time`
- **Data**: `encoding/json` (also available as `json`), `strings`, `regexp`, `sort`, `strconv`
- **Math**: `math`, `math/rand`
- **Text & tooling**: `text/template`, `debug`, and a supported subset of `testing`
- **Host-bound APIs**: `browser`, `storage`, `fs`, `os`, and `http`; filesystem/network calls require `Capabilities`, while APIs that reach host resources require the corresponding host native

This is deliberately not the full Go standard library. In particular,
`encoding/json.Unmarshal` returns the decoded value instead of filling a
pointer, so guest code should follow nanoGo's API rather than assume complete
stdlib compatibility.

### Multi-Package Programs & Tooling

Beyond a single `package main` file, nanoGo can load a small multi-file,
multi-package module straight from its VFS:

- **`interp.ParsePackageDir`/`PackageScope`**: merge every `.go` file in one
  directory into a single scope, two-phase (collect every type/func first,
  then evaluate var initializers), so forward references across files work
  regardless of file order.
- **`interp/loader`**: `LoadModule` walks a VFS tree, resolves local imports
  against a `go.mod` module path (only the `module` line is parsed — no
  `go.sum`, no downloads) and nanoGo's curated builtin packages, detects
  import cycles, and topologically orders packages so `init()`/package-level
  `var` initialization runs dependency-first. `RunProgram` then builds and
  runs the whole program. `RunFunctionTest`/`RunFunctionBench` call one
  function directly against data-driven cases (useful for exercise grading).
  `RunPackageTests`/`RunPackageBenchmarks` run `TestXxx(t *testing.T)` and
  `BenchmarkXxx(b *testing.B)` functions from `_test.go` files when they use
  nanoGo's supported `testing` subset (`T.Errorf`, `T.Fatalf`, `T.Run`,
  `T.Helper`, and `B.N`/timer controls). Those test files can use the normal
  Go `testing` package unchanged under real `go test` as well.
  `ReplaceFunction` hot-swaps one function's implementation without
  reloading the rest of the program.
- **`interp/index`**: pure `go/parser`/`go/ast` static analysis (no
  `go/types`) over a VFS tree — one entry per function/method with a
  best-effort, typeless call graph (`Calls`/`CalledBy`), which tests call
  which functions, and simple AST-based metrics (cyclomatic complexity,
  nesting depth, LOC).

**Known limitation**: struct types are registered in one shared, global
registry across every loaded package (matching `Run`/`RunContext`'s
existing behavior) — they are not namespaced per package, so two different
packages defining a same-named struct type collide (the last one registered
wins). Functions and package-level vars are properly isolated per package,
with Go's normal export rule (only capitalized names are visible through an
import alias).

Runnable examples: [examples/multi_package](examples/multi_package),
[examples/function_test](examples/function_test),
[examples/benchmark](examples/benchmark), and
[examples/index](examples/index). Additional focused examples cover
[concurrency](examples/concurrency) (`WaitGroup`, goroutines, channels, and
`defer`) and [hot-swap](examples/hot_swap) (`ReplaceFunction`):

```bash
go run ./examples/multi_package
go run ./examples/function_test
go run ./examples/benchmark
go run ./examples/index
go run ./examples/concurrency
go run ./examples/hot_swap
```

### Use loader, tests, hot-swap, and index from a host application

After populating `vm.VFS` with a module (including its `go.mod`), load it once
and register the curated packages before running it:

```go
ctx := context.Background()
vm := interp.NewInterpreter()
interp.RegisterBuiltinPackages(vm)

prog, err := loader.LoadModule(vm.VFS, "/app", loader.Options{})
if err != nil { /* handle the invalid module */ }
if err := loader.RunProgram(ctx, vm, prog, "main"); err != nil { /* handle */ }
```

`RunFunctionTest` builds the program when needed and returns one classified
result per case. Once the program is built, `ReplaceFunction` changes the
implementation for later calls without reloading imports or other packages:

```go
results, err := loader.RunFunctionTest(ctx, vm, prog, "main.Add", []loader.TestCase{
    {Args: []any{2, 3}, Want: 5},
})
if err != nil { /* handle */ }
_ = results

if err := loader.ReplaceFunction(vm, prog, "main", "Add", `
func Add(a, b int) int { return a - b }
`); err != nil { /* handle */ }
```

For static analysis, `index.Scan` works directly from the same VFS and does
not execute guest code:

```go
entries, err := index.Scan(vm.VFS, "/app", index.Options{})
if err != nil { /* handle */ }
for _, entry := range entries {
    fmt.Println(entry.ID, entry.Calls, entry.Metrics.CyclomaticComplexity)
}
```

## 🔨 Building & Development

### Prerequisites

- Go 1.25.0 or later
- Make (optional, for convenience)

### Build Commands

```bash
# Build everything
make all

# Build WebAssembly module only
make build-wasm

# Build the WASM and emit pre-compressed .gz / .br variants
# (brotli is optional; the target falls back gracefully when not installed)
make build-wasm-compressed

# Print uncompressed / gzip / brotli sizes of web/nanogo.wasm
make size-report

# Build CLI interpreter
make build-cli

# Build REPL
make build-repl

# Run tests
make test

# Run a quick timing benchmark of the demo program
make benchmark

# Clean build artifacts
make clean

# Tidy and vet
make tidy
```

### REPL

The REPL keeps declarations and imported aliases in one interpreter session,
but no longer reparses every prior import for each statement. Each evaluation
has a 10-second cancellation-aware timeout by default; `Ctrl-C` cooperatively
stops a running guest program.

```text
:timeout 2s    # set a per-evaluation deadline
:timeout off   # disable it for trusted experiments
:fmt <code>    # format a snippet
:vet <code>    # inspect a statement
```

### Running the Web Playground

```bash
# Build WASM
make build-wasm

# Serve the web directory (use any HTTP server)
python3 -m http.server 8080 --directory web

# Open http://localhost:8080 in your browser
```

### Testing

```bash
# Run the native test suite: interpreter, module loader, static index, and CLI
make test

# Or run a single package while working on it
go test ./interp
go test ./interp/loader
go test ./interp/index
go test ./cmd/cli
```

`go test ./...` is not the native test command for this repository: `cmd/wasm`
and `runtime` import `syscall/js` and must be built for `GOOS=js GOARCH=wasm`,
while `samples/` intentionally contains multiple independent `main` programs.
Use `make build-wasm` to verify the WASM target instead.

## ⚡ Performance & Deployment

The web playground ships a single ~7.7 MB `nanogo.wasm` artifact. A few simple
deployment tweaks dramatically improve cold-load time:

### 1. Serve pre-compressed WASM

`make build-wasm-compressed` produces `web/nanogo.wasm.gz` and (if `brotli` is
installed) `web/nanogo.wasm.br`. Typical sizes:

| Encoding     | Size          |
|--------------|---------------|
| uncompressed | ~7.7 MB       |
| gzip (-9)    | ~2.0 MB (-74%)|
| brotli (-11) | ~1.4 MB (-82%)|

Run `make size-report` after building to see the exact numbers for your tree.

#### nginx

```nginx
location /nanogo.wasm {
    gzip_static  on;          # serves nanogo.wasm.gz when client supports gzip
    brotli_static on;         # serves nanogo.wasm.br when client supports br
    types { application/wasm wasm; }
    default_type application/wasm;
    add_header Cache-Control "public, max-age=31536000, immutable";
}
```

#### Caddy

```caddyfile
@wasm path *.wasm
header @wasm Content-Type application/wasm
header @wasm Cache-Control "public, max-age=31536000, immutable"
encode zstd gzip
```

### 2. Streaming instantiation

The worker uses `WebAssembly.instantiateStreaming(fetch(...), ...)`, so make
sure your server returns `Content-Type: application/wasm` for `.wasm`
responses (most modern servers do this automatically). The worker
transparently falls back to the buffered path if streaming is unavailable
or the MIME type is wrong.

### 3. HTTP caching

`nanogo.wasm`, `wasm_exec.js`, `app.js`, and CodeMirror are all immutable
between releases. Setting `Cache-Control: public, max-age=31536000, immutable`
turns repeat visits into ~0-byte loads.

### 4. Built-in Service Worker

The playground registers `web/sw.js`, which uses a cache-first strategy for
the core offline assets:

- `index.html`
- `styles.css`
- `wasm_exec.js`
- `wasm_worker.js`
- `examples.js`
- `nanogo.wasm`
- the CodeMirror CDN files

That means repeat visits can start instantly even when the WASM is large or
the network is slow. When you change any of those assets, bump the
`CACHE_NAME` in `web/sw.js` so clients refresh their cached copy immediately.

## 🎯 Use Cases

### 1. **Educational Platforms**
Create interactive Go tutorials where students can write and execute code without installing anything:
- No server-side execution needed
- Instant feedback
- Safe sandbox environment

### 2. **Live Documentation**
Embed executable Go examples directly in documentation:
- Readers can modify and run examples
- Interactive API demonstrations
- Real-time algorithm visualization

### 3. **Browser-Based Tools**
Build sophisticated web applications with Go logic:
- Data processing tools
- Calculators and simulators
- Algorithm visualizers
- Format converters

### 4. **Prototyping & Experimentation**
Quick Go experimentation without local setup:
- Test algorithms
- Explore Go features
- Share code snippets with colleagues

### 5. **Code Challenges & Competitions**
Host programming competitions with Go:
- Browser-based coding environment
- Immediate code execution
- Fair sandboxed evaluation

## 🤝 Contributing

Contributions are welcome! Here's how you can help:

1. **Report Bugs**: Open an issue describing the problem
2. **Suggest Features**: Propose new functionality or improvements
3. **Submit PRs**: Fix bugs or implement features
4. **Improve Docs**: Help make documentation clearer
5. **Share Examples**: Add interesting sample programs

### Development Workflow

```bash
# Fork and clone the repository
git clone https://github.com/YourUsername/nanoGo.git

# Create a feature branch
git checkout -b feature/amazing-feature

# Make your changes and test
make test

# Build and verify
make all

# Commit and push
git commit -m "Add amazing feature"
git push origin feature/amazing-feature

# Open a Pull Request
```

## 📊 Comparison with Alternatives

| Feature | nanoGo | TinyGo | GopherJS |
|---------|--------|--------|----------|
| **Compilation** | Interpreted | AOT to WASM | Transpiled to JS |
| **Binary Size** | Very Small | Small-Medium | Large |
| **Dynamic Execution** | ✅ Yes | ❌ No | ❌ No |
| **Full Go Stdlib** | ❌ Subset | ⚠️ Partial | ✅ Yes |
| **Concurrency** | ✅ Goroutines | ✅ Goroutines | ✅ Goroutines |
| **Performance** | Medium | High | Medium |
| **Use Case** | Playgrounds, REPL | Production apps | Legacy projects |

## 📝 Limitations

- **Subset of Go**: Not all Go features are supported (reflection, CGO, unsafe)
- **Performance**: Interpreted execution is slower than compiled WASM
- **Standard Library**: Limited subset of Go's stdlib available
- **No Reflection**: Advanced reflection features not implemented
- **Browser-Only WASM**: Desktop WASM runtimes not tested

## 🗺️ Roadmap

- [ ] **Enhanced Package Support**: More stdlib packages
- [ ] **Debugger Integration**: Step-through debugging in browser
- [ ] **Performance Optimizations**: JIT compilation, bytecode caching
- [x] **Module System**: Multi-file/multi-package programs loaded from a VFS module (local packages + `go.mod` module path only — no external package downloads); see [interp/loader](interp/loader)
- [ ] **Advanced Types**: Better interface and generics support
- [ ] **IDE Features**: Code completion, syntax highlighting improvements
- [x] **Testing Framework**: A `testing.T`/`testing.B` subset runs unmodified `TestXxx`/`BenchmarkXxx` functions, plus a data-driven function test/benchmark harness and hot-swap; see [interp/loader](interp/loader)

## 📄 License

nanoGo is licensed under the **GNU General Public License v3.0**. See [LICENSE](LICENSE) for full details.

This means you can:
- ✅ Use nanoGo for commercial projects
- ✅ Modify and distribute nanoGo
- ✅ Use nanoGo in your applications

Under the condition that:
- ⚠️ Derivative works must also be GPL-3.0 licensed
- ⚠️ Source code must be made available

## 🙏 Acknowledgments

- Built with Go's standard `go/parser` and `go/ast` packages
- Inspired by minimal interpreter designs
- WebAssembly support powered by Go's WASM target
- Community contributions and feedback

## 📞 Contact & Links

- **Repository**: [github.com/SimonWaldherr/nanoGo](https://github.com/SimonWaldherr/nanoGo)
- **Author**: Simon Waldherr
- **Issues**: [GitHub Issues](https://github.com/SimonWaldherr/nanoGo/issues)

---

**⭐ Star this project if you find it useful!**

*Bringing the elegance of Go to the browser, one goroutine at a time.*
