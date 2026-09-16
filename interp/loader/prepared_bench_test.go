package loader

import (
	"context"
	"testing"
)

// These are native measurements. WASM startup and execution are measured by
// scripts/benchmark-wasm.cjs against the actual shipping artifact separately.
func BenchmarkPreparedExecution(b *testing.B) {
	for _, work := range []struct{ name, body string }{
		{"Numerical", `sum:=0;for i:=0;i<1000;i++{sum+=i};host.Emit("sum",sum)`},
		{"NestedSlices", `a:=[][]float64{{0,0},{1,1}};sum:=0.0;for _,row:=range a{for _,v:=range row{sum+=v}};host.Emit("sum",sum)`},
		{"Serialization", `data,err:=json.Marshal(map[string]int{"n":42});if err!=nil{panic(err)};var out map[string]int;if err:=json.Unmarshal(data,&out);err!=nil{panic(err)};host.Emit("out",out)`},
	} {
		b.Run(work.name, func(b *testing.B) {
			source := "package main;import \"encoding/json\";func main(){" + work.body + "}"
			b.Run("ColdPrepareAndRun", func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					p, e := PrepareSource(source)
					if e != nil {
						b.Fatal(e)
					}
					if _, e = p.Run(context.Background(), RunOptions{}); e != nil {
						b.Fatal(e)
					}
				}
			})
			b.Run("WarmFreshRun", func(b *testing.B) {
				p, e := PrepareSource(source)
				if e != nil {
					b.Fatal(e)
				}
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, e := p.Run(context.Background(), RunOptions{}); e != nil {
						b.Fatal(e)
					}
				}
			})
		})
	}
}
