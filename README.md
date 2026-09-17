# nanoGo

nanoGo is an embeddable interpreter for a subset of Go. Run scripts inside a Go
application or a browser, supply inputs, collect structured results, and control
execution without compiling each guest program. The interpreter is domain-neutral;
application behavior belongs in packages and host callbacks.

It includes a native CLI, REPL, MCP server, virtual filesystem, package loader,
and WebAssembly playground. It does not implement the entire Go language or
standard library. Read the [compatibility guide](docs/go-compatibility.md) before
porting an existing package.

## Run a program

The module declares Go 1.25 or later and has no external Go dependencies.
From a checkout:

```sh
go run ./cmd/cli samples/features_demo.go
go run ./examples/prepared
make build-cli build-repl build-mcp
```

The binaries are written to `build/`. The prepared example runs one program with
two different inputs and demonstrates that globals start fresh on each run.

A guest program can use familiar Go-style JSON:

```go
package main

import (
    "encoding/json"
    "fmt"
)

func main() {
    data, err := json.Marshal(map[string]int{"answer": 42})
    if err != nil { panic(err) }
    fmt.Println(string(data))
}
```

This prints `{"answer":42}`. Save it as a Go file and pass it to the CLI, or
paste it into the browser editor.

## Try the browser demo

```sh
make build-wasm
python3 -m http.server 8080 --directory web
```

Open [the local playground](http://localhost:8080). Select **Inputs & Results**
from Examples to try a data-processing program: edit its JSON inputs in the
**Data** panel, run it, and inspect named results separately from console logs.
The panel labels failed-run output as partial and shows structured diagnostics.
**Run** executes the current file or project; **Stop** replaces its worker.
Use Ctrl/Cmd+Enter to run, and the Code menu to format, vet, or test.

The playground also includes a multi-file workspace, canvas and HTML examples,
traces, a call graph, and debugging controls. Its optional AI panel sends a
request only when you ask, to the endpoint you configure. The full playground
loads editor/diagram assets from CDNs; its service worker cache is not a
first-load offline bundle.

For a dependency-free frontend, open `/minimal.html`. For a portable, single-file
HTML bundle containing all runtime assets, follow [offline embedding](docs/offline.md).
WASM and its matching `wasm_exec.js` must be built and shipped together.

## Embed nanoGo

| Task | Starting point |
| --- | --- |
| Run source in a Go host | [Integration guide](docs/integration.md), [quickstart example](examples/quickstart/main.go) |
| Reuse parsing with fresh state | [Prepared execution](docs/embedding.md#prepared-execution), [prepared example](examples/prepared/main.go) |
| Supply inputs and collect results | [Host bridge and execution contract](docs/embedding.md) |
| Add a browser worker | [JavaScript SDK](web/nanogo.mjs), [TypeScript declarations](web/nanogo.d.mts) |
| Serialize guest values | [JSON contracts](docs/json.md), [runnable example](examples/json/main.go) |
| Upgrade an older integration | [Migration guide](docs/upgrading.md) |
| Measure time, allocations, or download size | [Build and performance guide](docs/performance.md) |

Fresh interpreters deny filesystem and HTTP access by default. Hosts explicitly
grant capabilities and register callbacks. Cooperative execution budgets and
cancellation help bound guest work; they are not a hard Go/WASM memory cap and
cannot interrupt arbitrary blocking native code. See the
[accounting contract](docs/embedding.md#execution-budgets).

## Repository layout

| Directory | Contents |
| --- | --- |
| `interp/` | Evaluator, guest values, builtin packages, bridge, limits and VFS |
| `interp/loader/` | Module loading, cache, prepared execution and package tests |
| `interp/index/` | Static source index |
| `cmd/` | Native CLI, REPL, MCP and WASM entry points |
| `runtime/`, `web/` | Go browser bindings, SDK, worker and playground |
| `compat/` | Executable comparisons with standard Go |
| `examples/`, `samples/` | Host integrations and guest programs |
| `scripts/` | Artifact smoke tests, offline bundling and measurement tools |

## Develop and verify

```sh
make test test-compat test-web test-race
make vet vet-wasm fmt-check
make build-wasm test-wasm test-wasm-artifact
```

Node.js is needed for web and actual-WASM checks. These targeted commands avoid
building browser-only packages as native code or treating standalone sample
programs as one package. Do not use native tests as evidence of browser behavior.

Add a minimal regression before changing semantics. Use standard Go as the
reference for supported behavior; avoid map-order, scheduling and timing-based
expectations. Keep changes focused, preserve existing work, and document API
migrations. Generated binaries, profiles, `build/`, and all `.txt` files are
ignored. Retain shareable benchmark evidence as JSON.

Historical validation records: [embedding](docs/embedding-validation.md),
[runtime optimizations](docs/optimization-validation.md), and
[serialization](docs/serialization-validation.md). Each states its environment
and verification limits.

nanoGo is licensed under [GPLv3](LICENSE).
