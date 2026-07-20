// host_bridge demonstrates bidirectional messages between a Go host and a
// nanoGo program. Run it with: go run ./examples/host_bridge
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	vm := interp.NewInterpreter()
	interp.RegisterBuiltinPackages(vm)

	bridge := interp.NewHostChannel(1)
	if err := vm.BindHostChannel("hostIn", "hostOut", bridge); err != nil {
		log.Fatal(err)
	}

	// The bridge deep-copies supported primitive, map, and slice values at the
	// host/guest boundary, so guest code cannot retain host-owned references.
	if err := bridge.Send(ctx, map[string]any{
		"type": "greeting",
		"text": "hello from the Go host",
	}); err != nil {
		log.Fatal(err)
	}

	err := vm.RunContext(ctx, `package main
import "strings"

func main() {
	message := <-hostIn
	hostOut <- map[string]any{
		"type": "reply",
		"text": strings.ToUpper(message["text"]),
	}
}`)
	if err != nil {
		log.Fatal(err)
	}

	reply, err := bridge.Receive(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("host received: %#v\n", reply)
}
