package interp

import (
	"context"
	"testing"
	"time"
)

// FuzzInterpreterNeverPanics protects the boundary that receives arbitrary
// guest source. An interpreter must turn malformed or unsupported programs
// into errors; it must never crash the embedding host. Tight limits and a
// deadline also make accidental infinite loops and blocking package calls
// harmless while fuzzing.
func FuzzInterpreterNeverPanics(f *testing.F) {
	for _, seed := range []string{
		"package main\nfunc main() {}\n",
		"package main\nfunc main() { sum := 0; for i := 0; i < 10; i++ { sum += i }; _ = sum }\n",
		"package main\nfunc main() { ch := make(chan int); go func() { ch <- 1 }(); <-ch }\n",
		"package main\nfunc main( {\n",
		"package main\nimport \"fmt\"\nfunc main() { fmt.Printf(\"%d\", 1) }\n",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, source string) {
		// Keep each generated input cheaply reproducible and prevent a fuzzer
		// from spending its budget on huge syntax trees alone.
		if len(source) > 16<<10 {
			t.Skip()
		}
		vm := NewInterpreter()
		vm.Limits = ExecutionLimits{MaxSteps: 2_048, MaxGoroutines: 8}
		vm.MaxContainerSize = 512
		RegisterBuiltinPackages(vm)
		vm.RegisterNative("ConsoleLog", func([]any) (any, error) { return nil, nil })
		vm.RegisterNative("ConsoleWarn", func([]any) (any, error) { return nil, nil })
		vm.RegisterNative("ConsoleError", func([]any) (any, error) { return nil, nil })

		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("interpreter panicked for %q: %v", source, recovered)
			}
		}()
		_ = vm.RunContext(ctx, source)
	})
}

// FuzzSourceToolsNeverPanic covers the parser-backed tools exposed through
// the REPL, MCP server, and browser inspector. Their outputs may be errors,
// but arbitrary editor input must not crash the host process.
func FuzzSourceToolsNeverPanic(f *testing.F) {
	for _, seed := range []string{
		"package main\nfunc main() {}\n",
		"package main\nfunc main() { return; println(\"unreachable\") }\n",
		"package main\nfunc main(\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		if len(source) > 16<<10 {
			t.Skip()
		}
		defer func() {
			if recovered := recover(); recovered != nil {
				t.Fatalf("source tool panicked for %q: %v", source, recovered)
			}
		}()
		_, _ = FormatSource(source)
		_, _ = VetSource(source)
		_, _ = InspectSource(source)
	})
}
