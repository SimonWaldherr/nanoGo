package interp

import (
	"sync"
	"testing"
)

func TestTracerEventsSince(t *testing.T) {
	for _, capacity := range []int{1, 2, 7} {
		tracer := NewTracer(capacity)
		for count := 1; count <= 20; count++ {
			tracer.record(TraceEvent{Kind: "event"})
			for cursor := 0; cursor <= count+1; cursor++ {
				events := tracer.EventsSince(uint64(cursor))
				first := max(cursor+1, count-capacity+1)
				want := max(0, count-first+1)
				if len(events) != want {
					t.Fatalf("capacity=%d count=%d cursor=%d: got %d want %d", capacity, count, cursor, len(events), want)
				}
				for i, event := range events {
					if event.Sequence != uint64(first+i) {
						t.Fatalf("out of order: %#v", events)
					}
				}
			}
		}
		tracer.Reset()
		if len(tracer.Events()) != 0 {
			t.Fatal("reset retained events")
		}
		tracer.record(TraceEvent{Kind: "after reset"})
		if events := tracer.EventsSince(20); len(events) != 1 || events[0].Sequence != 21 {
			t.Fatalf("reset cursor: %#v", events)
		}
	}
}

func TestTracerConcurrentOrdering(t *testing.T) {
	tracer := NewTracer(4096)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 512; i++ {
				tracer.record(TraceEvent{Kind: "parallel"})
			}
		}()
	}
	wg.Wait()
	events := tracer.Events()
	for i, event := range events {
		if event.Sequence != uint64(i+1) {
			t.Fatalf("event %d sequence %d", i, event.Sequence)
		}
		if i > 0 && event.At.Before(events[i-1].At) {
			t.Fatal("timestamps out of order")
		}
	}
}

func BenchmarkTracerPolling(b *testing.B) {
	tracer := NewTracer(4096)
	for i := 0; i < 4096; i++ {
		tracer.record(TraceEvent{Kind: "event"})
	}
	for _, cursor := range []uint64{0, 4095, 4096} {
		name := "Full"
		if cursor == 4095 {
			name = "OneNew"
		}
		if cursor == 4096 {
			name = "NoNew"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_ = tracer.EventsSince(cursor)
			}
		})
	}
}
