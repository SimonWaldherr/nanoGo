# JSON interoperability

The guest packages `encoding/json` and its short alias `json` use these Go
contracts:

```go
data, err := json.Marshal(value) // []byte, error
err = json.Unmarshal(data, &target) // error
```

Use `string(data)` to display encoded JSON. `Unmarshal` requires a byte slice
and a non-nil guest pointer, including pointers to variables, struct fields,
and slice elements. The number of assignment targets never selects an API.

Malformed JSON, type mismatches, unsupported values, overflow, and conversion
envelope violations return ordinary guest errors. They do not stop execution
unless the program chooses to panic or return. Error values support comparison
with `nil`, printing, and `Error()`. `Marshal` returns a nil byte slice on
failure. Wrong function arity is an interpreter error. Cancellation and the
host's execution budgets remain execution failures, rather than catchable JSON
errors.

## Supported subset

- Booleans, strings, finite floating-point values, and supported integer
  representations, including named scalar types and aliases.
- Nested slices, fixed arrays, and maps with string or integer keys. Maps use
  Go's deterministic JSON key ordering; callers should not rely on guest map
  iteration order.
- Declared, non-recursive guest structs with exported fields. Field order,
  renamed fields, `omitempty`, `string`, `-`, unknown fields, and
  case-insensitive field matching follow `encoding/json`. Unexported fields
  are ignored. Other struct tags remain metadata.
- Guest pointers to supported values. Existing destination pointers and maps
  are updated, and ordinary map-entry replacement preserves Go's pointer
  identity behavior. Nil pointers, maps, and slices encode as `null`;
  non-nil empty slices encode as `[]`.
- Byte slices encode as base64 strings and decode from base64 strings or JSON
  integer arrays. Fixed byte arrays encode as integer arrays, as in Go.
- Empty interfaces decode to guest maps, slices, booleans, strings,
  `float64` numbers, and nil. This permits subsequent indexing, type
  assertions, range, and re-encoding.

The facade uses the host Go JSON decoder for syntax, numeric validation, and
field/tag rules, then copies values into guest storage. Invalid JSON syntax
does not change the target. Like Go, a type error can leave partially updated
fields; inspect the returned error before consuming a decoded value. Guest
integer storage cannot represent unsigned integers above the largest positive
guest `int`; these values are rejected explicitly.

Recursive type definitions, embedded anonymous fields, custom `MarshalJSON`
or `UnmarshalJSON` methods, custom text-marshaling map keys, channels,
functions, cyclic values, and opaque native objects are rejected. This is not
the complete Go `encoding/json` package: streaming `Encoder`/`Decoder`,
`RawMessage`, `Number`, and option-setting APIs are not exposed.

## Bounds and ownership

Each JSON call has a fixed conversion envelope: 128 nesting levels, at most
1,048,576 visited values, and 16 MiB of input/output JSON or traversed string
data. Individual fixed-size native type representations are also bounded
before creating conversion storage. Interfaces and pointers add conversion levels; a composite can consume
more than one visited value. Checks happen before allocating guest containers
where their size is known. Excessive nesting and cycles return errors instead
of recursing indefinitely.

These bounds are separate from `ExecutionLimits`. Successful marshaling
charges one guest allocation unit per returned byte. Decoding charges the
requested element counts of reconstructed slices, arrays, maps and structs,
plus newly created pointer cells, including reused container storage. This is
cumulative accounting of requested guest storage, not live heap measurement.
Temporary native JSON buffers and reflection metadata are not guest memory
units. The Go JSON codec itself is a bounded native operation; conversion
walks poll cancellation without adding interpreter steps. Worker/process
isolation remains necessary for a hard memory or wall-clock boundary.

Encoding returns a new byte buffer independent of its input. Decoding copies
from its byte buffer; subsequent input mutation does not affect the target.
Do not concurrently mutate a JSON input or destination from another guest or
host goroutine. A host callback receiving internal guest values must obey the
same rule.

## Migration from the old convenience facade

Earlier nanoGo versions returned a string from `Marshal` and a decoded value
from the single-argument `Unmarshal`. To retain those call signatures, change
the import explicitly:

```go
import json "nanogo/jsonlegacy"

text := json.Marshal(map[string]int{"answer": 42})
value := json.Unmarshal(text)
```

The legacy facade keeps single-return behavior and reports JSON failures as
interpreter failures, as before. Its decoded values retain the historical
native representation; use standard `Unmarshal` for typed, guest-indexable
containers. The legacy path applies the same bounded supported-value policy:
it is a signature migration aid, not a promise to reproduce earlier silent
conversions of unsupported values.

For new and migrated programs:

```go
import "encoding/json"

data, err := json.Marshal(map[string]int{"answer": 42})
if err != nil { panic(err) }
var value map[string]int
if err := json.Unmarshal(data, &value); err != nil { panic(err) }
fmt.Println(value["answer"])
```

Runnable sources are in [`examples/json`](../examples/json). From the repository:

```sh
go run ./examples/json
go run ./cmd/cli examples/json/main.go
go run ./cmd/cli examples/json/legacy.ng
```

`compat/TestJSONStandardGo` compares the same program against standard Go in
both direct execution and the package loader. `interp/TestJSON*` additionally
checks unsupported inputs, bounded conversion, ordinary errors, legacy
signatures, and execution-budget failure/recovery. These tests also run under
the real Go JavaScript/WASM test runner; they are not a substitute for testing
browser worker lifecycle.

`BenchmarkJSONRoundTrip` measures a warm encode/decode of a map containing two
numeric slices. On this Apple M2 Max with Go 1.27.1, three 200 ms samples gave
median 5,420 ns/op natively and 36,578 ns/op in actual WASM under Node 26.8.2;
both reported 105 allocations/op. These are workload snapshots, not a speedup
claim or browser startup measurement. Reproduce them independently:

```sh
go test ./interp -run '^$' -bench '^BenchmarkJSONRoundTrip$' -benchmem -benchtime=200ms -count=3
GOOS=js GOARCH=wasm go test -exec="$(go env GOROOT)/lib/wasm/go_js_wasm_exec" ./interp -run '^$' -bench '^BenchmarkJSONRoundTrip$' -benchmem -benchtime=200ms -count=3
```
