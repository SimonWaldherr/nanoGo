package interp

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func packedLifeSource(tb testing.TB) string {
	tb.Helper()
	source, err := os.ReadFile("../samples/game_of_life/main.go")
	if err != nil {
		tb.Fatal(err)
	}
	return string(source[:strings.Index(string(source), "func main()")])
}

func lifeReference(cells []int, w, h int) []int {
	next := make([]int, len(cells))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if dx != 0 || dy != 0 {
						n += cells[((y+dy+h)%h)*w+(x+dx+w)%w]
					}
				}
			}
			if n == 3 || n == 2 && cells[y*w+x] == 1 {
				next[y*w+x] = 1
			}
		}
	}
	return next
}

func TestPackedLife(t *testing.T) {
	core := packedLifeSource(t)
	for _, bits := range []int{16, 32} {
		for _, shape := range [][2]int{{1, 1}, {1, 2}, {1, 9}, {2, 7}, {3, 17}} {
			for _, pattern := range []string{"empty", "full", "random", "glider", "blinker"} {
				words, h := shape[0], shape[1]
				w := words * bits
				t.Run(fmt.Sprintf("%d/%dx%d/%s", bits, w, h, pattern), func(t *testing.T) {
					cells := make([]int, w*h)
					for y := 0; y < h; y++ {
						for x := 0; x < w; x++ {
							if pattern == "full" || pattern == "random" && (x*17+y*31+x*y)%7 < 3 {
								cells[y*w+x] = 1
							}
						}
					}
					if pattern == "glider" {
						for _, p := range [][2]int{{0, -1}, {1, 0}, {-1, 1}, {0, 1}, {1, 1}} {
							cells[((p[1]+h)%h)*w+(p[0]+w)%w] = 1
						}
					}
					if pattern == "blinker" {
						for _, x := range []int{w - 1, 0, 1} {
							cells[x] = 1
						}
					}
					data := make([]any, words*h)
					for y := 0; y < h; y++ {
						for x := 0; x < words; x++ {
							value := 0
							for bit := 0; bit < bits; bit++ {
								value |= cells[y*w+x*bits+bit] << bit
							}
							data[y*words+x] = value
						}
					}
					vm, _ := newTestVM()
					vm.RegisterNative("seed", func([]any) (any, error) { return &SliceVal{ElementType: "int", Data: data}, nil })
					generation := 0
					vm.RegisterNative("check", func(args []any) (any, error) {
						got := args[0].(*SliceVal)
						cells = lifeReference(cells, w, h)
						generation++
						for y := 0; y < h; y++ {
							for x := 0; x < w; x++ {
								alive := (ToInt(got.Data[y*words+x/bits]) >> (x % bits)) & 1
								if alive != cells[y*w+x] {
									return nil, fmt.Errorf("generation %d cell (%d,%d): got %d want %d", generation, x, y, alive, cells[y*w+x])
								}
							}
						}
						return nil, nil
					})
					source := core + fmt.Sprintf(`func main(){ grid:=seed(); next:=make([]int,%d); for generation:=0;generation<20;generation++ {lifeStep(grid,next,%d,%d,%d); grid,next=next,grid;check(grid)} }`, words*h, words, h, bits)
					if err := vm.Run(source); err != nil {
						t.Fatal(err)
					}
					if generation != 20 {
						t.Fatalf("checked %d generations", generation)
					}
				})
			}
		}
	}
}

const flatLifeSource = `package main
func lifeStep(src []int,dst []int,w int,h int) {
 for y:=0;y<h;y++ {
  row:=y*w; above:=row-w;below:=row+w
  if y==0 {above=(h-1)*w};if y==h-1 {below=0}
  for x:=0;x<w;x++ {
   left:=x-1;right:=x+1
   if x==0 {left=w-1};if x==w-1 {right=0}
   n:=src[above+left]+src[above+x]+src[above+right]+src[row+left]+src[row+right]+src[below+left]+src[below+x]+src[below+right]
   dst[row+x]=0
   if n==3 || n==2 && src[row+x]==1 {dst[row+x]=1}
  }
 }
}
`

func BenchmarkLifeVariants(b *testing.B) {
	// Identical 96x48 toroidal board, seed and generation count. No rendering.
	for _, variant := range []string{"Naive", "Flat", "Packed16", "Packed32", "Packed32Unscoped"} {
		b.Run(variant, func(b *testing.B) {
			const w, h, generations = 96, 48, 20
			var source string
			if variant == "Naive" {
				source = naiveLifeSource + fmt.Sprintf(`func main(){ grid:=make([][]int,%d);for y:=0;y<%d;y++ {row:=make([]int,%d);for x:=0;x<%d;x++ {if (x*17+y*31+x*y)%%7<3 {row[x]=1}};grid[y]=row};for g:=0;g<%d;g++ {grid=lifeStep(grid)} }`, h, h, w, w, generations)
			} else if variant == "Flat" {
				source = flatLifeSource + fmt.Sprintf(`func main(){grid:=make([]int,%d);next:=make([]int,%d);for y:=0;y<%d;y++ {for x:=0;x<%d;x++ {if (x*17+y*31+x*y)%%7<3 {grid[y*%d+x]=1}}};for g:=0;g<%d;g++ {lifeStep(grid,next,%d,%d);grid,next=next,grid} }`, w*h, w*h, h, w, w, generations, w, h)
			} else {
				bits := 32
				if variant == "Packed16" {
					bits = 16
				}
				words := w / bits
				source = packedLifeSource(b) + fmt.Sprintf(`func main(){grid:=make([]int,%d);next:=make([]int,%d);for y:=0;y<%d;y++ {for x:=0;x<%d;x++ {if (x*17+y*31+x*y)%%7<3 {grid[y*%d+x/%d]|=1<<(x%%%d)}}};for g:=0;g<%d;g++ {lifeStep(grid,next,%d,%d,%d);grid,next=next,grid} }`, words*h, words*h, h, w, words, bits, bits, generations, words, h, bits)
			}
			if variant == "Packed32Unscoped" {
				source = strings.Replace(source, packedLifeSource(b), unscopedLifeSource, 1)
			}
			vm, _ := newTestVM()
			vm.Limits.MaxSteps = 1_000_000_000
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := vm.Run(source); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(vm.LastStepCount()), "steps/op")
		})
	}
}

func TestLifeWebKernelMatchesSample(t *testing.T) {
	source := renderExampleTemplate(webExamples(t)["Life"])
	start := strings.Index(source, "func lifeStep(")
	end := strings.Index(source, "// Update only cells")
	sample := packedLifeSource(t)
	if start < 0 || end < start || strings.TrimSpace(source[start:end]) != strings.TrimSpace(sample[strings.Index(sample, "func lifeStep("):]) {
		t.Fatal("browser Life kernel differs from the tested sample")
	}
}

const naiveLifeSource = `package main
func lifeStep(g [][]int) [][]int {
	h := len(g)
	w := len(g[0])
	next := make([][]int, h)
	for y := 0; y < h; y++ {
		row := make([]int, w)
		for x := 0; x < w; x++ {
			n := 0
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					if dx == 0 && dy == 0 { continue }
					yy := (y+dy+h)%h
					xx := (x+dx+w)%w
					if g[yy][xx] == 1 { n++ }
				}
			}
			if g[y][x] == 1 {
				if n == 2 || n == 3 { row[x] = 1 }
			} else if n == 3 {
				row[x] = 1
			}
		}
		next[y] = row
	}
	return next
}
`

const unscopedLifeSource = `package main
func lifeStep(src []int, dst []int, words int, height int, bits int) {
	shift := bits - 1
	high := 1 << shift
	half := high - 1
	mask := half | high
	for y := 0; y < height; y++ {
		row := y * words
		above := row - words
		below := row + words
		if y == 0 {
			above = (height - 1) * words
		}
		if y == height-1 {
			below = 0
		}
		for x := 0; x < words; x++ {
			left := x - 1
			right := x + 1
			if x == 0 {
				left = words - 1
			}
			if x == words-1 {
				right = 0
			}
			up := src[above+x]
			mid := src[row+x]
			down := src[below+x]
			// Mask right shifts explicitly: nanoGo int is 32-bit in WebAssembly.
			a := (up << 1) | ((src[above+left] >> shift) & 1)
			b := ((up >> 1) & half) | ((src[above+right] & 1) << shift)
			c := (down << 1) | ((src[below+left] >> shift) & 1)
			d := ((down >> 1) & half) | ((src[below+right] & 1) << shift)
			e := (mid << 1) | ((src[row+left] >> shift) & 1)
			f := ((mid >> 1) & half) | ((src[row+right] & 1) << shift)
			// Carry-save addition: each bit position is an independent cell.
			ab := a ^ b
			loA := ab ^ up
			hiA := (a & b) | (ab & up)
			cd := c ^ d
			loB := cd ^ down
			hiB := (c & d) | (cd & down)
			loC := e ^ f
			hiC := e & f
			low := loA ^ loB
			ones := low ^ loC
			carry := (loA & loB) | (low & loC)
			h1 := hiA ^ hiB
			h2 := hiC ^ carry
			twos := h1 ^ h2
			fours := (hiA & hiB) | (hiC & carry) | (h1 & h2)
			// Birth at three, survival at two or three. Eight has twos == 0.
			dst[row+x] = twos &^ fours & (ones | mid) & mask
		}
	}
}

`

func TestLifeChangedCellRendering(t *testing.T) {
	source := renderExampleTemplate(webExamples(t)["Life"])
	source = source[:strings.Index(source, "func main()")] + `func main(){
 grid:=[]int{1 | (1<<31),0,0,0};drawn:=make([]int,4)
 drawLife(grid,drawn,2)
 grid[0]=1<<31;grid[3]=1<<31
 drawLife(grid,drawn,2)
 drawLife(grid,drawn,2)
 }`
	vm, _ := newWebExampleVM()
	var changes []string
	flushes := 0
	vm.RegisterNative("CanvasSet", func(args []any) (any, error) {
		changes = append(changes, fmt.Sprint(args))
		return nil, nil
	})
	vm.RegisterNative("CanvasFlush", func([]any) (any, error) { flushes++; return nil, nil })
	if err := vm.Run(source); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(changes, ";"); got != "[0 0 true];[31 0 true];[0 0 false];[63 1 true]" {
		t.Fatalf("canvas updates=%s", got)
	}
	if flushes != 3 {
		t.Fatalf("flushes=%d", flushes)
	}
}
