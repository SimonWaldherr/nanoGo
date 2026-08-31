package interp

import "testing"

func TestUnnamedMethodReceiverRuns(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
type Value struct{}
func (Value) Label() string { return "ok" }
func main() { fmt.Println(Value{}.Label()) }
`)
	if got, want := out, "ok\n"; got != want {
		t.Fatalf("unnamed receiver output = %q, want %q", got, want)
	}
}
