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

// BenchmarkControlFlowHotPaths keeps range binding, backward goto lookup,
// recover dispatch, and guest buffered channels measurable independently.
func BenchmarkControlFlowHotPaths(b *testing.B) {
	workloads := []struct {
		name string
		src  string
	}{
		{"RangeSlice", `package main
func main() { s := []int{}; for i := 0; i < 20000; i++ { s = append(s, i) }; total := 0; for _, v := range s { total = total + v }; _ = total }`},
		{"Goto", `package main
func main() { i := 0; loop: i++; if i < 20000 { goto loop } }`},
		{"Make", `package main
func main() { for i := 0; i < 5000; i++ { s := make([]int, 8, 16); m := make(map[string]int, 8); ch := make(chan int, 1); m["x"] = s[0]; ch <- m["x"]; _ = <-ch } }`},
		{"Recover", `package main
func one() { defer func() { recover() }(); panic("x") }
func main() { for i := 0; i < 1000; i++ { one() } }`},
		{"BufferedChannel", `package main
func main() { ch := make(chan int, 1); for i := 0; i < 20000; i++ { ch <- i; _ = <-ch } }`},
	}
	for _, workload := range workloads {
		b.Run(workload.name, func(b *testing.B) {
			vm, _ := newTestVM()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := vm.Run(workload.src); err != nil {
					b.Fatalf("Run: %v", err)
				}
			}
		})
	}
}

// guestWorkloads are whole guest programs, each dominated by one distinct
// part of the evaluator, so a change can be attributed instead of just
// observed. BenchmarkFibRecursive and BenchmarkEvalExprArithmetic above cover
// call dispatch and the bare statement loop; these cover the container,
// string, struct, map and nested-loop paths that real guest code spends its
// time in and that those two miss entirely.
//
// Sizes are chosen so each program runs long enough to dominate parse and
// setup cost while still leaving `go test -bench` responsive. They are guest
// source, so they exercise the same Run path a host or the playground uses —
// unlike interp/loader's BenchmarkSum, which measures a guest program's step
// count rather than the host implementation's throughput.
var guestWorkloads = []struct {
	name string
	src  string
}{
	{
		// Slice growth and indexed reads: builtinAppend, SliceVal indexing,
		// and the `i < len(s)` loop header.
		name: "SliceAppendIndex",
		src: `
package main
func main() {
	s := []int{}
	for i := 0; i < 40000; i++ { s = append(s, i*3) }
	t := 0
	for i := 0; i < len(s); i++ { t = t + s[i] }
	_ = t
}`,
	},
	{
		// Struct literal construction plus a value-receiver method call per
		// iteration: composite literals, field access, and method dispatch.
		name: "StructMethodCalls",
		src: `
package main
type P struct { X int; Y int }
func (p P) Sum() int { return p.X + p.Y }
func main() {
	t := 0
	for i := 0; i < 30000; i++ {
		p := P{X: i, Y: i * 2}
		t = t + p.Sum()
	}
	_ = t
}`,
	},
	{
		// Read-modify-write on one map entry: MapVal.hash plus the map
		// index lvalue path.
		name: "MapReadModifyWrite",
		src: `
package main
func main() {
	m := map[string]int{}
	for i := 0; i < 20000; i++ { m["k"] = m["k"] + i }
	_ = m["k"]
}`,
	},
	{
		// String building and stdlib string calls, which route through the
		// curated strings package rather than the evaluator's own operators.
		name: "StringBuildAndSearch",
		src: `
package main
import "strings"
func main() {
	acc := ""
	for i := 0; i < 4000; i++ { acc = acc + "x" }
	_ = strings.Contains(acc, "xxx")
	n := 0
	for i := 0; i < 4000; i++ { if strings.HasPrefix(acc, "x") { n++ } }
	_ = n
}`,
	},
	{
		// A nested loop over an indexed container with a comparison and a
		// swap per inner step — the shape most sensitive to how cheaply
		// a[i] participates in integer expressions.
		name: "NestedLoopSort",
		src: `
package main
func main() {
	n := 220
	a := []int{}
	for i := 0; i < n; i++ { a = append(a, (n-i)*7919%n) }
	for i := 0; i < n; i++ {
		for j := 0; j < n-1-i; j++ {
			if a[j] > a[j+1] { t := a[j]; a[j] = a[j+1]; a[j+1] = t }
		}
	}
	_ = a[0]
}`,
	},
}

// BenchmarkGuestWorkloads runs each program in guestWorkloads as its own
// sub-benchmark, so `go test ./interp -run '^$' -bench=GuestWorkloads -benchmem`
// reports one line per evaluator area and
// `-bench=GuestWorkloads/StructMethodCalls` isolates a single one.
//
// A fresh interpreter per iteration keeps repeated runs from accumulating
// declarations in one shared globals scope, which would otherwise make later
// iterations measure a different (larger) environment than the first.
func BenchmarkGuestWorkloads(b *testing.B) {
	for _, w := range guestWorkloads {
		b.Run(w.name, func(b *testing.B) {
			vm, _ := newTestVM()
			if err := vm.Run(w.src); err != nil {
				b.Fatalf("Run: %v", err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				vm, _ := newTestVM()
				if err := vm.Run(w.src); err != nil {
					b.Fatalf("Run: %v", err)
				}
			}
		})
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

// BenchmarkDebugQNoObserver keeps the common production case measurable:
// debug.Q must evaluate its arguments, but without a tracer it must not pay
// for building diagnostic strings or formatting expression ASTs.
func BenchmarkDebugQNoObserver(b *testing.B) {
	const src = `
package main
import "debug"
func main() {
	for i := 0; i < 2000; i++ {
		debug.Q(i, i*2)
	}
}`
	vm, _ := newTestVM()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := vm.Run(src); err != nil {
			b.Fatalf("Run: %v", err)
		}
	}
}

// BenchmarkMathAndHTTPFacade covers the lightweight standard-package layer:
// numeric native dispatch plus the capability-checked HTTP host hand-off.
// The HTTP native is deliberately local, so this measures interpreter work,
// not network latency.
func BenchmarkMathAndHTTPFacade(b *testing.B) {
	const src = `
package main
import (
	"http"
	"math"
)
func main() {
	for i := 0; i < 1000; i++ {
		_ = math.Sqrt(i*i + 1)
		_ = http.GetText("https://example.com/value")
	}
}`
	vm, _ := newTestVM()
	vm.RegisterInternalNative("HTTPGetText", func([]any) (any, error) { return "ok", nil })
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := vm.Run(src); err != nil {
			b.Fatalf("Run: %v", err)
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

// BenchmarkTemplateRenderRepeated renders one template many times with
// different data — the shape any report, table, or page generator has.
// text/template.RenderString is nanoGo's whole template surface, and it
// parses its first argument on every call, so this is where a parse cache
// shows up.
func BenchmarkTemplateRenderRepeated(b *testing.B) {
	const src = `
package main
import "text/template"
func main() {
	out := ""
	for i := 0; i < 200; i++ {
		s, _ := template.RenderString("{{.Name}} has {{.Count}} items\n",
			map[string]interface{}{"Name": "row", "Count": i})
		out = out + s
	}
	_ = out
}
`
	vm, _ := newTestVM()
	if err := vm.Run(src); err != nil {
		b.Fatalf("Run: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm, _ := newTestVM()
		if err := vm.Run(src); err != nil {
			b.Fatalf("Run: %v", err)
		}
	}
}

// BenchmarkTemplateRenderDistinct renders a different template text on every
// iteration. It is the counterpart to BenchmarkTemplateRenderRepeated: a
// cache must not make the never-repeated case worse, and it must not grow
// without bound when guest code builds template text dynamically.
func BenchmarkTemplateRenderDistinct(b *testing.B) {
	const src = `
package main
import (
	"strconv"
	"text/template"
)
func main() {
	out := ""
	for i := 0; i < 200; i++ {
		s, _ := template.RenderString("row "+strconv.Itoa(i)+": {{.Name}}\n",
			map[string]interface{}{"Name": "x"})
		out = out + s
	}
	_ = out
}
`
	vm, _ := newTestVM()
	if err := vm.Run(src); err != nil {
		b.Fatalf("Run: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		vm, _ := newTestVM()
		if err := vm.Run(src); err != nil {
			b.Fatalf("Run: %v", err)
		}
	}
}
