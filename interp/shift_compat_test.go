package interp

import (
	"fmt"
	"go/token"
	"strings"
	"testing"
)

func TestNegativeShiftsRecoverWithoutAssignment(t *testing.T) {
	for _, statement := range []string{
		"_ = x << n", "_ = x >> n", "x <<= n", "x >>= n",
		"_ = x << amount()", "_ = x >> amount()",
		"values[0] <<= n", "values[0] >>= n",
	} {
		t.Run(statement, func(t *testing.T) {
			for _, observed := range []bool{false, true} {
				vm, output := newTestVM()
				if observed {
					vm.SetTracer(NewTracer(32))
					vm.SetVariableTracker(NewVariableTracker())
				}
				source := fmt.Sprintf(`package main
func amount() int { return -1 }
func main() {
 x := 8; n := -1; values := []int{16}
 defer func(){ConsoleLog(recover());ConsoleLog(x);ConsoleLog(values[0])}()
 %s
 ConsoleLog("not reached")
}`, statement)
				if err := vm.Run(source); err != nil {
					t.Fatal(err)
				}
				if got := output.String(); got != "runtime error: negative shift amount\n8\n16\n" {
					t.Fatalf("observed=%v: %q", observed, got)
				}
			}
		})
	}
}

func TestShiftFallbackAndLargeAmounts(t *testing.T) {
	vm := NewInterpreter()
	for _, op := range []token.Token{token.SHL, token.SHR} {
		for _, right := range []any{int(-1), int64(-1)} {
			_, err := vm.applyBinaryOp(op, 8, right)
			if _, ok := err.(*panicError); !ok {
				t.Fatalf("%s %T: expected recoverable panic, got %v", op, right, err)
			}
		}
	}
	got := runAndCapture(t, `package main
func main(){x:=8;n:=1000;ConsoleLog(x<<n);ConsoleLog(x>>n);x<<=n;ConsoleLog(x);x=-8;x>>=n;ConsoleLog(x)}`)
	if strings.TrimSpace(got) != "0\n0\n0\n-1" {
		t.Fatal(got)
	}
}
