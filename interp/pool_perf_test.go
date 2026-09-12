package interp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestWaitGroupConcurrentGenerations(t *testing.T) {
	w := newNativeWaitGroup()
	for round := 0; round < 40; round++ {
		if err := w.Add(8); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := w.Wait(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		var host sync.WaitGroup
		for i := 0; i < 4; i++ {
			host.Add(1)
			go func() {
				defer host.Done()
				if err := w.Wait(context.Background()); err != nil {
					t.Error(err)
				}
			}()
		}
		for i := 0; i < 8; i++ {
			host.Add(1)
			go func() {
				defer host.Done()
				if err := w.Add(-1); err != nil {
					t.Error(err)
				}
			}()
		}
		host.Wait()
		if err := w.Wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Add(-1); err == nil {
		t.Fatal("negative counter accepted")
	}
	if err := w.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerPoolRepeatedBatches(t *testing.T) {
	out := runAndCapture(t, `package main
import "sync"
func worker(jobs chan int, results chan int, wg *sync.WaitGroup){
 defer wg.Done()
 for n:=range jobs { results<-n*n }
}
func main(){
 var wg sync.WaitGroup
 for batch:=0;batch<3;batch++ {
  jobs:=make(chan int,32); results:=make(chan int,32)
  wg.Add(4)
  for i:=0;i<4;i++ {go worker(jobs,results,&wg)}
  for i:=0;i<32;i++ {jobs<-i};close(jobs)
  wg.Wait();close(results)
  total:=0;for n:=range results {total=total+n};ConsoleLog(total)
 }
}`)
	if strings.TrimSpace(out) != "10416\n10416\n10416" {
		t.Fatal(out)
	}
}

func TestTickerDropsTicksAndStops(t *testing.T) {
	ch := &ChannelVal{C: make(chan any, 1)}
	first := time.UnixMilli(1000)
	second := time.UnixMilli(2000)
	publishTimerTick(ch, first)
	publishTimerTick(ch, second)
	if got := <-ch.C; got != 1000 {
		t.Fatal(got)
	}
	publishTimerTick(ch, second)
	if got := <-ch.C; got != 2000 {
		t.Fatal(got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	v, err := newNativeTimer(ctx, 1, true)
	if err != nil {
		t.Fatal(err)
	}
	c := v.Fields["C"].(*ChannelVal)
	if _, open, err := c.Receive(ctx); !open || err != nil {
		t.Fatalf("%v %v", open, err)
	}
	stopNativeTimer(v)
	stopNativeTimer(v)
	// Stop does not close or discard the guest's already-buffered output.
	if c.closed.Load() {
		t.Fatal("Stop closed ticker channel")
	}
}

func BenchmarkWaitGroupReuse(b *testing.B) {
	w := newNativeWaitGroup()
	ctx := context.Background()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := w.Add(4); err != nil {
			b.Fatal(err)
		}
		for j := 0; j < 4; j++ {
			if err := w.Add(-1); err != nil {
				b.Fatal(err)
			}
		}
		if err := w.Wait(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkTickerFullBuffer(b *testing.B) {
	ch := &ChannelVal{C: make(chan any, 1)}
	ch.C <- 1
	now := time.Now()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		publishTimerTick(ch, now)
	}
}
