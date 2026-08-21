package interp

import "testing"

func TestReproGotoRedeclare(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	i := 0
Loop:
	x := i
	i++
	if i < 3 { goto Loop }
	fmt.Println("final x:", x)
}
`)
	t.Logf("out=%q", out)
	if out != "final x: 2\n" {
		t.Errorf("got %q", out)
	}
}

func TestReproDanglingLabel(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	for i := 0; i < 3; i++ {
		if i == 1 { goto done }
		fmt.Println(i)
	}
done:
	fmt.Println("after")
}
`)
	t.Logf("out=%q", out)
	if out != "0\nafter\n" {
		t.Errorf("got %q", out)
	}
}

func TestReproDanglingLabelBareEmpty(t *testing.T) {
	out := runAndCapture(t, `
package main
import "fmt"
func main() {
	for i := 0; i < 3; i++ {
		if i == 1 { goto done }
		fmt.Println(i)
	}
done:
}
`)
	t.Logf("out=%q", out)
	if out != "0\n" {
		t.Errorf("got %q", out)
	}
}
