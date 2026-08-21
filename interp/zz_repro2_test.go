package interp

import "testing"

func TestReproLabeledDeclInLoopBody(t *testing.T) {
	vm, buf := newTestVM()
	err := vm.Run(`
package main
import "fmt"
func main() {
	for i := 0; i < 3; i++ {
	Retry:
		y := i * 2
		_ = Retry
		fmt.Println(y)
	}
}
`)
	t.Logf("err=%v out=%q", err, buf.String())
}
