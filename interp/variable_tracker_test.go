package interp

import "testing"

func TestVariableTrackerRetainsLastValues(t *testing.T) {
	vm := NewInterpreter()
	tracker := NewVariableTracker()
	vm.SetVariableTracker(tracker)
	src := `package main

func twice(n int) int { return n * 2 }

func main() {
	x := 1
	for i := 0; i < 3; i++ {
		x += i
	}
	x = twice(x)
}
`
	if err := vm.Run(src); err != nil {
		t.Fatalf("Run: %v", err)
	}

	byKey := map[string]VariableSnapshot{}
	for _, snapshot := range tracker.Snapshots() {
		byKey[snapshot.Function+"."+snapshot.Name] = snapshot
	}
	if got := byKey["main.x"]; got.Value != "8" || got.Writes != 5 || got.Line == 0 {
		t.Errorf("main.x = %+v, want last value 8, five writes and a source line", got)
	}
	if got := byKey["main.i"]; got.Value != "3" {
		t.Errorf("main.i = %+v, want last value 3", got)
	}
	if got := byKey["twice.n"]; got.Value != "4" || got.Writes != 1 {
		t.Errorf("twice.n = %+v, want argument value 4", got)
	}
}

func TestVariableTrackerIsOptIn(t *testing.T) {
	vm := NewInterpreter()
	if err := vm.Run("package main\nfunc main() { x := 1; _ = x }"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if vm.VariableTracker() != nil {
		t.Fatal("fresh interpreter unexpectedly has a variable tracker")
	}
}
