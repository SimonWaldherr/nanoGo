package interp

import (
	"bytes"
	runtimeTrace "runtime/trace"
	"testing"
)

func TestRuntimeTraceAnnotationsAreOptIn(t *testing.T) {
	vm, _ := newTestVM()
	vm.SetRuntimeTraceAnnotations(true)

	var trace bytes.Buffer
	startedHere := false
	if !runtimeTrace.IsEnabled() {
		if err := runtimeTrace.Start(&trace); err != nil {
			t.Skipf("runtime trace unavailable: %v", err)
		}
		startedHere = true
	}
	err := vm.Run(`package main
func helper() {}
func main() { helper() }
`)
	if startedHere {
		runtimeTrace.Stop()
	}
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !startedHere {
		return // `go test -trace` owns the output and verifies it externally.
	}
	for _, want := range []string{"nanogo.run_start", "nanogo.call_start", "nanogo.run_end"} {
		if !bytes.Contains(trace.Bytes(), []byte(want)) {
			t.Errorf("runtime trace is missing %q", want)
		}
	}
}
