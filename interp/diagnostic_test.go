package interp

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestStructuredDiagnostics(t *testing.T) {
	for _, tc := range []struct{ source, code string }{
		{`package main;func main( {`, "parse.syntax"},
		{"package main\nfunc main(){ missing() }", "runtime.error"},
		{`package main;func main(){panic("oops")}`, "runtime.panic"},
	} {
		vm := NewInterpreter()
		err := vm.Run(tc.source)
		d := DiagnosticFor(fmt.Errorf("wrapper: %w", err), "")
		if d == nil || d.Code != tc.code || d.Location == nil {
			t.Fatalf("%s: %+v %v", tc.code, d, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := NewInterpreter().RunContext(ctx, `package main;func main(){}`)
	if !errors.Is(err, context.Canceled) || DiagnosticFor(err, "").Code != "execution.canceled" {
		t.Fatalf("%v", err)
	}
	vm := NewInterpreter()
	vm.Limits.MaxSteps = 3
	err = vm.Run(`package main;func main(){for{}}`)
	d := DiagnosticFor(err, "")
	if !errors.Is(err, ErrStepLimit) || d.Limit == nil || d.Limit.Maximum != 3 {
		t.Fatalf("%v %+v", err, d)
	}
}
func TestAdditionalBudgetsAndRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		limits       ExecutionLimits
		want         error
	}{
		{"calls", `package main;func f(){f()};func main(){f()}`, ExecutionLimits{MaxCallDepth: 8}, ErrCallDepthLimit},
		{"output", `package main;func main(){ConsoleLog("abc");ConsoleLog("def")}`, ExecutionLimits{MaxOutputBytes: 5}, ErrOutputLimit},
		{"results", `package main;func main(){for i:=0;i<20;i++{host.Emit("x",i)}}`, ExecutionLimits{MaxResultBytes: 40}, ErrResultLimit},
		{"allocation", `package main;func main(){for i:=0;i<5;i++{x:=make([]int,10);_ = x}}`, ExecutionLimits{MaxAllocationUnits: 20}, ErrAllocationLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm := NewInterpreter()
			vm.EnableResults()
			vm.Limits = tc.limits
			calls := 0
			vm.RegisterNative("ConsoleLog", func([]any) (any, error) { calls++; return nil, nil })
			err := vm.Run(tc.source)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v want %v", err, tc.want)
			}
			if tc.name == "output" && calls != 1 {
				t.Fatalf("forwarded %d", calls)
			}
			if vm.LastResults().Committed {
				t.Fatal("committed failed results")
			}
			if err = vm.Run(`package main;func main(){}`); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestHostFunctionCancellation(t *testing.T) {
	vm := NewInterpreter()
	entered := make(chan struct{})
	_ = vm.RegisterHostFunction("block", HostFunctionSpec{}, func(ctx context.Context, _ []any) ([]any, error) { close(entered); <-ctx.Done(); return nil, ctx.Err() })
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- vm.RunContext(ctx, `package main;func main(){block()}`) }()
	<-entered
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestPanicDiagnosticRetainsOriginAndStack(t *testing.T) {
	vm := NewInterpreter()
	err := vm.Run("package main\nfunc inner(){panic(\"bad\")}\nfunc outer(){defer func(){}();inner()}\nfunc main(){outer()}")
	d := DiagnosticFor(err, "")
	if d == nil || d.Location == nil || d.Location.Line != 2 || len(d.Stack) < 3 {
		t.Fatalf("%+v %v", d, err)
	}
	vm = NewInterpreter()
	err = vm.Run("package main\nfunc inner(){panic(\"bad\")}\nfunc main(){defer func(){panic(\"replacement\")}();inner()}")
	d = DiagnosticFor(err, "")
	if d.Location == nil || d.Location.Line != 3 {
		t.Fatalf("replacement: %+v", d)
	}
}

func TestZeroValueAllocationPreflight(t *testing.T) {
	for _, source := range []string{`package main;func main(){var x [100]int;_ = x}`, `package main;type Bad struct{Next Bad};func main(){var x Bad;_ = x}`} {
		vm := NewInterpreter()
		vm.Limits.MaxAllocationUnits = 50
		if err := vm.Run(source); !errors.Is(err, ErrAllocationLimit) {
			t.Fatalf("%v", err)
		}
	}
}
