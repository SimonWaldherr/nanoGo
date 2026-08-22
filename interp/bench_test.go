// interp/bench_test.go
package interp

import (
	"context"
	"strconv"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Real Go benchmarks (`go test -bench`) for nanoGo's own host implementation.
//
// These measure the Go code in evaluator.go / environment.go / vfs.go /
// package_scope.go itself (ns/op, B/op, allocs/op) -- NOT a guest program's
// step count. Contrast with interp/loader/bench_test.go's BenchmarkSum and
// BenchmarkSumTo, which are nanoGo GUEST source strings embedded inside Go
// tests (TestRunPackageBenchmarksUsesTestingBSubset,
// TestBenchmarkFunctionMatchesRealGoTestBench) and exercised through the
// RunFunctionBench/RunPackageBenchmarks harness -- they measure how many
// interpreter steps a guest benchmark takes, not how fast the host Go
// implementation runs.
//
// Run with:
//   go test ./interp/... -run '^$' -bench=. -benchmem
//
// so future changes to the hot, shared core (evaluator.go, environment.go,
// vfs.go, package_scope.go -- used by every execution path: Run/RunContext,
// the REPL, the MCP server, and every interp/loader/interp/index feature)
// can be checked for a throughput or allocation regression instead of
// discovering one after the fact.
// ---------------------------------------------------------------------------

// BenchmarkFibRecursive measures evalExpr/evalStmt dispatch throughput on a
// small, deeply recursive call -- the interpreter's hottest combined path
// (function call + call-frame setup + arithmetic + branching) under load.
func BenchmarkFibRecursive(b *testing.B) {
	const src = `
package main
func fib(n int) int {
	if n <= 1 { return n }
	return fib(n-1) + fib(n-2)
}
func main() { fib(20) }
`
	vm, _ := newTestVM()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := vm.Run(src); err != nil {
			b.Fatalf("Run: %v", err)
		}
	}
}

// BenchmarkEvalExprArithmetic measures evalExpr/evalStmt dispatch throughput
// on a tight for-loop doing integer arithmetic with no function calls, which
// isolates the statement/expression evaluation loop from call-frame setup
// cost (see BenchmarkFibRecursive for the call-heavy counterpart).
func BenchmarkEvalExprArithmetic(b *testing.B) {
	const src = `
package main
func main() {
	sum := 0
	for i := 0; i < 100000; i++ {
		sum = sum + i*2 - 1
	}
	_ = sum
}
`
	vm, _ := newTestVM()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := vm.Run(src); err != nil {
			b.Fatalf("Run: %v", err)
		}
	}
}

// BenchmarkChannelBufferedRoundTrip isolates the runtime overhead of the
// ChannelVal send/receive path used by guest channels. Keep this separate from
// evaluator benchmarks so channel synchronization changes can be compared
// without parser or AST-dispatch noise.
func BenchmarkChannelBufferedRoundTrip(b *testing.B) {
	ch := &ChannelVal{ElementType: "int", C: make(chan any, 1)}
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := ch.Send(ctx, i); err != nil {
			b.Fatalf("Send: %v", err)
		}
		if _, open, err := ch.Receive(ctx); err != nil || !open {
			b.Fatalf("Receive: value open=%v err=%v", open, err)
		}
	}
}

// BenchmarkVFSReadWrite measures VFS.WriteFile/ReadFile throughput, the hot
// path behind every os.* and fs.* guest file operation.
func BenchmarkVFSReadWrite(b *testing.B) {
	fs := NewVFS()
	data := []byte(strings.Repeat("x", 256))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := fs.WriteFile("/tmp/bench.txt", data, 0644); err != nil {
			b.Fatalf("WriteFile: %v", err)
		}
		if _, err := fs.ReadFile("/tmp/bench.txt"); err != nil {
			b.Fatalf("ReadFile: %v", err)
		}
	}
}

// BenchmarkReadDirInPopulatedVFS measures ReadDir on a small, 3-file
// directory inside a VFS that also holds many thousands of unrelated nodes
// elsewhere in the tree. ReadDir now costs O(children) via the VFS's
// parent->children index (see vfs.go's addChildLocked/removeChildLocked),
// instead of scanning every node in the whole VFS regardless of which
// directory is being listed — this benchmark's ns/op should stay small and
// flat, not grow with unrelated VFS size, which a full-map-scan
// implementation would regress on.
func BenchmarkReadDirInPopulatedVFS(b *testing.B) {
	fs := NewVFS()
	for i := 0; i < 20000; i++ {
		dir := "/big/dir" + strconv.Itoa(i)
		if err := fs.MkdirAll(dir, 0755); err != nil {
			b.Fatalf("MkdirAll: %v", err)
		}
		if err := fs.WriteFile(dir+"/file.txt", []byte("x"), 0644); err != nil {
			b.Fatalf("WriteFile: %v", err)
		}
	}
	if err := fs.MkdirAll("/small", 0755); err != nil {
		b.Fatalf("MkdirAll /small: %v", err)
	}
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := fs.WriteFile("/small/"+name, []byte("x"), 0644); err != nil {
			b.Fatalf("WriteFile: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := fs.ReadDir("/small"); err != nil {
			b.Fatalf("ReadDir: %v", err)
		}
	}
}

// BenchmarkPackageScopeLoad measures the CollectDecls+EvalDecls loading cost
// for a small multi-file package -- the two-phase decl-collection algorithm
// interp/loader's LoadModule relies on for every package it loads. Files are
// parsed once during setup; only the per-package decl loading is timed,
// isolating it from parser/IO cost. A fresh PackageScope is used each
// iteration (mirroring TestPackageScopeMultiFileForwardReferences in
// package_scope_test.go) so repeated iterations don't hit "already declared"
// collisions in the shared vm.
func BenchmarkPackageScopeLoad(b *testing.B) {
	vm := NewInterpreter()
	vm.Capabilities = FullCapabilities()

	mustWrite := func(p, src string) {
		if err := vm.VFS.MkdirAll("/benchpkg", 0755); err != nil {
			b.Fatalf("MkdirAll: %v", err)
		}
		if err := vm.VFS.WriteFile(p, []byte(src), 0644); err != nil {
			b.Fatalf("WriteFile %s: %v", p, err)
		}
	}
	mustWrite("/benchpkg/a.go", `package benchpkg

var X = Helper()

func UseType() int {
	return NewPoint(2, 3).Sum()
}
`)
	mustWrite("/benchpkg/b.go", `package benchpkg

func Helper() int {
	return 41
}
`)
	mustWrite("/benchpkg/c.go", `package benchpkg

type Point struct {
	X int
	Y int
}

func NewPoint(x, y int) Point {
	return Point{X: x, Y: y}
}

func (p Point) Sum() int {
	return p.X + p.Y
}
`)

	files, fset, err := ParsePackageDir(vm.VFS, "/benchpkg")
	if err != nil {
		b.Fatalf("ParsePackageDir: %v", err)
	}
	ctx := context.Background()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ps := vm.NewPackageScope("benchpkg")
		err := vm.WithExecution(ctx, fset, func() error {
			for _, f := range files {
				if err := ps.CollectDecls(f, fset); err != nil {
					return err
				}
			}
			for _, f := range files {
				if err := ps.EvalDecls(ctx, f); err != nil {
					return err
				}
			}
			return ps.RunInit(ctx)
		})
		if err != nil {
			b.Fatalf("load: %v", err)
		}
	}
}
