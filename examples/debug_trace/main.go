// debug_trace demonstrates q-style guest probes and a traceGL-inspired local
// execution timeline. Run it with: go run ./examples/debug_trace
package main

import (
	"context"
	"fmt"
	"log"

	"simonwaldherr.de/go/nanogo/interp"
)

func main() {
	vm := interp.NewInterpreter()
	interp.RegisterBuiltinPackages(vm)

	tracer := interp.NewTracer(64)
	vm.SetTracer(tracer)

	err := vm.RunContext(context.Background(), `package main
import "debug"

func square(n int) int {
	debug.Q(n)
	return n * n
}

func main() {
	result := square(7)
	debug.Q(result)
	debug.Mark("result is ready")
}`)
	if err != nil {
		log.Fatal(err)
	}

	for _, event := range tracer.Events() {
		fmt.Printf("%03d %-16s %-12s %s\n", event.Sequence, event.Kind, event.Function, event.Message)
	}
}
