package main

import (
	"strings"
	"testing"
	"time"
)

func TestRunSafeHelloWorld(t *testing.T) {
	err := RunSafe(`
package main
import "fmt"
func main() { fmt.Println("hello") }
`, 5*time.Second)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestRunSafePanicRecovery(t *testing.T) {
	err := RunSafe(`
package main
func main() { panic("kaboom") }
`, 5*time.Second)
	if err == nil {
		t.Fatal("expected error from panic")
	}
	if !strings.Contains(err.Error(), "kaboom") {
		t.Errorf("expected 'kaboom' in error, got %q", err.Error())
	}
}

func TestRunSafeTimeout(t *testing.T) {
	err := RunSafe(`
package main
import "time"
func main() { time.Sleep(10000) }
`, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected 'timed out' in error, got %q", err.Error())
	}
}

func TestRunSafeSyntaxError(t *testing.T) {
	err := RunSafe(`this is not valid go code at all`, 5*time.Second)
	if err == nil {
		t.Fatal("expected syntax error")
	}
}

func TestRunSafeNoMain(t *testing.T) {
	err := RunSafe(`
package main
func helper() int { return 1 }
`, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for missing main()")
	}
}

func TestRunSafeArithmetic(t *testing.T) {
	err := RunSafe(`
package main
import "fmt"
func main() {
	x := 2 + 3
	fmt.Println(x)
}
`, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}
