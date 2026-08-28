// cmd/cli/bench_test.go
package main

import (
	"os"
	"testing"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

// The CLI builds one sandbox per process, so its setup cost lands directly
// in the latency of every `nanogo-cli file.go` invocation. Process creation
// on a developer machine can dwarf it in wall-clock terms, which is exactly
// why these measure in-process: allocs/op here is exact and attributable,
// while an end-to-end shell timing on Windows is mostly the OS's exec path.
//
// Run with:
//   go test ./cmd/cli -run '^$' -bench=. -benchmem

// BenchmarkRunSafeHello is the floor for `nanogo-cli <file.go>`: sandbox
// construction plus a program that does nothing but print once.
//
// The CLI's console natives write to the real os.Stdout, so the benchmark
// swaps in a null sink for its duration — otherwise every iteration emits a
// line into the benchmark's own output, and the measurement includes the
// terminal's write cost rather than the interpreter's.
func BenchmarkRunSafeHello(b *testing.B) {
	const src = "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hi\") }\n"
	restore := silenceStdout(b)
	defer restore()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := RunSafe(src, 5*time.Second); err != nil {
			b.Fatalf("RunSafe: %v", err)
		}
	}
}

// silenceStdout points os.Stdout at the null device and returns a function
// restoring the previous value.
func silenceStdout(b *testing.B) func() {
	b.Helper()
	devNull, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		b.Fatalf("open %s: %v", os.DevNull, err)
	}
	previous := os.Stdout
	os.Stdout = devNull
	return func() {
		os.Stdout = previous
		devNull.Close()
	}
}

// BenchmarkRegisterSafeNatives isolates the CLI's own host-native
// registration, which builds the console, sprintf, file and HTTP bridges.
func BenchmarkRegisterSafeNatives(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := interp.NewInterpreter()
		registerSafeNatives(vm)
	}
}

// BenchmarkCLISandboxSetup is RunSafeHello without the guest program: what
// the CLI pays before any user code runs.
func BenchmarkCLISandboxSetup(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := interp.NewInterpreter()
		registerSafeNatives(vm)
		interp.RegisterBuiltinPackages(vm)
	}
}
