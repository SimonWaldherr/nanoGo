// cmd/repl/bench_test.go
package main

import (
	"testing"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

// Unlike the CLI and the MCP server, the REPL builds its sandbox once and
// then reuses it for every line, so its per-line cost is what matters: the
// input classification (looksLikeDecl/tryConvertShortVarDecl), the source
// wrapping, and re-running a one-statement program on an interpreter whose
// globals keep growing.
//
// Run with:
//   go test ./cmd/repl -run '^$' -bench=. -benchmem

func newBenchREPL(b *testing.B) *interp.Interpreter {
	b.Helper()
	vm := interp.NewInterpreter()
	registerSafeNatives(vm)
	interp.RegisterBuiltinPackages(vm)
	return vm
}

// BenchmarkREPLStatementLine measures one ordinary statement line end to
// end: wrap, parse, execute.
func BenchmarkREPLStatementLine(b *testing.B) {
	vm := newBenchREPL(b)
	if err := runREPLSource(vm, time.Second, buildDeclSource("var counter = 0")); err != nil {
		b.Fatalf("seed: %v", err)
	}
	src := buildStmtSource("counter = counter + 1")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := runREPLSource(vm, time.Second, src); err != nil {
			b.Fatalf("run: %v", err)
		}
	}
}

// BenchmarkREPLDeclarationLine covers the other main shape, a top-level
// declaration, which re-declares into the persistent globals scope.
func BenchmarkREPLDeclarationLine(b *testing.B) {
	vm := newBenchREPL(b)
	src := buildDeclSource("func twice(n int) int { return n * 2 }")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := runREPLSource(vm, time.Second, src); err != nil {
			b.Fatalf("run: %v", err)
		}
	}
}

// BenchmarkREPLLineClassification isolates the pure string handling every
// line goes through before anything is parsed.
func BenchmarkREPLLineClassification(b *testing.B) {
	lines := []string{
		"fmt.Println(x)",
		"x := 5",
		"func greet() {}",
		"var total = 1 + 2",
		"import \"strings\"",
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, line := range lines {
			if looksLikeDecl(line) {
				_ = buildDeclSource(line)
				continue
			}
			if converted, ok := tryConvertShortVarDecl(line); ok {
				_ = buildDeclSource(converted)
				continue
			}
			_ = buildStmtSource(line)
		}
	}
}

// BenchmarkREPLStartup measures what a user waits for before the first
// prompt appears.
func BenchmarkREPLStartup(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm := interp.NewInterpreter()
		registerSafeNatives(vm)
		interp.RegisterBuiltinPackages(vm)
	}
}
