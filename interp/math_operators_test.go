package interp

import (
	"fmt"
	"go/token"
	"math"
	"testing"
)

func TestNumericOperatorFastPaths(t *testing.T) {
	vm := NewInterpreter()
	for _, pair := range [][2]int{{0, 1}, {17, 3}, {-17, 3}, {17, -3}, {-17, -3}, {int(^uint(0) >> 1), -1}, {-int(^uint(0)>>1) - 1, -1}} {
		a, b := pair[0], pair[1]
		cases := map[token.Token]any{token.ADD: a + b, token.SUB: a - b, token.MUL: a * b, token.QUO: a / b, token.REM: a % b, token.AND: a & b, token.OR: a | b, token.XOR: a ^ b, token.AND_NOT: a &^ b, token.EQL: a == b, token.NEQ: a != b, token.LSS: a < b, token.GTR: a > b, token.LEQ: a <= b, token.GEQ: a >= b}
		for op, want := range cases {
			got, err := vm.applyBinaryOp(op, a, b)
			if err != nil || got != want {
				t.Fatalf("%d %s %d = %v (%v), want %v", a, op, b, got, err, want)
			}
		}
	}
	for _, a := range []int{0, 1, -1, 12345} {
		for _, b := range []int{0, 1, 31, 32, 63, 64, 128} {
			for op, want := range map[token.Token]int{token.SHL: a << uint(b), token.SHR: a >> uint(b)} {
				got, err := vm.applyBinaryOp(op, a, b)
				if err != nil || got != want {
					t.Fatalf("shift %d %s %d: %v %v", a, op, b, got, err)
				}
			}
		}
	}
	for _, a := range []float64{0, math.Copysign(0, -1), 1.5, -2.5, math.Inf(1), math.Inf(-1), math.NaN()} {
		for _, b := range []float64{0, math.Copysign(0, -1), 2, math.Inf(1), math.NaN()} {
			for op, want := range map[token.Token]float64{token.ADD: a + b, token.SUB: a - b, token.MUL: a * b, token.QUO: a / b} {
				got, err := vm.applyBinaryOp(op, a, b)
				value, ok := got.(float64)
				if err != nil || !ok || !(math.IsNaN(want) && math.IsNaN(value)) && math.Float64bits(value) != math.Float64bits(want) {
					t.Fatalf("float %v %s %v: got %v want %v (%v)", a, op, b, got, want, err)
				}
			}
		}
	}
}

func TestIntegerDivisionAssignments(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func main(){a,b:=7,2;q:=a/b;fmt.Println(q);q=a/b;fmt.Println(q);a=-7;q=a/b;fmt.Println(q);m:=map[string]int{};m["q"]=a/b;fmt.Println(m["q"]);defer func(){fmt.Println(recover())}();b=0;q=a/b;fmt.Println(q)}`)
	want := "3\n3\n-3\n-3\nruntime error: integer divide by zero\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

func TestCompoundMathAssignments(t *testing.T) {
	out := runAndCapture(t, `package main
import "fmt"
func main(){x:=12345;x+=3;x-=2;x*=4;x/=2;x%=1000;x|=512;x&=1023;x^=127;x&^=8;x<<=2;x>>=1;fmt.Println(x);f:=1.5;f+=0.5;f*=3.0;f-=1.0;f/=2.0;fmt.Println(f)}`)
	x := 12345
	x += 3
	x -= 2
	x *= 4
	x /= 2
	x %= 1000
	x |= 512
	x &= 1023
	x ^= 127
	x &^= 8
	x <<= 2
	x >>= 1
	if out != fmt.Sprint(x, "\n2.5\n") {
		t.Fatalf("output=%q", out)
	}
}
