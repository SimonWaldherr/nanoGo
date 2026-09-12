# Bit-parallel nanoGo Life

Run the simulation entirely inside nanoGo:

```sh
go run ./cmd/cli samples/game_of_life/main.go
```

The deterministic 96×48 board reaches **106 live cells after 100 generations**.
The same file also runs as ordinary Go (`go run ./samples/game_of_life`).
The playground's **Life** example uses the same kernel with random seeds,
changed-cell rendering and four simulation generations per displayed frame.

## Algorithm

- Pack 32 cells in each `int`; evaluate their neighbor counts simultaneously
  using carry-save Boolean addition and Conway's B3/S23 rule.
- Reuse two flat buffers and swap them after each generation.
- Wrap both axes: the board is a torus, including across word boundaries.
- Keep scratch values in scopes of at most four integer locals. This matches
  nanoGo's unboxed local storage and avoids spilling large bit patterns into
  boxed map entries on every iteration.
- Mask right shifts explicitly, preserving the sign bit as a cell on 32-bit
  WebAssembly as well as native 64-bit hosts.

`lifeStep(src, dst, words, height, bits)` requires distinct `src` and `dst`
buffers, each containing `words*height` integers, positive dimensions, and
`bits` equal to 16 or 32. The board width is `words*bits`; the recommended
32-bit variant therefore requires a width divisible by 32. Upper bits in a
16-bit input word must be zero. This low-level kernel assumes valid inputs.

## Measurements

Run the same seeded 96×48 board for 20 generations, without drawing:

```sh
go test ./interp -run '^$' -bench '^BenchmarkLifeVariants$' -benchtime=3x -benchmem
```

Apple M2 Max, native nanoGo, two runs of three iterations each:

| Variant | Time per 20 generations | Evaluator steps | Allocated bytes |
| --- | ---: | ---: | ---: |
| Original nested grid | 652–803 ms | 36,360,502 | ~1,964,000 |
| Flat grid, reused buffers | 293–296 ms | 7,992,136 | ~995,000 |
| Packed 16-bit, scoped locals | 83–88 ms | 2,071,147 | ~711,000 |
| **Packed 32-bit, scoped locals** | **43–45 ms** | **1,123,627** | **~416,000** |
| Packed 32-bit, unscoped locals | 61–62 ms | 1,359,787 | ~6,974,000 |

On the same host using Go's Node.js WebAssembly runner (three iterations):

| Variant | Time per 20 generations |
| --- | ---: |
| Original nested grid | 3,129 ms |
| Flat grid | 1,135 ms |
| Packed 16-bit | 266 ms |
| **Packed 32-bit, scoped locals** | **115 ms** |
| Packed 32-bit, unscoped locals | 173 ms |

The selected kernel is approximately **27× faster than the original** in this
WebAssembly benchmark. Rendering and frame delays are excluded.

These measurements include parsing, seed construction and all 20 generations.
Allocated bytes describe interpreter activity, not just board storage. Timings
vary with the machine and workload. This is the fastest of the compared
nanoGo implementations, not a claim about every possible board or algorithm.

## Correctness

`TestPackedLife` compares every cell after every generation with an independent
scalar reference: empty/full boards, deterministic mixed patterns, gliders and
blinkers; one- and multi-word rows; heights of 1, 2, 7, 9 and 17; both 16- and
32-bit packing. The tests also run under Go's Node-based WebAssembly runner.
`TestLifeWebKernelMatchesSample` keeps the playground kernel aligned with this
sample. The existing web example test enforces the playground's step budget.
