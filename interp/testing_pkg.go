// interp/testing_pkg.go
package interp

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// errTestFatal is returned by testing.T.Fatalf's native implementation. Once
// it crosses callFunction's return like any other native error, it unwinds
// the whole guest call stack up to whoever invoked the test function (see
// interp/loader's RunPackageTests) — no new control-flow primitive needed.
// nanoGo does have a guest-visible recover() builtin (interp/evaluator.go),
// but it cannot intercept this: recover() only fires when callFrame.panicking
// is set, and that only ever happens for a *panicError (a guest panic()) or
// an unexpected native Go panic — never for a plain sentinel error like this
// one returned normally, so a guest defer genuinely cannot catch it. This
// mirrors real Go, where recover() cannot stop runtime.Goexit() either.
var errTestFatal = errors.New("nanogo: testing.T.Fatalf")

// errTestSkip mirrors testing.T.Skipf's Goexit behaviour. It is deliberately
// distinct from a failure so a host test runner can report it as skipped.
var errTestSkip = errors.New("nanogo: testing.T.Skipf")

// IsTestFatal reports whether err was produced by testing.T.Fatalf, as
// opposed to a genuine guest panic or another runtime error.
func IsTestFatal(err error) bool { return errors.Is(err, errTestFatal) }

// IsTestSkipped reports whether err was produced by testing.T.Skip/Skipf.
func IsTestSkipped(err error) bool { return errors.Is(err, errTestSkip) }

// testState is the native backing object for a *testing.T (or *testing.B)
// StructVal, mirroring the nativeWaitGroup/nativeTimer pattern used
// elsewhere in this file's sibling packages.go.
type testState struct {
	mu       sync.Mutex
	failed   bool
	skipped  bool
	messages []string
}

func newTestState() *testState { return &testState{} }

func ensureTestState(v any) *testState {
	if sv, ok := v.(*StructVal); ok {
		if ts, ok := sv.Fields["__native"].(*testState); ok {
			return ts
		}
		ts := newTestState()
		sv.Fields["__native"] = ts
		return ts
	}
	return newTestState()
}

// TestFailed reports whether t (a *testing.T value from RegisterBuiltinPackages)
// recorded a failure via Errorf or Fatalf.
func TestFailed(t any) bool { return ensureTestState(t).failed }

// TestSkipped reports whether t called Skip or Skipf.
func TestSkipped(t any) bool { return ensureTestState(t).skipped }

// TestMessages returns every message recorded via Errorf/Fatalf on t, in call order.
func TestMessages(t any) []string {
	ts := ensureTestState(t)
	ts.mu.Lock()
	defer ts.mu.Unlock()
	return append([]string(nil), ts.messages...)
}

// NewTestT constructs a fresh *testing.T value for a host-side test runner
// (see interp/loader) to pass into a TestXxx(t *testing.T) function.
func (vm *Interpreter) NewTestT() any {
	return &StructVal{TypeName: "T", Fields: map[string]any{"__native": newTestState()}}
}

func (vm *Interpreter) recordTestFailure(recv any, format string, args []any, fatal bool) (any, error) {
	message := format
	if sp, ok := vm.natives["__hostSprintf"]; ok {
		if res, err := sp(append([]any{format}, args...)); err == nil {
			message = ToString(res)
		}
	}
	ts := ensureTestState(recv)
	ts.mu.Lock()
	ts.failed = true
	ts.messages = append(ts.messages, message)
	ts.mu.Unlock()
	vm.emitAssertionTrace("T", AssertionEvent{Format: format, Args: args, Fatal: fatal})
	if fatal {
		return nil, errTestFatal
	}
	return nil, nil
}

func testArgsMessage(args []any) string {
	parts := make([]string, len(args))
	for i, arg := range args {
		parts[i] = ToString(arg)
	}
	return strings.Join(parts, " ")
}

func (vm *Interpreter) recordTestSkip(recv any, format string, args []any) (any, error) {
	message := format
	if sp, ok := vm.natives["__hostSprintf"]; ok {
		if res, err := sp(append([]any{format}, args...)); err == nil {
			message = ToString(res)
		}
	}
	ts := ensureTestState(recv)
	ts.mu.Lock()
	ts.skipped = true
	ts.messages = append(ts.messages, message)
	ts.mu.Unlock()
	return nil, errTestSkip
}

// benchState is the native backing object for a *testing.B StructVal. Steps
// (see StepCount) are nanoGo's primary, deterministic benchmark metric;
// this only tracks the secondary wall-clock metric, honoring
// ResetTimer/StartTimer/StopTimer the same way real testing.B does so a
// guest's own setup code can be excluded from the measured window.
type benchState struct {
	mu           sync.Mutex
	start        time.Time
	elapsed      time.Duration
	running      bool
	reportAllocs bool
}

func newBenchState() *benchState { return &benchState{start: time.Now(), running: true} }

func (b *benchState) ResetTimer() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.elapsed = 0
	if b.running {
		b.start = time.Now()
	}
}

func (b *benchState) StopTimer() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		b.elapsed += time.Since(b.start)
		b.running = false
	}
}

func (b *benchState) StartTimer() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running {
		b.start = time.Now()
		b.running = true
	}
}

func (b *benchState) Elapsed() time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return b.elapsed + time.Since(b.start)
	}
	return b.elapsed
}

func ensureBenchState(v any) *benchState {
	if sv, ok := v.(*StructVal); ok {
		if bs, ok := sv.Fields["__native"].(*benchState); ok {
			return bs
		}
		bs := newBenchState()
		sv.Fields["__native"] = bs
		return bs
	}
	return newBenchState()
}

// NewTestB constructs a fresh *testing.B value with b.N set to n, for a
// host-side benchmark runner (see interp/loader) to pass into a
// BenchmarkXxx(b *testing.B) function.
func (vm *Interpreter) NewTestB(n int) any {
	return &StructVal{TypeName: "B", Fields: map[string]any{"N": n, "__native": newBenchState()}}
}

// BenchElapsed returns the wall-clock duration b has been "running" for
// (respecting any ResetTimer/StartTimer/StopTimer calls the guest made),
// for a host-side benchmark runner to divide by b.N.
func BenchElapsed(b any) time.Duration { return ensureBenchState(b).Elapsed() }

// registerTestingPackage installs a curated "testing" package exposing a
// minimal *testing.T (Errorf/Fatalf/Run/Helper) and *testing.B (N/
// ResetTimer/StartTimer/StopTimer/ReportAllocs). It is a real, if partial,
// subset: an unmodified func TestXxx(t *testing.T)/BenchmarkXxx(b *testing.B)
// using only these methods runs unchanged both in nanoGo and through a real
// `go test`/`go test -bench`.
func registerTestingPackage(vm *Interpreter) {
	tType := &TypeDef{Name: "T", Kind: "struct", Methods: map[string]*Function{}}
	vm.types["T"] = tType

	tType.Methods["Errorf"] = &Function{Name: "Errorf", RecvType: "T", Params: []string{"format"}, IsVariadic: true, Native: func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, nil
		}
		return vm.recordTestFailure(args[0], ToString(args[1]), args[2:], false)
	}}
	tType.Methods["Error"] = &Function{Name: "Error", RecvType: "T", Params: []string{"args"}, IsVariadic: true, Native: func(args []any) (any, error) {
		if len(args) < 2 {
			return vm.recordTestFailure(args[0], "", nil, false)
		}
		return vm.recordTestFailure(args[0], "%s", []any{testArgsMessage(args[1:])}, false)
	}}
	tType.Methods["Fatalf"] = &Function{Name: "Fatalf", RecvType: "T", Params: []string{"format"}, IsVariadic: true, Native: func(args []any) (any, error) {
		if len(args) < 2 {
			return nil, nil
		}
		return vm.recordTestFailure(args[0], ToString(args[1]), args[2:], true)
	}}
	tType.Methods["Fatal"] = &Function{Name: "Fatal", RecvType: "T", Params: []string{"args"}, IsVariadic: true, Native: func(args []any) (any, error) {
		if len(args) < 2 {
			return vm.recordTestFailure(args[0], "", nil, true)
		}
		return vm.recordTestFailure(args[0], "%s", []any{testArgsMessage(args[1:])}, true)
	}}
	tType.Methods["Skipf"] = &Function{Name: "Skipf", RecvType: "T", Params: []string{"format"}, IsVariadic: true, Native: func(args []any) (any, error) {
		if len(args) < 2 {
			return vm.recordTestSkip(args[0], "", nil)
		}
		return vm.recordTestSkip(args[0], ToString(args[1]), args[2:])
	}}
	tType.Methods["Skip"] = &Function{Name: "Skip", RecvType: "T", Params: []string{"args"}, IsVariadic: true, Native: func(args []any) (any, error) {
		return vm.recordTestSkip(args[0], "%s", []any{testArgsMessage(args[1:])})
	}}
	tType.Methods["Helper"] = &Function{Name: "Helper", RecvType: "T", Native: func(args []any) (any, error) {
		return nil, nil
	}}
	tType.Methods["Run"] = &Function{Name: "Run", RecvType: "T", Params: []string{"name", "fn"}, Native: func(args []any) (any, error) {
		if len(args) < 3 {
			return false, nil
		}
		fn, ok := args[2].(*Function)
		if !ok {
			return false, NewRuntimeError("testing: T.Run: second argument must be a function")
		}
		childT := vm.NewTestT()
		_, err := vm.callFunction(fn, fn.Env, nil, []any{childT})
		// Fatalf's sentinel only bounds the subtest, matching real Go's
		// per-goroutine runtime.Goexit(): it must not also unwind the
		// caller's own test. Any other error (a genuine panic, a step-limit
		// error, ...) does propagate, matching a subtest crash taking down
		// the run.
		if err != nil && !IsTestFatal(err) && !IsTestSkipped(err) {
			return false, err
		}
		passed := !TestFailed(childT)
		if !passed {
			parent := ensureTestState(args[0])
			parent.mu.Lock()
			parent.failed = true
			parent.mu.Unlock()
		}
		return passed, nil
	}}

	// N is intentionally guest-visible so browser demos can construct a
	// testing.B value and exercise the same benchmark loop shape as go test.
	bType := &TypeDef{Name: "B", Kind: "struct", Fields: []FieldDef{{Name: "N", Type: "int"}}, Methods: map[string]*Function{}}
	vm.types["B"] = bType
	bType.Methods["ResetTimer"] = &Function{Name: "ResetTimer", RecvType: "B", Native: func(args []any) (any, error) {
		ensureBenchState(args[0]).ResetTimer()
		return nil, nil
	}}
	bType.Methods["StartTimer"] = &Function{Name: "StartTimer", RecvType: "B", Native: func(args []any) (any, error) {
		ensureBenchState(args[0]).StartTimer()
		return nil, nil
	}}
	bType.Methods["StopTimer"] = &Function{Name: "StopTimer", RecvType: "B", Native: func(args []any) (any, error) {
		ensureBenchState(args[0]).StopTimer()
		return nil, nil
	}}
	bType.Methods["ReportAllocs"] = &Function{Name: "ReportAllocs", RecvType: "B", Native: func(args []any) (any, error) {
		bs := ensureBenchState(args[0])
		bs.mu.Lock()
		bs.reportAllocs = true
		bs.mu.Unlock()
		return nil, nil
	}}

	vm.RegisterPackage("testing", &Package{Name: "testing", Types: map[string]*TypeDef{"T": tType, "B": bType}})
}
