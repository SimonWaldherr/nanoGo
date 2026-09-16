# Offline browser embedding and protocol compatibility

nanoGo's browser SDK supports URL assets, supplied WASM bytes, and a compiled
`WebAssembly.Module`. Every client owns a dedicated worker and an independent
Go runtime. The supplied-assets path includes the matching Go runtime and worker
source; it does not call `fetch`, `importScripts`, or `eval`.

## A self-contained HTML file

Using the existing Go and Node toolchain:

```sh
make build-wasm WASM_OUT=build/offline/nanogo.wasm
node scripts/build-offline.cjs \
  build/offline/nanogo.wasm build/offline/wasm_exec.js build/offline.html
```

Open `build/offline.html` in a browser, including directly from disk. The generated
file contains the browser SDK, classic worker, matching `wasm_exec.js`, and
base64-encoded WASM. It offers a source editor, bytes/module startup, worker
restart, and an executable **Verify offline lifecycle** check. It uses no CDN,
service worker, backend, or cross-origin isolation. The generated file is a build
artifact and is excluded from version control. Base64 increases the binary's
storage size; this is a portability example, not a smaller download format.

The verification button runs actual Go/WASM programs with both asset modes,
checks fresh globals on successive calls, recovery after a guest panic, deadline
termination with a queued request, and worker restart. Network entry points are
replaced with throwing functions in the page and worker; the page also checks
HTTP resource entries. An intentionally infinite guest loop exercises the
worker deadline; there is no performance threshold assertion.

## Supplying assets from a bundler or host

Provide assets as values using your bundler's existing raw-text/binary mechanism.
The SDK itself does not load them when `offline: true` is selected:

```js
import { createNanoGo, createInlineWorkerFactory } from './nanogo.mjs';

// Host-owned values, bundled into this application:
// wasmBytes: Uint8Array
// wasmExecSource: text of wasm_exec.js from the binary's Go toolchain
// workerSource: text of web/wasm_worker.js from this SDK version
const workerFactory = createInlineWorkerFactory({
  wasmExecSource,
  workerSource,
});

const client = await createNanoGo({
  offline: true,
  workerFactory,
  wasmBytes,
  requestTimeoutMs: 10_000,
  onMessage(message) {
    if (message.type === 'log') console.log(message.text);
  },
});
try {
  const response = await client.run('package main\nfunc main() { ConsoleLog(42) }');
  console.log(response.stats);
} finally {
  client.dispose();
}
```

To reuse compilation across fresh workers, provide
`wasmModule: await WebAssembly.compile(wasmBytes)` instead of `wasmBytes`.
A compiled module is immutable and structured-cloned to the worker; instances,
linear memory, Go state, and guest state are not shared.

Exactly one of `wasmBytes`, `wasmModule`, and `wasmURL` can be supplied. Byte
buffers and views are copied, including only the selected view's byte range;
the caller's buffer is never transferred or detached. SharedArrayBuffer-backed
views are rejected. Invalid binaries reject client initialization with the
underlying compilation/link failure in the message.

`createInlineWorkerFactory` accepts trusted JavaScript source. It creates a
classic Blob worker containing the Go runtime followed by the worker SDK. It
uses neither dynamic evaluation nor script imports. A host's Content Security
Policy must permit its chosen bootstrap mechanism, such as `worker-src blob:`;
an inline HTML example also needs its inline module script allowed. Browser
policies that prohibit Blob workers still apply. Do not treat guest-supplied
text as runtime or worker source.

## Custom worker creation and ownership

`workerFactory` may return a Worker directly, or a handle:

```js
const client = await createNanoGo({
  offline: true,
  wasmModule,
  workerFactory() {
    const worker = makeBundledWorker(); // must already contain wasm_exec.js
    return {
      worker,
      wasmExecProvided: true,
      dispose() { releaseHostResources(); },
    };
  },
});
```

The client terminates the worker and invokes `dispose` exactly once when it is
disposed, startup fails, the runtime fails, or a deadline expires. It also cleans
up a handle rejected for missing runtime support. A custom factory owns cleanup
if it throws before returning its handle. `createInlineWorkerFactory` handles
that case itself and revokes its Blob URL on construction failure. Successful
Blob URLs remain valid until client termination, then are revoked.

Do not share a Worker across clients. The client owns its message handlers and
termination. `wasmExecProvided: true` can alternatively be set directly on the
client options. It disables runtime `importScripts` and requires the worker's
Go constructor to exist. A host-provided factory is trusted code; it is
responsible for avoiding network access within its own bootstrap.

`offline: true` requires a factory, supplied WASM, and supplied runtime. It
rejects URL fallbacks and conflicting configuration. Supplying only `wasmBytes`
without `offline: true` is also supported, but the default worker still loads
`wasm_exec.js` by URL; that combination is not a complete offline bundle.

## Lifecycle and version compatibility

| Combination | Contract |
| --- | --- |
| Current client + current worker | Protocol 2; version is sent in `init` and `ready` and available as `client.protocolVersion`. |
| Current client + worker with no version | Legacy protocol 1; URL initialization and existing operations remain supported. Supplied assets are rejected if the ready reply lacks protocol 2. |
| Older client + current worker | Missing client version is accepted; the existing message names and terminal responses remain intact. |
| Unknown explicit protocol version | Initialization fails clearly and owned resources are released. |
| Older WASM + current worker | Existing export probing/readiness polling remains. Inputs and limits require `hasStructuredResults` and fail explicitly when unsupported. |
| Any WASM + mismatched `wasm_exec.js` | Unsupported: use the runtime copied by `make build-wasm` from the same Go compiler as the binary. |

Ship matching client and worker sources for offline use. Protocol negotiation
checks message compatibility; it cannot repair mismatched Go runtime imports
or prevent arbitrary host-provided worker code from making its own requests.
WASM feature capabilities and the worker protocol version are separate.

Initialization includes download/compilation/startup and defaults to a 30-second
`initTimeoutMs`. `requestTimeoutMs` applies only after an operation is dispatched;
queued time is excluded. It defaults to zero (disabled) for existing integrations.
Timeout/disposal/fatal failures reject the active operation, every queued
operation, and future requests on that client. Timeout terminates the worker so
a delayed untagged reply cannot be attributed to another request. Create another
client to restart. Guest errors delivered with a terminal response reject that
operation but leave the worker available for subsequent runs.

## Inputs, results, and diagnostics

Current WASM builds expose `hasStructuredResults` and `hasDiagnostics`.
`client.run(source, {inputs, limits})` and `runWorkspace(files, options)` forward
these options to the runtime. Inputs must be finite JSON data; unsupported
objects, undefined/function values, cycles, and nesting deeper than 64 levels
are rejected rather than silently changed by JSON serialization. Limits use the
runtime's documented accounting; a worker deadline is a separate wall-clock
control.

Successful and failed completion messages retain `stats`, including structured
`results`, `resultsCommitted`, and `diagnostic` when available. Inspect
`resultsCommitted` before treating emitted values as a successful completed
output. A rejected request has the full terminal message in `error.response`
and its structured diagnostic in `error.diagnostic`. Console messages remain
independent ordered events through `onMessage`.

Diagnostic source lines and columns are 1-based; columns count UTF-8 bytes.
JavaScript string offsets count UTF-16 code units and must be converted before
highlighting non-ASCII text. Older builds may provide only error text.

## Verification and boundaries

```sh
node --test web/tests/*.test.cjs
tsc --noEmit --strict --lib ES2022,DOM web/nanogo.d.mts
node scripts/smoke-offline.cjs build/offline/nanogo.wasm build/offline/wasm_exec.js
```

The smoke script uses the actual WASM artifact, matching Go runtime, SDK, and
worker in Node worker threads with browser-like messaging. It verifies supplied
bytes and compiled modules while `fetch` and `importScripts` throw, plus fresh
state, failure recovery, request timeout, queue rejection, restart, malformed
WASM, and cleanup. It additionally tests structured inputs/results when the
artifact advertises them. This does not substitute for a real browser check of
Blob-worker policies or file-origin behavior; use the generated HTML's check
button in each supported browser.

During this implementation, Node SDK/worker tests and actual WASM supplied-asset
checks passed. Real browser validation was initially blocked because the local
Mac was locked and no browser surface was available. Browser behavior should
only be reported as verified after the generated HTML check is run successfully.

For actual-WASM timing evidence, run:

```sh
node scripts/benchmark-wasm.cjs build/offline/nanogo.wasm build/offline/wasm_exec.js
```

The script validates each workload's output before reporting numerical loops,
nested slices, and standard JSON round trips. It reports module compilation,
fresh-worker startup, the first execution, and seven subsequent executions
separately. Every execution still parses source and creates fresh guest state;
these measurements do not benchmark the native prepared-program API. A fresh
worker is not a completely cold browser/process: the engine may reuse compiled
code. Node WASM measurements are distinct from browser timings and native Go
benchmarks; a single run does not establish a performance improvement.
