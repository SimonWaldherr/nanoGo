# Packed Game of Life

This sample implements Conway's B3/S23 rule on a toroidal board. Run it through
either interpreter or ordinary Go:

```sh
go run ./cmd/cli samples/game_of_life/main.go
go run ./samples/game_of_life
```

The deterministic 96×48 sample ends with 106 live cells after 100 generations.
The playground Life example uses the same kernel with randomized seeds,
changed-cell rendering and four simulation steps per displayed frame.

## Kernel contract

`lifeStep(src, dst, words, height, bits)` requires distinct integer buffers of
length `words*height`, positive dimensions, and `bits` of 16 or 32. Board width
is `words*bits`; 16-bit words must have zero upper bits. The kernel assumes
validated arguments and wraps both axes, including across word boundaries.

Cells are packed into words. Carry-save Boolean addition computes neighbor
counts for many cells at once; two flat buffers alternate between generations.
Small local scopes use nanoGo's unboxed integer slots. Explicit right-shift masks
preserve the sign-bit cell on both 32-bit WASM and native 64-bit hosts.

## Correctness and measurement

`TestPackedLife` compares every cell in every generation with an independent
scalar reference, covering empty/full/mixed boards, gliders/blinkers, one/multiple
word rows, several heights and both packing widths. `TestLifeWebKernelMatchesSample`
keeps the browser kernel aligned. Web examples also run under their configured
step budget; actual-WASM checks are separate from native tests.

```sh
go test ./interp -run '^$' -bench '^BenchmarkLifeVariants$' -benchtime=3x -benchmem
```

The recorded Apple M2 Max observations below use the same seeded 96×48 board
for 20 generations. Native ranges cover two runs of three iterations; WASM uses
the Go Node runner. Parsing and seeding are included, rendering/delays excluded.

| Implementation | Native time | Actual WASM time | Evaluator steps |
| --- | ---: | ---: | ---: |
| Nested grid | 652–803 ms | 3,129 ms | 36,360,502 |
| Flat reusable grid | 293–296 ms | 1,135 ms | 7,992,136 |
| Packed 16-bit, scoped | 83–88 ms | 266 ms | 2,071,147 |
| Packed 32-bit, scoped | 43–45 ms | 115 ms | 1,123,627 |
| Packed 32-bit, unscoped | 61–62 ms | 173 ms | 1,359,787 |

These historical observations compare these implementations, not every possible
Life algorithm or browser environment. Measure your own board sizes and rendering
costs. See [performance guidance](../../docs/performance.md) for separating
allocation, guest steps, startup and execution measurements.
