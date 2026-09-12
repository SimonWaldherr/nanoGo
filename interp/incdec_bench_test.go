package interp

import "testing"

func BenchmarkIncDecBindings(b *testing.B) {
	for _, name := range []string{"a", "d"} {
		b.Run(name, func(b *testing.B) {
			vm := NewInterpreter()
			env := NewEnv(nil)
			for _, n := range []string{"a", "b", "c", "d"} {
				vm.declareInt(n, 1000, env)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				vm.addInt(name, 1, env)
				vm.addInt(name, -1, env)
			}
		})
	}
}

func BenchmarkIncDecLoop(b *testing.B) {
	vm, _ := newTestVM()
	const source = `package main
func main(){ a,b,c,d:=1000,1000,1000,1000;for n:=0;n<10000;n++ {a++;b--;c++;d--;a--;b++;c--;d++} }`
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := vm.Run(source); err != nil {
			b.Fatal(err)
		}
	}
}
