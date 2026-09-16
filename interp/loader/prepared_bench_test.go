package loader

import (
	"context"
	"fmt"
	"testing"

	"simonwaldherr.de/go/nanogo/interp"
)

// These benchmarks measure the selected Go target (native by default).
// scripts/benchmark-wasm.cjs measures worker startup and execution against
// the actual shipping WASM artifact separately.
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

// An untouched workspace asset should not be copied into each fresh run.
func BenchmarkPreparedWorkspace(b *testing.B) {
	for _, size := range []int{0, 1 << 20, 16 << 20} {
		b.Run(fmt.Sprintf("AssetBytes=%d", size), func(b *testing.B) {
			fs := interp.NewVFS()
			for name, data := range map[string][]byte{
				"/tmp/main.go":   []byte(`package main;func main(){host.Emit("answer",42)}`),
				"/tmp/asset.bin": make([]byte, size),
			} {
				if err := fs.WriteFile(name, data, 0644); err != nil {
					b.Fatal(err)
				}
			}
			p, err := PrepareModule(fs, "/tmp", Options{ModulePath: "bench.local/app"})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r, err := p.Run(context.Background(), RunOptions{})
				if err != nil || !r.Results.Committed {
					b.Fatalf("run: %+v, %v", r, err)
				}
			}
		})
	}
}
