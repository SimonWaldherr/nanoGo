# nanoGo

> **A Go-subset interpreter for native hosts and WebAssembly** — execute supported Go source dynamically in a browser playground, REPL, CLI, or embedded host.

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Go Version](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)](https://go.dev/)
[![WebAssembly](https://img.shields.io/badge/WebAssembly-654FF0?logo=webassembly&logoColor=white)](https://webassembly.org/)
[![DOI](https://zenodo.org/badge/1075593525.svg)](https://doi.org/10.5281/zenodo.18649874)


## 🚀 Overview

nanoGo is a **minimalist Go interpreter** written in Go. It can run Go source dynamically in a native host (CLI, REPL, or an embedding application) and, when built for `js/wasm`, in a browser. While projects like TinyGo compile Go programs to WASM, nanoGo instead compiles the interpreter to WASM and evaluates guest Go source at runtime.

**Key Distinction:** Instead of compiling Go programs ahead-of-time to WASM, nanoGo is an interpreter written in Go, compiled to WASM, that can execute Go source code dynamically at runtime.

## ✨ What nanoGo provides

nanoGo is useful where executing a small, controlled Go-like program at runtime is more important than compiling a complete Go application. It parses and evaluates a supported subset of Go; it does **not** invoke the Go compiler or perform complete static type checking. Consequently, a program that works in nanoGo is not automatically portable to Go, and a valid Go program may use language or library features nanoGo does not implement.

- **Interactive execution:** edit and run supported Go source in a browser, REPL, CLI, or embedded host.
- **Concurrency primitives:** guest goroutines, channels, `select`, timers, and a `sync.WaitGroup` subset.
- **Curated libraries:** a documented, partial set of standard-library-like packages plus browser and host integrations.
- **Host-controlled access:** filesystem and HTTP operations are denied by default in a newly created interpreter and must be granted through `Capabilities` and host natives. The browser playground deliberately enables its private VFS and browser-mediated HTTP; CORS still applies.
- **Resource controls:** hosts can set execution deadlines, step limits, container-size limits, and goroutine limits. These controls are cooperative and do not make arbitrary host natives safe.

The browser playground deliberately raises only its per-run deterministic step
budget to 50 million checkpoints so its long-running visual demos can finish;
the interpreter default for generic embedded hosts remains 10 million. The
playground still creates one interpreter per run and exposes **Stop**.

### 💡 **Unique Use Cases**
- **Interactive Tutorials**: Create Go learning platforms that run entirely in the browser
- **Browser-Based IDEs**: Build web-based development environments without server-side execution
- **Client-Side Data Processing**: Perform complex computations on user data without sending it to servers
- **Live Code Demonstrations**: Showcase Go algorithms and patterns with interactive examples
- **Educational Tools**: Teach Go programming with zero installation requirements
- **Prototyping & Experimentation**: Quickly test Go ideas without local setup

## 🌟 Features

### Core Capabilities
- ✅ **Supported Go subset**: Variables, functions, structs, slices, maps, interfaces, channels, and selected control-flow constructs
- ✅ **Concurrency**: Goroutines and channels, including cancellation-aware waits
- ✅ **Built-in Packages**: Curated subsets of `fmt`, `time`, `sync`, `math`, `math/rand`, `strings`, `regexp`, `sort`, `strconv`, `path`, `unicode/utf8`, `encoding/json`/`json`, `os`, `fs`, and `testing`
- ✅ **Browser Integration**: Special `browser` package for DOM manipulation and canvas drawing
- ✅ **HTTP facade**: Browser code can use nanoGo's `http.GetText`/`PostText` facade, subject to CORS; embedded hosts must provide and authorize a transport native
- ✅ **Template helper**: `text/template.RenderString` expands a template; it does not provide `html/template` escaping
- ✅ **Session storage**: The playground's worker-local `storage` facade persists only while that worker remains alive
- ✅ **Math & random subsets**: Selected `math` and `math/rand` functions
- ✅ **Multi-Package Modules**: Load multi-file, multi-package programs from a VFS module, run/hot-swap individual functions, and run tests and benchmarks that use nanoGo's supported `testing` subset (see [interp/loader](interp/loader) and [interp/index](interp/index))
- ✅ **Browser IDE Workspace**: Persistent file tabs, multi-file upload/create, `go.mod` snapshots, package/import checks, project runs, and a keyboard command palette
- ✅ **WASM SDK Surface**: `nanoGoVersion`, `nanoGoRunWorkspace`, `nanoGoWorkspaceCheck`, and `nanoGoTestWorkspace` expose capability/limit metadata plus module-aware execution/testing for custom frontends

### Execution Modes
- **🌐 Web Playground**: Interactive browser-based Go editor with live execution
- **🖥️ CLI Interpreter**: Run Go scripts from the command line
- **📝 REPL Mode**: Interactive Read-Eval-Print-Loop for experimentation

### 🧭 Go Execution Lab (web playground)

The web playground includes an **Execution Lab** alongside Console, Canvas,
HTML, and Inspector. It projects nanoGo's existing AST-backed static call
graph into a clickable [Mermaid](https://mermaid.js.org/) flowchart:

- **Visualize current code** asks the WASM worker for the current static call
  graph; this is analysis, not a record of one particular runtime execution.
- Selecting a function in the diagram jumps to its source line. The Lab can
  place source-linked breakpoint markers in CodeMirror. **Debug run** records
  bounded breakpoint-hit events and line heat, turns hit markers amber, and
  lets the replay controls jump through the recorded source locations.
  This is deterministic record/replay, not a live pause: the worker finishes
  the run before the browser replays it, so no evaluator goroutine is blocked
  waiting for UI input.
- Mermaid loads on demand and is included in the service worker cache after
  installation, so a previously opened playground can visualize code offline.

### ✨ Opt-in AI assistant (web playground)

The **AI** tab is a small browser-side client for an OpenAI-compatible
`/chat/completions` endpoint, inspired by the local-first, explicit-context
workflow of [liveCalc](https://github.com/SimonWaldherr/liveCalc). It does not
add a nanoGo server or silently send code:

- The endpoint, model, API key, and "include context" setting are kept in the
  browser's `localStorage`; conversation messages are not persisted.
- Nothing is sent until **Ask AI** is pressed. With context enabled, the
  request contains the current Go source and the Lab's static graph summary.
- Assistant-proposed Go is never applied automatically. A user must press
  **Apply Go block to editor** after reviewing a fenced `go` block.

Use a trusted endpoint and browser profile: `localStorage` is convenient for
personal development, not a hardened secret vault. Direct browser requests
also require the selected endpoint to allow the playground origin via CORS.

### Safety Features
- **Capability-gated execution**: The interpreter denies its built-in filesystem and HTTP facades by default; a host must still securely implement every native it registers
- **Controlled natives**: Host functions are explicit capabilities, not an automatic security boundary
- **No Direct Host File System Access**: Guest access goes through a capability-checked VFS or explicitly registered host native

## 🎮 Quick Start

### Try It Online

There is no hosted playground URL in this repository. Build and serve the included playground locally as shown below.

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

### Use nanoGo as a Go library in your own project

nanoGo is a normal Go module (`simonwaldherr.de/go/nanogo`) — add it with `go
get` and import the interpreter package directly, no separate service or
build step required:

```bash
go get simonwaldherr.de/go/nanogo
```

```go
import "simonwaldherr.de/go/nanogo/interp"
```

The shortest complete example — an interpreter, the two natives `fmt`
needs, and one guest program — lives in
[examples/quickstart](examples/quickstart):

```bash
go run ./examples/quickstart
```

For the single most common real-world embedding — running untrusted guest
snippets inside a `net/http` handler, one isolated interpreter per request,
with a request-scoped timeout and deterministic step/goroutine limits — see
[examples/http_server](examples/http_server):

```bash
go run ./examples/http_server
```

The rest of this document covers progressively more advanced host
integrations (host channels, request context, capabilities, VFS mounts,
tracing); see the "🔌 Embedding nanoGo in Other Applications" section below
for embedding the *browser playground* itself in another web page.

## 🔌 Embedding nanoGo in Other Applications

nanoGo is meant to be embedded, not just visited. Three integration paths
are supported, from "drop an iframe into a blog post" to "build your own
frontend from scratch":

### 1. Embed the playground as an `<iframe>`

The playground reads its initial state entirely from the URL, so embedding
it needs no build step and no server-side code — just an `<iframe src="...">`:

```html
<iframe
  src="https://simonwaldherr.github.io/nanoGo/?embed=1&autorun=1#code=BASE64_ENCODED_GO_SOURCE"
  width="640" height="420" loading="lazy" title="nanoGo playground">
</iframe>
```

Query-string options (combine freely):

| Param | Effect |
|---|---|
| `embed=1` | Compact, iframe-friendly layout: sidebar, promo panel, and the advanced inspector are hidden; only the editor and one output panel remain. An "Open in nanoGo ↗" link appears so a visitor can pop out to the full playground with the same code. |
| `autorun=1` | Runs the code automatically once WebAssembly finishes loading. |
| `readonly=1` | Makes the editor read-only (`CodeMirror`'s `nocursor` mode) — useful for "here's the output" embeds where the visitor shouldn't edit the snippet. |
| `example=Name` | Preloads a built-in example by its exact name (e.g. `example=FizzBuzz`) instead of a `#code=` payload. |
| `theme=name` | Sets the initial color theme (`dark`, `light`, `solar`, `dracula`, `sepia`, `monokai`, `ocean`, `forest`, `midnight`, `pastel`, `high-contrast`). |
| `#code=<base64>` | The editor's initial source, base64-encoded UTF-8 (`btoa(unescape(encodeURIComponent(source)))` in JS). Takes priority over `example=` when both are present. Lives in the hash, not the query string, so it never hits the server in a request log. |

The playground's header has a **🔗 Embed** button that generates this
`<iframe>` snippet (with `autorun`/`readonly` toggles) from whatever code is
currently in the editor, plus a **📤 Share** button for a plain link.

### 2. Control an embedded playground with `postMessage`

A page hosting the playground in an `<iframe>` can drive it and observe its
output without touching the iframe's DOM, via `window.postMessage`. No
origin allowlist is enforced on either side — like other embeddable widgets
(YouTube, CodeSandbox), the channel only ever affects the widget's own
editor/run state, never host page data — so restricting it, if you need to,
is left to your own `event.origin` check.

Outgoing (playground → `window.parent`):

| Message | When |
|---|---|
| `{type:'nanogo:ready'}` | WebAssembly finished loading; safe to send commands. |
| `{type:'nanogo:output', text, kind}` | One per console line. `kind` is `output`, `warn`, `error`, or `system`. |
| `{type:'nanogo:done', elapsed, stats}` | A run finished. `stats` mirrors the Inspector's `{elapsedMs, steps, ...}`. |

Incoming (your page → the iframe's `contentWindow`):

| Message | Effect |
|---|---|
| `{type:'nanogo:set-code', code}` | Replaces the editor's contents. |
| `{type:'nanogo:set-workspace', files, modulePath, active}` | Replaces the browser VFS snapshot; each file is `{path, source}`. |
| `{type:'nanogo:run'}` | Starts a run, as if the Run button was clicked. |
| `{type:'nanogo:workspace-check'}` | Resolves the current module/import graph without executing it. |
| `{type:'nanogo:workspace-run'}` | Runs the complete current multi-file workspace. |
| `{type:'nanogo:workspace-test'}` | Runs the supported `TestXxx` functions across the workspace. |
| `{type:'nanogo:stop'}` | Stops the running program. |

```html
<iframe id="ng" src="https://simonwaldherr.github.io/nanoGo/?embed=1" width="640" height="420"></iframe>
<script>
  const frame = document.getElementById('ng');
  window.addEventListener('message', (event) => {
    if (event.data?.type === 'nanogo:ready') {
      frame.contentWindow.postMessage({ type: 'nanogo:set-code', code: 'package main\nimport "fmt"\nfunc main() { fmt.Println("hi from the host page") }' }, '*');
      frame.contentWindow.postMessage({ type: 'nanogo:run' }, '*');
    }
    if (event.data?.type === 'nanogo:output') console.log('[nanogo]', event.data.text);
  });
</script>
```

### 3. Build your own frontend from scratch

The production playground (`web/index.html`) is a full-featured, ~2000-line
CodeMirror-based editor — more than most integrations need to read through
just to learn the WASM worker's message protocol. [web/minimal.html](web/minimal.html)
and [web/app.js](web/app.js) are a from-scratch, framework-free reference
frontend (a plain `<textarea>`, a handful of buttons, no CDN dependency)
that implements the same protocol in well under 300 lines — a copyable
starting point for a custom UI. Build the WASM module once (`make
build-wasm`), then serve `web/` and open `minimal.html`.

The one non-obvious detail either reference implementation needs to get
right: `wasm_worker.js` coalesces high-frequency messages (console lines,
canvas updates) into `{type:'batch', items:[...]}` so a tight guest loop
costs one `postMessage` instead of thousands. A frontend that only handles
individual message types — without unwrapping `batch` first — will see
almost no output from anything that logs or draws in a loop.

#### Module-aware browser SDK

The worker also exposes a safe, snapshot-based project protocol for IDEs. A
frontend sends source contents rather than host paths; nanoGo copies them into
an isolated VFS, resolves local imports and `go.mod`, and returns package
metadata with the same loader used by native hosts:

```js
worker.postMessage({
  type: 'workspace-check',
  modulePath: 'example.com/demo',
  files: [
    { path: 'go.mod', source: 'module example.com/demo\n' },
    { path: 'main.go', source: 'package main\nfunc main() { greet() }\n' },
    { path: 'greet.go', source: 'package main\nfunc greet() {}\n' }
  ]
});

worker.postMessage({
  type: 'workspace-run',
  modulePath: 'example.com/demo',
  files: [/* same snapshot */],
  trace: true,
  profile: true
});

worker.postMessage({
  type: 'workspace-test',
  modulePath: 'example.com/demo',
  files: [/* same snapshot */],
  filter: 'Test'
});
```

`workspace-check-result` reports `{ok, workspace}` with the resolved module,
packages, files, and imports. `workspace-done` carries the normal run stats
plus `stats.workspace`; `workspace-test-result` aggregates the supported
`TestXxx` subset across packages. Paths are relative, must end in `.go` or be `go.mod`,
and are rejected if they escape the virtual workspace. Dependencies are
deliberately host-provided snapshots: the browser SDK never downloads or
executes an unreviewed remote module.

The full playground uses this protocol behind its compact **IDE workspace**
bar. `Ctrl/⌘+K` opens the command palette; `Check` performs the module-aware
resolution, and `Project` runs every file through `interp/loader`.

## 🤖 MCP Server for AI Coding Clients

`nanogo-mcp` turns nanoGo into a **safe, temporary Go workspace for an MCP
client**. MCP (Model Context Protocol) is the JSON-RPC protocol through which
AI coding clients discover tools and context. Rather than giving an agent
unrestricted shell access, this server lets it create Go files in an in-memory
virtual filesystem (VFS), inspect and test them, and execute them with
nanoGo's sandbox.

This makes it useful for:

- validating small Go examples, algorithms, and bug reproductions;
- teaching or reviewing Go code with parser, call-graph, and complexity data;
- iterating on a multi-file nanoGo module entirely in a temporary workspace;
- running the supported `TestXxx(t *testing.T)` subset before executing a
  program; and
- building agent workflows that need no access to the host disk or network.

Build the server with:

```bash
make build-mcp
```

The same executable speaks two interchangeable transports, so it can be
reached by the widest possible range of MCP clients and tooling:

- **stdio** (default) — JSON-RPC messages on stdin/stdout, diagnostics on
  stderr. This is what Claude Desktop, Claude Code, and most editor
  integrations expect to launch as a private subprocess.
- **Streamable HTTP** (`-http addr`) — a single `/mcp` endpoint per the MCP
  spec's HTTP transport, for anything that reaches the server over a
  network instead of spawning it: web-based MCP clients, remote or hosted
  agents, or several clients sharing one running server.

Configure a stdio-based client (exact configuration location is
client-specific) with a command entry shaped like:

```json
{
  "mcpServers": {
    "nanogo": {
      "command": "/absolute/path/to/nanoGo/build/nanogo-mcp"
    }
  }
}
```

For an HTTP-based client, start the server listening on a port:

```bash
build/nanogo-mcp -http :8080
# or: NANOGO_MCP_HTTP_ADDR=:8080 build/nanogo-mcp
```

and point the client at `http://localhost:8080/mcp`. `GET /` and
`GET /healthz` are unauthenticated plain-text/JSON probes useful for a quick
`curl` check or a container health check; CORS is open to any origin so
browser-based clients can reach it directly.

Each client gets its own isolated in-memory workspace: the stdio transport
serves one implicit session for the process's lifetime, while the HTTP
transport allocates a fresh session (and `Mcp-Session-Id` header) on every
`initialize` call, so concurrent clients never see each other's files.
Idle HTTP sessions are reclaimed automatically after 30 minutes; a client can
also end one explicitly with `DELETE /mcp` (`Mcp-Session-Id` header
required).

Either transport negotiates MCP protocol versions from `2024-11-05` through
`2025-11-25`, and exposes the `nanogo://guide` resource with its workflow and
safety notes.

### Agent workflow and tools

For a single snippet, call `fmt_code`, `vet_code`, `inspect_code`, or
`call_graph` before `run_code`. For a project, create `go.mod` and source files
with `vfs_write` (`create_parents: true`), use `vfs_tree` or `index_module` to
orient in the workspace, run `test_module`, and finish with `run_module`.

| Need | MCP tools |
|---|---|
| Execute a self-contained program | `run_code` |
| Format and fast static checks | `fmt_code`, `vet_code` |
| Understand source structure without execution | `inspect_code`, `call_graph` |
| Understand a complete VFS project | `index_module`, `vfs_tree`, `vfs_stat` |
| Run a multi-file module or its tests | `run_module`, `test_module` |
| Manage the temporary workspace | `vfs_read`, `vfs_write`, `vfs_list`, `vfs_mkdir`, `vfs_chdir`, `vfs_remove` |

`inspect_code`, `call_graph`, and `index_module` return compact JSON in MCP
text content, so clients can reason over the result without executing guest
code. Their result sizes are bounded by `max_functions`, `max_depth`, or
`max_entries` parameters where appropriate.

### Safety model and limits

Each session's VFS lives only as long as that session does — the whole
process for stdio, or until it idles out or is explicitly deleted for HTTP —
and is never the host disk. Guest Go code can read and write that VFS, but it
cannot access the host filesystem or network through this server. Execution
timeouts default to 10 seconds and are limited to 1–60 seconds, in addition to
nanoGo's interpreter resource limits. Static analysis is syntactic and
best-effort; it complements rather than replaces the Go compiler or `go vet`.
The HTTP transport's CORS policy and lack of authentication assume a trusted
network (localhost or an internal one) — put it behind your own auth/reverse
proxy before exposing it beyond that.

### Project Structure

```
nanoGo/
├── cmd/
│   ├── cli/        # Native CLI interpreter
│   ├── mcp/        # MCP server for AI coding clients
│   ├── repl/       # Interactive REPL
│   └── wasm/       # WebAssembly build for the browser
├── examples/       # Runnable Go-embedding examples (quickstart, http_server, ...)
├── interp/         # Go interpreter implementation
├── runtime/        # Runtime support (browser APIs, stdlib)
├── samples/        # Example Go programs
└── web/            # Web playground frontend
    ├── index.html      # Full-featured playground (CodeMirror, inspector, themes)
    ├── minimal.html     # From-scratch reference frontend, paired with app.js
    ├── app.js           # ...its ~300-line vanilla-JS controller
    ├── examples.js      # Source for every built-in playground example
    ├── wasm_worker.js   # Web Worker hosting the WASM interpreter
    └── nanogo.wasm      # Built by `make build-wasm` (not checked in)
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
    
    // nanoGo returns the decoded value; unlike encoding/json in Go, it does
    // not take a destination pointer or return an error.
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
`debug.Mark` adds a named timeline marker. `debug.Stack` returns the current
guest call stack (innermost call first) as a string, including across `go`
statements — a spawned goroutine's stack chains back through its launch site.
`debug.Vars` returns every local binding visible at the call site (innermost
scope wins on shadowing) as sorted `name = value` lines. `debug.Assert(cond,
msg...)` fails the run with `msg` (default `"assertion failed"`) when `cond`
is false, and records the failure on the tracer even without guest stdout.
`Tracer.Events()` provides a bounded, chronological trace of runs, calls,
guest goroutines, denied capabilities, and debug probes—well suited to a
traceGL-like timeline or a custom local UI.

```go
tracer := interp.NewTracer(2_048)
vm.SetTracer(tracer)

// Guest source: import "debug"
// debug.Q(total); debug.Mark("before send")
// fmt.Println(debug.Stack()); fmt.Println(debug.Vars())
// debug.Assert(total > 0, "total must be positive")
if err := vm.RunContext(ctx, source); err != nil { /* ... */ }
for _, event := range tracer.Events() {
    fmt.Println(event.Sequence, event.Kind, event.Location, event.Message)
}
```

The tracer is in-memory and bounded; it does not grant the guest filesystem or
network access. See [examples/capabilities](examples/capabilities) and
[examples/debug_trace](examples/debug_trace) for runnable host programs.

Hosts can configure source breakpoints without enabling tracing globally. When
a `Tracer` is attached, each matching statement produces a bounded
`breakpoint` event with its function and source location:

```go
vm.SetBreakpoints([]int{12, 27})
vm.SetTracer(interp.NewTracer(4096))
```

`SetBreakpoints` is an atomic, immutable line-set swap and adds no source-line
lookup cost when no breakpoints are configured.

### Host runtime trace integration

The browser inspector intentionally uses nanoGo's compact `Tracer`: it records
guest-level events and can be sent as small JSON to the page. A native embedding
host can additionally mirror those same high-level events as user annotations
in Go's execution trace. This is opt-in, keeps the normal fast path free of
trace formatting, and combines with the Go runtime's own scheduler, GC, and
host-goroutine events:

```go
f, err := os.Create("nanogo-trace.out")
if err != nil { panic(err) }
defer f.Close()

if err := runtimeTrace.Start(f); err != nil { panic(err) }
vm.SetRuntimeTraceAnnotations(true)
err = vm.RunContext(ctx, source)
runtimeTrace.Stop()
if err != nil { /* handle guest error */ }

// Inspect with: go tool trace nanogo-trace.out
```

The annotations use categories such as `nanogo.call_start` and
`nanogo.goroutine_start`. `golang.org/x/exp/trace` is deliberately not a runtime
dependency: it is an experimental trace reader, and Go 1.25's stable
`runtime/trace.FlightRecorder` supersedes its recorder for new host code.

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
- **Data**: `encoding/json` (also available as `json`), `strings`, `regexp`, `sort`, `strconv`, `path`, `unicode/utf8`
- **Math**: selected `math` and `math/rand` functions
- **Text & tooling**: `text/template`, `debug`, and a supported subset of `testing`
- **Host-bound APIs**: `browser`, `storage`, `fs`, `os`, and `http`; filesystem/network calls require `Capabilities`, while APIs that reach host resources require the corresponding host native

This is deliberately not the full Go standard library: each listed package is
a subset with only the functions described by its registration in
[`interp/packages.go`](interp/packages.go). In particular,
`encoding/json.Unmarshal` returns the decoded value instead of filling a
pointer, so guest code should follow nanoGo's API rather than assume complete
stdlib compatibility. `path` provides `Base`, `Clean`, `Dir`, `Ext`, `IsAbs`,
and `Join`; `unicode/utf8` provides `RuneCountInString`, `RuneLen`,
`ValidRune`, and `ValidString`, plus `RuneError`, `RuneSelf`, and `UTFMax`.

### Multi-Package Programs & Tooling

Beyond a single `package main` file, nanoGo can load a small multi-file,
multi-package module straight from its VFS:

- **`interp.ParsePackageDir`/`PackageScope`**: merge every `.go` file in one
  directory into a single scope, two-phase (collect every type/func first,
  then evaluate var initializers), so forward references across files work
  regardless of file order.
- **`interp/loader`**: `LoadModule` walks a VFS tree, resolves local imports
  against the `go.mod` module path, local `replace` directives, explicitly
  configured VFS dependency roots, and nanoGo's curated builtin packages. It
  never downloads code or interprets `go.sum`: the embedding host decides
  exactly which dependency snapshots enter the VFS. `ModuleCache` reuses a
  parsed dependency graph until the VFS revision changes, then invalidates it
  automatically. The loader detects import cycles and topologically orders
  packages so `init()`/package-level `var` initialization runs dependency-first.
  `RunProgram` then builds and runs the whole program. `RunFunctionTest`/`RunFunctionBench` call one
  function directly against data-driven cases (useful for exercise grading).
  `RunPackageTests`/`RunPackageBenchmarks` run `TestXxx(t *testing.T)` and
  `BenchmarkXxx(b *testing.B)` functions from `_test.go` files when they use
  nanoGo's supported `testing` subset (`T.Error`, `T.Errorf`, `T.Fatal`,
  `T.Fatalf`, `T.Skip`, `T.Skipf`, `T.Run`, `T.Helper`, and `B.N`/timer
  controls). Both internal `package foo` tests and Go-style external
  `package foo_test` API tests are supported; test-only variables and `init()`
  functions, as well as packages imported only by `_test.go`, load lazily with
  the test overlay and never with the production program. Those test files can
  use the normal Go `testing` package unchanged under real `go test` as well.
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

For dynamic, host-controlled dependency snapshots, point module paths at VFS
roots. A local `replace example.com/lib => ./third_party/lib` in `go.mod`
works too. Reuse a cache when an editor or MCP session repeatedly loads an
unchanged multi-file module; every result remains safe to build with a
different interpreter.

```go
cache := loader.NewModuleCache(vm.VFS)
prog, err := cache.Load("/app", loader.Options{
    DependencyRoots: map[string]string{
        "example.com/shared": "/deps/shared",
    },
})
if err != nil { /* handle the invalid module */ }
// VFS writes invalidate this parsed graph automatically on the next Load.
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

# Run supported TestXxx functions from a multi-file module snapshot
./build/nanogo-cli test ./my-module

# Build REPL
make build-repl

# Run tests
make test

# Static checks, races and a short coverage-guided fuzz pass
make vet
make test-race
make fuzz

# Host benchmarks (time + allocations) and an on-demand CPU profile
make benchmark-go
make profile-cpu  # then: go tool pprof build/nanogo-cpu.pprof
make profile-mem  # then: go tool pprof -alloc_space build/nanogo-mem.pprof

# Native Go execution trace with nanoGo user annotations
make trace        # then: go tool trace build/nanogo.trace

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

The playground's **Test** button runs `TestXxx(t *testing.T)` functions from
the current editor document through the same nanoGo `testing` subset as the
module loader. The adjacent optional filter accepts a `go test -run`-style Go
regular expression; test names and assertion messages are shown in the output.
For a true multi-file project, use MCP `test_module` or the CLI command
`nanogo-cli test <module-dir> [package-name]`.

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

The size of `nanogo.wasm` depends on the Go toolchain and build inputs. Use
`make size-report` after a local build instead of relying on a fixed size. A
few deployment tweaks can improve cold-load time:

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

Version static assets deliberately. Only then is a long-lived immutable cache
policy safe; otherwise a browser can retain an outdated worker or WASM file.

### 4. Built-in Service Worker

The playground registers `web/sw.js`, which uses a cache-first strategy for
the core offline assets:

- `index.html`
- `styles.css`
- `wasm_exec.js`
- `wasm_worker.js`
- `examples.js`
- `nanogo.wasm`
- `manifest.webmanifest`
- `assets/logo.svg`
- the CodeMirror CDN files

That can improve repeat visits when the assets are already cached. The cache is
versioned per deploy: the GitHub Pages workflow replaces the `__BUILD_ID__`
token in `CACHE_NAME` with the commit SHA, so every deployment gets a distinct
cache and returning visitors automatically pick up changed assets. For local
development (served without that substitution), bump the `ui<N>` suffix in
`web/sw.js`'s `CACHE_NAME` if you need to force a fresh cache.

## 🎯 Use Cases

### 1. **Educational Platforms**
Create interactive Go tutorials where students can write and execute code without installing anything:
- No server-side execution needed
- Feedback without a local Go toolchain
- Capability-constrained execution when the host configures restrictive limits and natives

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
- Host-configured, capability-constrained evaluation; do not treat the playground defaults as a competition-security boundary

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

## 📊 Positioning

nanoGo interprets supported Go source at runtime. It is therefore best suited
to small playgrounds, teaching tools, controlled automation, and embedded
experiments—not as a substitute for compiling a production Go application to
WebAssembly. Measure the generated WASM size and runtime characteristics for
your own workload.

## 📝 Limitations

- **Subset of Go**: Not all Go features are supported (reflection, CGO, unsafe)
- **Performance**: Interpreted execution is slower than compiled WASM
- **Standard Library**: Limited subset of Go's stdlib available
- **No Reflection**: Advanced reflection features not implemented
- **Browser-Only WASM**: Desktop WASM runtimes not tested

## 🗺️ Roadmap

- [ ] **Enhanced Package Support**: More stdlib packages
- [x] **Debugger Integration (record/replay)**: Source breakpoints, bounded
  hit events, and trace replay in the browser; live pause/continue remains a
  future worker protocol.
- [ ] **Performance Optimizations**: JIT compilation, bytecode caching
- [x] **Module System**: Multi-file/multi-package programs loaded from a VFS module (local packages + `go.mod` module path only — no external package downloads); see [interp/loader](interp/loader)
- [ ] **Advanced Types**: Better interface and generics support
- [x] **IDE Features (workspace core)**: Persistent multi-file tabs, project
  check/run, command palette, SDK capability metadata, and source-linked
  diagnostics; completion and refactoring remain future work.
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
