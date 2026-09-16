package interp

import (
	"strconv"
	"strings"
	"testing"
)

// Runs without an external Go compiler, including under the js/wasm runner.
// compat/ compares the boundary programs against standard Go separately.
func TestNumericArgumentBoundaries(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"int_max", `func f(n int){fmt.Println(n)};func main(){f(` + strconv.Itoa(int(^uint(0)>>1)) + `)}`, strconv.Itoa(int(^uint(0) >> 1))},
		{"named_small", `type Small int8;type Derived Small;func f(n Derived){fmt.Println(n)};func main(){f(-128);f(127)}`, "-128\n127"},
		{"unsigned", `type Octet uint8;func f(n Octet){fmt.Println(n)};func main(){f(0);f(255)}`, "0\n255"},
		{"float32_rounding", `type Single float32;func f(n float32){fmt.Println(float64(n)>3.4028234e38 && float64(n)<3.4028236e38)};func g(n Single){fmt.Println(float64(n)>3.4028234e38 && float64(n)<3.4028236e38)};func main(){f(3.4028235e38);g(3.4028235e38)}`, "true\ntrue"},
		{"named_string", `type Text string;func f(n Text){fmt.Println(n)};func main(){f("hello")}`, "hello"},
		{"named_boolean", `type Flag bool;func f(n Flag){fmt.Println(n)};func main(){f(1==1)}`, "true"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			vm, output := newTestVM()
			if err := vm.Run("package main;import \"fmt\";" + tc.body); err != nil {
				t.Fatal(err)
			}
			if got := strings.TrimSpace(output.String()); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestNumericArgumentOverflowRejected(t *testing.T) {
	for _, tc := range []struct{ typ, value string }{
		{"int8", "128"}, {"int8", "-129"}, {"uint8", "-1"}, {"uint8", "256"},
		{"int16", "32768"}, {"int32", "2147483648"},
		{"float32", "3.4028236e38"}, {"float32", "-3.4028236e38"},
		{"bool", "1"}, {"string", "65"},
	} {
		for _, named := range []bool{false, true} {
			t.Run(tc.typ+"/"+tc.value+"/named="+strconv.FormatBool(named), func(t *testing.T) {
				typ, decl := tc.typ, ""
				if named {
					typ = "Target"
					decl = "type Target " + tc.typ + ";"
				}
				vm, _ := newTestVM()
				if err := vm.Run("package main;" + decl + "func f(n " + typ + "){};func main(){f(" + tc.value + ")}"); err == nil {
					t.Fatal("accepted unrepresentable constant")
				}
			})
		}
	}
}
