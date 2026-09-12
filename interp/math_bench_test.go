package interp

import (
	"go/token"
	"testing"
)

func BenchmarkNumericOperators(b *testing.B) {
	vm := NewInterpreter()
	for _, op := range []token.Token{token.ADD, token.SUB, token.MUL, token.QUO, token.REM, token.AND, token.OR, token.XOR, token.AND_NOT, token.SHL, token.SHR, token.LSS} {
		b.Run(op.String(), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				_, _ = vm.applyBinaryOp(op, 12345, 3)
			}
		})
	}
}
func BenchmarkMathWorkloads(b *testing.B) {
	for _, tc := range []struct{ name, src string }{
		{"Division", `package main
func main(){sum:=0;for i:=1;i<10000;i++ {sum=sum+i/3};_ =sum}`},
		{"Float", `package main
func main(){x:=0.125;for i:=0;i<10000;i++ {x=(x+0.25)*1.001-0.1;x=x/1.01};_=x}`},
		{"Math", `package main
import "math"
func main(){for i:=1;i<1000;i++ {_ =math.Sqrt(i);_=math.Sin(i);_=math.Cos(i);_=math.Pow(i,0.5);_=math.Log(i);_=math.Abs(-i);_=math.Min(i,2);_=math.Max(i,2);_=math.Floor(1.2);_=math.Ceil(1.2);_=math.Round(1.2);_=math.Log2(i);_=math.Log10(i)}}`},
	} {
		b.Run(tc.name, func(b *testing.B) {
			vm, _ := newTestVM()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := vm.Run(tc.src); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
