// concurrency runs a nanoGo guest program that combines a buffered channel,
// a goroutine, sync.WaitGroup, and defer. Run it with:
// go run ./examples/concurrency
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

func main() {
	vm := interp.NewInterpreter()
	interp.RegisterBuiltinPackages(vm)
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Println(interp.ToString(args[0]))
		}
		return nil, nil
	})
	vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		return fmt.Sprintf(interp.ToString(args[0]), args[1:]...), nil
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := vm.RunContext(ctx, source); err != nil {
		log.Fatal(err)
	}
}

const source = `package main

import (
	"fmt"
	"sync"
)

func main() {
	jobs := make(chan int, 2)
	results := make(chan int, 2)
	jobs <- 2
	jobs <- 3
	close(jobs)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for job := range jobs {
			results <- job * job
		}
	}()

	wg.Wait()
	close(results)
	for result := range results {
		fmt.Println("square:", result)
	}
}`
