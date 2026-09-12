package interp

import (
	"errors"
	"reflect"
	"testing"
)

func TestCallArgumentsOrderAndLifetime(t *testing.T) {
	for _, call := range []string{"capture", "host.Capture"} {
		t.Run(call, func(t *testing.T) {
			vm, _ := newTestVM()
			var retained [][]any
			capture := func(args []any) (any, error) { retained = append(retained, args); return nil, nil }
			vm.RegisterNative("capture", capture)
			vm.RegisterPackage("host", &Package{Name: "host", Funcs: map[string]*Function{"Capture": {Name: "Capture", Native: capture}}})
			if err := vm.Run(`package main
var count = 0
func next() int { count++; return count }
func identity(v int) int { return v }
func main() {
 ` + call + `(next(), identity(next()))
 ` + call + `(next(), next())
 values := []int{next(), next()}
 ` + call + `(values...)
 values[0] = 99
 ` + call + `()
}`); err != nil {
				t.Fatal(err)
			}
			want := [][]any{{1, 2}, {3, 4}, {5, 6}, {}}
			if !reflect.DeepEqual(retained, want) {
				t.Fatalf("retained arguments=%#v want %#v", retained, want)
			}
		})
	}
}

func TestCallArgumentFailureStopsEvaluation(t *testing.T) {
	for _, call := range []string{"capture", "host.Capture"} {
		t.Run(call, func(t *testing.T) {
			vm, _ := newTestVM()
			failure := errors.New("argument failed")
			invoked := false
			vm.RegisterNative("fail", func([]any) (any, error) { return nil, failure })
			capture := func([]any) (any, error) { invoked = true; return nil, nil }
			vm.RegisterNative("capture", capture)
			vm.RegisterPackage("host", &Package{Name: "host", Funcs: map[string]*Function{"Capture": {Name: "Capture", Native: capture}}})
			err := vm.Run("package main\nfunc main(){" + call + "(fail(),capture())}")
			if !errors.Is(err, failure) || invoked {
				t.Fatalf("err=%v invoked=%t", err, invoked)
			}
		})
	}
}
