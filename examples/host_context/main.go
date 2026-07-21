// host_context demonstrates safe request-context metadata passed from a Go
// host to nanoGo. Run it with: go run ./examples/host_context
package main

import (
	"context"
	"log"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

type contextKey string

const (
	requestIDKey contextKey = "request-id"
	featuresKey  contextKey = "features"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, requestIDKey, "req-7f8c")
	ctx = context.WithValue(ctx, featuresKey, map[string]any{"betaUI": true})

	vm := interp.NewInterpreter()
	interp.RegisterBuiltinPackages(vm)
	// Only these explicit fields cross the boundary. The guest receives a
	// copied snapshot, never the Go context or its unlisted values.
	if err := vm.BindHostContext("hostContext", ctx,
		interp.ContextField{Name: "requestID", Key: requestIDKey},
		interp.ContextField{Name: "features", Key: featuresKey},
	); err != nil {
		log.Fatal(err)
	}

	err := vm.RunContext(ctx, `package main
import "fmt"

func main() {
	values := hostContext["values"]
	fmt.Println("request:", values["requestID"])
	fmt.Println("beta UI:", values["features"]["betaUI"])
	fmt.Println("deadline (unix ms):", hostContext["deadlineUnixMilli"])
}`)
	if err != nil {
		log.Fatal(err)
	}
}
