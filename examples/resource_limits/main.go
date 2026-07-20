// resource_limits demonstrates deterministic resource limits. Run it with:
// go run ./examples/resource_limits
package main

import (
	"context"
	"errors"
	"fmt"
	"log"

	"simonwaldherr.de/go/nanogo/interp"
)

func main() {
	vm := interp.NewInterpreter()
	vm.Limits = interp.ExecutionLimits{
		MaxSteps:      5_000,
		MaxGoroutines: 4,
	}

	err := vm.RunContext(context.Background(), `package main
func main() {
	for { }
}`)
	if !errors.Is(err, interp.ErrStepLimit) {
		log.Fatalf("expected step limit, got %v", err)
	}
	fmt.Println("guest stopped at its configured step limit")
}
