# Embedding update: validation record

Local validation on 2026-09-16, Apple M2 Max, darwin/arm64, Go 1.27.1,
Node 26.8.2 and existing Binaryen 132. No dependencies were installed or upgraded.
The measured full-feature WASM was built from the final working tree with
`make build-wasm-optimized WASM_OUT=build/embedding/nanogo.wasm`.

## Verified

- Full native interpreter, loader, index, CLI, MCP, REPL and standard-Go
  comparison suites passed with `go test -race`. The final JSON/bridge/strict
  result/budget/diagnostic changes also passed focused race tests.
- The compatibility suite invokes the installed Go compiler as its reference
  and exercises direct execution and module loading. JSON tests include malformed
  data, ordinary guest errors, pointer/map/slice alias behavior, null, tags,
  byte encoding, and explicit legacy APIs. Selected invalid numeric programs are
  rejected by both Go and nanoGo.
- Prepared runs passed initialization and entry failure recovery, cancellation,
  input/global/imported-package/VFS isolation, original source/config changes,
  shared initialization+entry budget, and concurrent run tests under `-race`.
- Native and WASM `go vet`, formatting, 56 Node web regression tests and strict
  TypeScript declaration checking passed. Go js/wasm package tests ran in Node;
  the JSON regression suite also ran as actual WASM.
- `scripts/smoke-wasm.cjs` executed the shipping optimized artifact, including
  JSON, guest error recovery, tracing/profiling, workspaces and guest tests.
- `scripts/smoke-offline.cjs` executed the real SDK, worker and optimized Go
  artifact with fetch/importScripts forbidden in the supplied-assets path. Bytes
  and compiled modules worked. It verified copied inputs, committed/partial
  results, diagnostics, exhausted budgets, fresh workspace state, startup failure,
  deadline/queue rejection, restart, disposal and factory cleanup.
- The native prepared and JSON examples ran, and the legacy JSON example passed
  the nanoGo CLI. The single-file HTML generator and its script-syntax regression
  passed, including an embedded `</script>` source fixture.

## Not verified

A real browser could not be controlled: computer-use inventory reported no
available browser/app because the Mac was locked; automatic unlock failed.
The user was asked to unlock it, and one later inventory retry remained blocked.
Node worker/WASM results are **not** evidence that every browser's Blob worker,
CSP and local-file policy behaves identically. The generated
`build/offline.html` includes a Verify button for bytes and compiled-module modes.
Open it in a supported browser, disable network in developer tools, run Verify,
and inspect the network log to complete that check. No browser engine versions
or historical nanoGo release matrix are claimed verified here.

No hard Go/WASM heap cap is provided. Accounting boundaries and callback
responsibilities are documented in [embedding.md](embedding.md). JSON and Go
semantics remain explicitly scoped subsets, not complete standard-library or
compiler equivalence.

## Native observations

Medians of three 200 ms benchmark samples, sequential with no other test/build
run in progress. Complete sample values are in
[embedding-native.json](benchmarks/embedding-native.json).

| Workload | Cold prepare + fresh run | Reused preparation + fresh run | Allocations cold / warm |
| --- | ---: | ---: | ---: |
| Numerical loop | 62.081 µs | 53.478 µs | 280 / 90 |
| Nested slices | 19.529 µs | 9.515 µs | 347 / 125 |
| JSON serialization | 21.372 µs | 9.872 µs | 428 / 169 |

A warm `ModuleCache.Load` measured 258.8 ns/op, 496 bytes and 6 allocations.
The prepared benchmark includes fresh VM and VFS state each iteration. This is
an observation of preparation reuse on the new implementation, not a speedup
claim against the previous release. Source and exact command are retained in the
benchmark file and sample metadata.

## Actual WASM observations (Node)

Complete samples: [embedding-wasm.json](benchmarks/embedding-wasm.json), generated
by `node scripts/benchmark-wasm.cjs build/embedding/nanogo.wasm`.
Module compilation took 12.220 ms. Every workload used a fresh worker followed
by one first execution and seven repeated requests; requests still parse and
execute with a fresh interpreter. A fresh worker is not a cold browser/process.

| Workload | Fresh worker startup | First execution | Median repeated execution |
| --- | ---: | ---: | ---: |
| Numerical loop | 49.666 ms | 43.520 ms | 10.550 ms |
| Nested slices | 20.376 ms | 19.847 ms | 10.930 ms |
| JSON round trip | 21.054 ms | 11.793 ms | 4.053 ms |

Native and WASM benchmark bodies differ; do not compare these tables as an
architecture speed ratio. They do not establish a browser performance claim.
The binary was 14,348,155 bytes (SHA-256
`1be09ff8a55df867972c18ea4909cf8952d9731b773168fa6bab73105270911e`).
The generated self-contained HTML was 19,197,119 bytes due largely to base64
embedding. No binary-size reduction is claimed for this feature update. Build
metadata and subsequent toolchains can change the artifact bytes.
