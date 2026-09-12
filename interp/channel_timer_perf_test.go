package interp

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestTimerStopCancelAndDelivery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	v, err := newNativeTimer(ctx, 1, false)
	if err != nil {
		t.Fatal(err)
	}
	ch := v.Fields["C"].(*ChannelVal)
	if _, open, err := ch.Receive(ctx); err != nil || !open {
		t.Fatalf("open=%v err=%v", open, err)
	}
	if !stopNativeTimer(v) || stopNativeTimer(v) {
		t.Fatal("Stop must be idempotent")
	}
	for i := 0; i < 100; i++ {
		parent, stop := context.WithCancel(context.Background())
		v, err := newNativeTimer(parent, 60000, false)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); stop() }()
		go func() { defer wg.Done(); stopNativeTimer(v) }()
		wg.Wait()
		select {
		case <-v.Fields["C"].(*ChannelVal).C:
			t.Fatal("stopped timer delivered")
		default:
		}
	}
}

func TestChannelBlockedCancellationAndDrain(t *testing.T) {
	for _, sending := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		ch := &ChannelVal{C: make(chan any)}
		result := make(chan error, 1)
		go func() {
			if sending {
				result <- ch.Send(ctx, 1)
			} else {
				_, _, err := ch.Receive(ctx)
				result <- err
			}
		}()
		cancel()
		select {
		case err := <-result:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("blocked after cancellation")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := &ChannelVal{C: make(chan any, 1)}
	if err := ch.Send(ctx, 42); err != nil {
		t.Fatal(err)
	}
	if err := ch.Close(); err != nil {
		t.Fatal(err)
	}
	if v, open, err := ch.Receive(ctx); v != 42 || !open || err != nil {
		t.Fatalf("%v %v %v", v, open, err)
	}
	if _, open, err := ch.Receive(ctx); open || err != nil {
		t.Fatalf("%v %v", open, err)
	}
}

func BenchmarkCancellableChannel(b *testing.B) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := &ChannelVal{C: make(chan any, 1)}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if err := ch.Send(ctx, 1); err != nil {
			b.Fatal(err)
		}
		if _, _, err := ch.Receive(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSleepZero(b *testing.B) {
	vm := NewInterpreter()
	RegisterBuiltinPackages(vm)
	pkg, _ := vm.Package("time")
	f := pkg.Funcs["Sleep"].NativeContext
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	args := []any{0}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := f(ctx, args); err != nil {
			b.Fatal(err)
		}
	}
}
