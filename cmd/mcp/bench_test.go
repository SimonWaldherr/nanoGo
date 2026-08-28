// cmd/mcp/bench_test.go
package main

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

// The MCP server builds a complete sandbox per tool call — a fresh
// Interpreter, the host natives, and every builtin package — before it
// evaluates a single line of guest code. For an agent issuing many small
// run_code calls that per-call setup, not the guest program, is what the
// client waits on, so it needs its own benchmarks rather than being folded
// into interp's evaluator numbers.
//
// Run with:
//   go test ./cmd/mcp -run '^$' -bench=. -benchmem
//
// allocs/op is the figure to watch here: wall-clock on a loaded developer
// machine is far noisier than the allocation count, which is exact.

// BenchmarkNewMCPInterpreter isolates the per-tool-call sandbox construction
// that runCode, runModule and testModule all begin with.
func BenchmarkNewMCPInterpreter(b *testing.B) {
	vfs := interp.NewVFS()
	var buffer strings.Builder
	var bufferMu sync.Mutex
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		newMCPInterpreter(vfs, &buffer, &bufferMu)
	}
}

// BenchmarkRunCodeHello measures a complete run_code round trip for a
// minimal program. Almost all of its cost is setup, which is exactly the
// point: it is the floor an agent pays for any single-file execution.
func BenchmarkRunCodeHello(b *testing.B) {
	const src = "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hi\") }\n"
	vfs := interp.NewVFS()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := runCode(src, vfs, 5*time.Second); err != nil {
			b.Fatalf("runCode: %v", err)
		}
	}
}

// BenchmarkRunCodeLoop keeps the same setup but gives the evaluator real
// work, so a change that trades setup cost for evaluation cost (or the
// reverse) shows up as a different ratio between this and RunCodeHello.
func BenchmarkRunCodeLoop(b *testing.B) {
	const src = `package main

import "fmt"

func main() {
	sum := 0
	for i := 0; i < 20000; i++ { sum = sum + i }
	fmt.Println(sum)
}
`
	vfs := interp.NewVFS()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := runCode(src, vfs, 5*time.Second); err != nil {
			b.Fatalf("runCode: %v", err)
		}
	}
}

// BenchmarkHandleToolsList covers the tools/list response. Clients call it
// once per session, but it rebuilds every tool's JSON schema from scratch,
// so it is worth knowing what that costs.
func BenchmarkHandleToolsList(b *testing.B) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, rpcErr := handleToolsList(); rpcErr != nil {
			b.Fatalf("handleToolsList: %+v", rpcErr)
		}
	}
}

// BenchmarkToolCallFmtCode measures a tool that does no guest execution at
// all, separating JSON-RPC argument decoding and result marshalling from
// interpreter setup.
func BenchmarkToolCallFmtCode(b *testing.B) {
	params, err := json.Marshal(map[string]any{
		"name":      "fmt_code",
		"arguments": map[string]any{"code": "package main\nimport \"fmt\"\nfunc main() {\nfmt.Println(\"hi\")\n}\n"},
	})
	if err != nil {
		b.Fatalf("marshal: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, rpcErr := handleToolCall(params, defaultSession); rpcErr != nil {
			b.Fatalf("handleToolCall: %+v", rpcErr)
		}
	}
}
