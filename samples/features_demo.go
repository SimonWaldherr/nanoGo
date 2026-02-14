// samples/features_demo.go
package main

import (
	"fmt"
	"sync"
	"time"
)

func main() {
	fmt.Println("Hello from nanoGo demo!")
	demonstrateSleep()
	demonstrateChannels()
}

func demonstrateSleep() {
	t0 := time.Now()
	time.Sleep(50) // ms
	fmt.Printf("Slept for ~%dms\n", time.Since(t0))
}

func demonstrateChannels() {
	ch := make(chan int, 2)
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		for i := 0; i < 3; i++ {
			ch <- i
		}
		close(ch)
	}()

	for v := range ch {
		fmt.Println("got", v)
	}
	wg.Wait()
}
