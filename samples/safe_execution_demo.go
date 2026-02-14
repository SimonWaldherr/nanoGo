// samples/safe_execution_demo.go
//
// This file is NOT meant to be compiled with `go build`.
// It is Go source that gets interpreted by nanoGo at runtime,
// demonstrating how untrusted code runs safely inside a host app.
//
// Run it:  make run-demo            (uses samples/features_demo.go)
//      or: ./build/nanogo-cli samples/safe_execution_demo.go
//
// Safety guarantees provided by the nanoGo interpreter:
//   1. No file-system access   — os, io, etc. are not available.
//   2. No network access       — net, http client sockets are absent.
//   3. No unsafe / reflect     — pointer arithmetic is impossible.
//   4. Wall-clock timeout      — the host cancels long-running code.
//   5. Panic recovery          — a panic in user code cannot crash the host.
//   6. Selective API surface   — only explicitly registered functions
//      are reachable from interpreted code.

package main

import (
	"fmt"
	"math"
	"strings"
	"time"
	"sync"
)

// --- Basic computation ---

func fibonacci(n int) int {
	if n <= 1 {
		return n
	}
	return fibonacci(n-1) + fibonacci(n-2)
}

// --- Struct with methods ---

type Point struct {
	X float64
	Y float64
}

func (p Point) Distance() float64 {
	return math.Sqrt(p.X*p.X + p.Y*p.Y)
}

func main() {
	fmt.Println("=== nanoGo safe execution demo ===")
	fmt.Println()

	// 1. Function calls & recursion
	fmt.Println("-- Fibonacci --")
	for i := 0; i < 10; i++ {
		fmt.Printf("fib(%d) = %d\n", i, fibonacci(i))
	}

	// 2. String manipulation (safe: no file I/O)
	fmt.Println()
	fmt.Println("-- Strings --")
	greeting := "Hello, nanoGo World!"
	fmt.Println(strings.ToUpper(greeting))
	parts := strings.Split(greeting, " ")
	fmt.Printf("Word count: %d\n", len(parts))

	// 3. Structs & methods
	fmt.Println()
	fmt.Println("-- Structs --")
	p := Point{X: 3.0, Y: 4.0}
	fmt.Printf("Point(%v, %v) distance = %v\n", p.X, p.Y, p.Distance())

	// 4. Goroutines & channels (sandboxed concurrency)
	fmt.Println()
	fmt.Println("-- Concurrency --")
	ch := make(chan string, 3)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		ch <- "alpha"
		ch <- "bravo"
		ch <- "charlie"
		close(ch)
	}()

	for msg := range ch {
		fmt.Println("received:", msg)
	}
	wg.Wait()

	// 5. Timing (safe: Sleep is capped by the host timeout)
	fmt.Println()
	fmt.Println("-- Timing --")
	t0 := time.Now()
	time.Sleep(25)
	elapsed := time.Since(t0)
	fmt.Printf("Elapsed: ~%dms\n", elapsed)

	// 6. Math
	fmt.Println()
	fmt.Println("-- Math --")
	fmt.Printf("sqrt(2)   = %v\n", math.Sqrt(2.0))
	fmt.Printf("pow(2,10) = %v\n", math.Pow(2.0, 10.0))

	fmt.Println()
	fmt.Println("=== demo complete ===")
}
