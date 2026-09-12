package interp

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestNumericExactTypes(t *testing.T) {
	out := runAndCapture(t, `package main
import ("fmt"; n "numeric")
func main(){
 a:=n.Decimal("0.1");b:=n.Decimal("0.2")
 fmt.Println(a.Add(b).String(),a.String(),a.Cmp(b))
 fmt.Println(n.Decimal("-1.005").Fixed(2),n.Decimal("1.005").Fixed(2))
 fmt.Println(n.Decimal("1").Div(n.Decimal("8")).String())
 fmt.Println(n.BigInt("999999999999999999999999").Add(n.BigInt("1")).String())
 fmt.Println(n.BigInt("-7").Div(n.BigInt("2")).String())
 r:=n.Rational("2","6");fmt.Println(r.String(),r.Add(r).String(),r.Fixed(4))
 price:=n.Money("19.99","EUR");tax:=n.Decimal("1.19")
 total:=price.Mul(tax);fmt.Println(total.String(),total.Fixed(2),price.String())
 fmt.Println(price.Sub(n.Money("0.99","EUR")).String())
 fmt.Println(n.Decimal("-3.5").Abs().Neg().String())
}`)
	want := "0.3 0.1 -1\n-1.01 1.01\n0.125\n1000000000000000000000000\n-3\n1/3 2/3 0.3333\nEUR 23.7881 23.79 EUR 19.99\nEUR 19\n-3.5\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}
func TestNumericVectorsAndCoordinates(t *testing.T) {
	out := runAndCapture(t, `package main
import ("fmt"; "numeric")
func main(){
 v:=numeric.Vec2(3,4);w:=v.Scale(2);fmt.Println(v.Norm(),w.X,w.Y,v.Dot(w))
 u:=v.Normalize();fmt.Println(u.X,u.Y)
 a:=numeric.Vec3(1,0,0);b:=numeric.Vec3(0,1,0);c:=a.Cross(b);fmt.Println(c.X,c.Y,c.Z)
 p:=numeric.Coordinate(10,20);q:=p.Translate(v);d:=q.Sub(p);fmt.Println(q.X,q.Y,d.X,d.Y,p.Distance(q))
 fmt.Println(v.Sub(numeric.Vec2(1,1)).Add(numeric.Vec2(2,2)).X)
}`)
	want := "5 6 8 50\n0.6 0.8\n0 0 1\n13 24 3 4 5\n4\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}
func TestNumericValidation(t *testing.T) {
	cases := []struct{ src, want string }{
		{`numeric.Decimal(0.1)`, "numeric string"},
		{`numeric.Decimal("1e1000000")`, "invalid numeric string"},
		{`numeric.Rational("1","0")`, "zero denominator"},
		{`numeric.Decimal("1").Div(numeric.Decimal("3"))`, "non-terminating"},
		{`numeric.Decimal("1").Div(numeric.Decimal("0"))`, "division by zero"},
		{`numeric.Money("1","eur")`, "uppercase"},
		{`numeric.Money("1","EUR").Add(numeric.Money("1","USD"))`, "matching currencies"},
		{`numeric.Money("1","EUR").Mul(numeric.Money("1","EUR"))`, "dimensionless"},
		{`numeric.Decimal("1").Add(numeric.Money("1","EUR"))`, "cannot mix"},
		{`numeric.BigInt("1").Add(numeric.Decimal("1"))`, "requires BigInt"},
		{`numeric.Decimal("1").Fixed(129)`, "0..128"},
		{`numeric.Vec2(0,0).Normalize()`, "normalize zero"},
		{`numeric.Vec2(1,2).Add(numeric.Vec3(1,2,3))`, "expected Vec2"},
		{`numeric.Coordinate(1,2).Translate(numeric.Coordinate(1,2))`, "expected Vec2"},
		{`numeric.Vec2("1",2)`, "int or float64"},
		{`numeric.Vec2(1)`, "expects 2"},
	}
	for _, tc := range cases {
		t.Run(tc.src, func(t *testing.T) {
			vm, _ := newTestVM()
			err := vm.Run("package main\nimport \"numeric\"\nfunc main(){_=" + tc.src + "}")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}
func TestNumericJSONAndHostBridge(t *testing.T) {
	vm, out := newTestVM()
	var host any
	vm.RegisterNative("capture", func(args []any) (any, error) { var err error; host, err = BridgeToHost(args[0]); return nil, err })
	err := vm.Run(`package main
import ("numeric";"encoding/json";"fmt")
func main(){m:=numeric.Money("9007199254740993.01","EUR");capture(m);s,_:=json.Marshal(m);fmt.Println(s)}`)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"Amount": "9007199254740993.01", "Currency": "EUR"}
	if !reflect.DeepEqual(host, want) {
		t.Fatalf("host=%#v", host)
	}
	if !strings.Contains(out.String(), `"Amount":"9007199254740993.01"`) {
		t.Fatalf("JSON=%q", out.String())
	}
}
func TestNumericExample(t *testing.T) {
	source, err := os.ReadFile("../examples/numeric/program.ng")
	if err != nil {
		t.Fatal(err)
	}
	vm, out := newTestVM()
	if err := vm.Run(string(source)); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "23.79") {
		t.Fatalf("output=%q", out.String())
	}
}

func TestNumericOperatorErrors(t *testing.T) {
	for _, statement := range []string{
		`_=v+1`, `_=1+v`, `_=v*2`, `_= -v`, `v++`, `v--`, `v+=1`,
	} {
		t.Run(statement, func(t *testing.T) {
			vm, _ := newTestVM()
			err := vm.Run(`package main
import "numeric"
func main(){v:=10; {v:=numeric.Decimal("2"); ` + statement + `};_=v}`)
			if err == nil || !strings.Contains(err.Error(), "numeric:") {
				t.Fatalf("expected numeric error, got %v", err)
			}
		})
	}
}
func TestNumericFiniteAndBounds(t *testing.T) {
	for _, expression := range []string{
		`numeric.Vec2(math.Inf(1),0)`,
		`numeric.Vec3(0,math.NaN(),0)`,
		`numeric.Vec2(1e308,0).Scale(2)`,
		`numeric.Decimal("` + strings.Repeat("9", 256) + `").Mul(numeric.Decimal("10"))`,
		`numeric.Decimal("0.` + strings.Repeat("0", 128) + `1")`,
	} {
		vm, _ := newTestVM()
		if err := vm.Run(`package main
import("numeric";"math")
func main(){_=` + expression + `}`); err == nil {
			t.Fatalf("accepted %s", expression)
		}
	}
	out := runAndCapture(t, `package main
import("numeric";"fmt")
func main(){a:=numeric.Vec2(1e308,1e308).Normalize();b:=numeric.Vec2(1e-300,0).Normalize();fmt.Println(a.Norm(),b.X,b.Y)}`)
	if out != "1 1 0\n" {
		t.Fatalf("normalization: %q", out)
	}
}
func TestNumericMutatedFieldsValidated(t *testing.T) {
	vm, _ := newTestVM()
	err := vm.Run(`package main
import "numeric"
func main(){m:=numeric.Money("1","EUR");m.Currency="invalid";_=m.Add(m)}`)
	if err == nil || !strings.Contains(err.Error(), "currency") {
		t.Fatalf("got %v", err)
	}
}
