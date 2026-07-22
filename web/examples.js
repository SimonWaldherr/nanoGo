/* global window */
window.EXAMPLES = {
  "Basics": `package main

import "fmt"

func main() {
  fmt.Println("Hello from nanoGo!")
}
`,
  "Canvas": `package main

import (
  "browser"
  "fmt"
)

func main() {
  fmt.Println("Canvas demo")
  w, h := 24, 12
  browser.CanvasSize(w, h)
  for y := 0; y < h; y++ {
    for x := 0; x < w; x++ {
      browser.CanvasSet(x, y, (x+y)%2 == 0)
    }
  }
  browser.CanvasFlush()
}
`,
  "Channels": `package main

import "fmt"

func worker(id int, jobs chan int, results chan int, done chan struct{}) {
  for j := range jobs {
    results <- j * j
    fmt.Printf("worker %d processed %d", id, j)
  }
  close(done)
}

func main() {
  fmt.Println("Channels demo")
  jobs := make(chan int, 3)
  results := make(chan int, 3)
  done := make(chan struct{})
  go worker(1, jobs, results, done)
  for i := 1; i <= 3; i++ { jobs <- i }
  close(jobs)
  <-done
  close(results)
  for r := range results {
    fmt.Println("result:", r)
  }
}
`,
  "WaitGroup": `package main

import (
  "fmt"
  "sync"
  "time"
)

func worker(id int, wg *sync.WaitGroup) {
  defer wg.Done()
  time.Sleep(200)
  fmt.Println("worker", id, "done")
}

func main() {
  fmt.Println("Concurrency demo (WaitGroup)")
  var wg sync.WaitGroup
  for i := 1; i <= 3; i++ {
    wg.Add(1)
    go worker(i, &wg)
  }
  wg.Wait()
  fmt.Println("all workers finished")
}
`,
  "Regexp + JSON": `package main

import (
  "fmt"
  "regexp"
  json "encoding/json"
)

func main() {
  fmt.Println("Regexp + JSON demo")
  rx, _ := regexp.Compile("h(.*)o")
  m := rx.FindStringSubmatch("hello")
  fmt.Println("submatch:", m)
  obj := map[string]any{"x": 1, "s": "hi"}
  b, _ := json.Marshal(obj)
  fmt.Println("json:", b)
}
`,
  "Strings/Sort": `package main

import (
  "fmt"
  "strings"
  "sort"
)

func main() {
  ss := strings.Split("go,wasm,interp,fun", ",")
  fmt.Println("split:", ss)
  fmt.Println("join:", strings.Join(ss, " | "))

  xs := []int{5,2,9,1,3}
  sort.Ints(xs)
  fmt.Println("sorted:", xs)
}
`,
  "Path + UTF-8": `package main

import (
  "fmt"
  "path"
  "unicode/utf8"
)

func main() {
  endpoint := path.Join("/api", "v1", "../users.json")
  fmt.Println("clean endpoint:", endpoint)
  fmt.Println("file:", path.Base(endpoint), "extension:", path.Ext(endpoint))

  label := "Go ✓"
  fmt.Println("runes:", utf8.RuneCountInString(label))
  fmt.Println("valid UTF-8:", utf8.ValidString(label))
}
`,
  "Template + DOM": `package main

import (
  "browser"
  "fmt"
  "text/template"
)

func main() {
  data := map[string]any{
    "Title": "Template Render",
    "Body": "Rendered with text/template package",
    "Items": []any{"one","two","three"},
  }
  out, _ := template.RenderString("<h3>{{.Title}}</h3><p>{{.Body}}</p><ul>{{range .Items}}<li>{{.}}</li>{{end}}</ul>", data)
  browser.SetHTML("output", out)
  fmt.Println("Template rendered → #output")
}
`,
  "Random": `package main

import (
  "fmt"
  "math/rand"
)

func main() {
  rand.Seed(1234)
  fmt.Println("Random demo: Intn(100) * 5")
  for i := 0; i < 5; i++ {
    fmt.Println(rand.Intn(100))
  }
}
`,
  "Sleep": `package main

import (
  "fmt"
  "time"
)

func main() {
  fmt.Println("sleep demo start")
  time.Sleep(250) // 250ms
  fmt.Println("sleep demo end")
}
`,
  "Life": `package main

import (
  "browser"
  "fmt"
  "time"
)

func lifeStep(g [][]int) [][]int {
  h := len(g)
  w := len(g[0])
  nxt := make([][]int, h)
  for y := 0; y < h; y++ {
    row := make([]int, w)
    for x := 0; x < w; x++ {
      n := 0
      for dy := -1; dy <= 1; dy++ {
        for dx := -1; dx <= 1; dx++ {
          if dx == 0 && dy == 0 { continue }
          yy := (y+dy+h)%h; xx := (x+dx+w)%w
          if g[yy][xx] == 1 { n++ }
        }
      }
      if g[y][x] == 1 { if n < 2 || n > 3 { row[x] = 0 } else { row[x] = 1 } } else { if n == 3 { row[x] = 1 } }
    }
    nxt[y] = row
  }
  return nxt
}

func main() {
  fmt.Println("Game of Life")
  // Keep this intentionally small: the playground also runs a deterministic
  // evaluator step limit, so a compact grid leaves room for animation.
  w, h := 24, 16
  browser.CanvasSize(w, h)
  grid := make([][]int, h)
  for y := 0; y < h; y++ {
    row := make([]int, w)
    for x := 0; x < w; x++ { if (x*y + y) % 7 == 0 { row[x] = 1 } }
    grid[y] = row
  }
  for i := 0; i < 20; i++ {
    for y := 0; y < h; y++ {
      for x := 0; x < w; x++ {
        browser.CanvasSet(x, y, grid[y][x] == 1)
      }
    }
    browser.CanvasFlush()
    grid = lifeStep(grid)
    time.Sleep(20)
  }
  fmt.Println("done")
}
`,
  "HTTP + Storage": `package main

import (
  "browser"
  "fmt"
  "http"
  "storage"
  "strings"
)

func main() {
  // Fetch a small text (same-origin or CORS allowed)
  txt := http.GetText("https://jsonplaceholder.typicode.com/todos/1")
  fmt.Println("fetched len:", len(txt))

  // Store in nanoGo's worker-session storage facade.
  storage.SetItem("lastFetchLen", fmt.Sprintf("%d", len(txt)))
  v := storage.GetItem("lastFetchLen")
  fmt.Println("stored lastFetchLen:", v)

  // Show first 60 chars into DOM
  snip := strings.TrimSpace(txt)
  if len(snip) > 60 { snip = snip[:60] + "..." }
  browser.SetHTML("output", "<pre>"+snip+"</pre>")
  fmt.Println("Wrote snippet to #output")
}
`,
  "FizzBuzz": `package main

import "fmt"

func main() {
  for i := 1; i <= 30; i++ {
    if i%15 == 0 { 
      fmt.Println("FizzBuzz") 
    } else if i%3 == 0 { 
      fmt.Println("Fizz") 
    } else if i%5 == 0 {
      fmt.Println("Buzz") 
    } else { 
      fmt.Println(i) 
    }
  }
}
`,
  "Fibonacci": `package main

import "fmt"

func fib(n int) int {
  if n < 2 { return n }
  return fib(n-1) + fib(n-2)
}

func main() {
  fmt.Println("Fibonacci numbers:")
  for i := 0; i < 10; i++ { fmt.Println(i, fib(i)) }
}
`,
  "Prime Sieve": `package main

import "fmt"

func sieve(n int) []int {
  // Use int flags to avoid potential issues with boolean literals in the runtime
  isPrime := make([]int, n+1)
  for i := 2; i <= n; i++ {
    isPrime[i] = 1
  }
  for p := 2; p*p <= n; p++ {
    if isPrime[p] != 0 {
      for multiple := p*p; multiple <= n; multiple += p {
        isPrime[multiple] = 0
      }
    }
  }
  primes := []int{}
  for i := 2; i <= n; i++ {
    if isPrime[i] != 0 {
      primes = append(primes, i)
    }
  }
  return primes
}

func main() {
  fmt.Println("Primes up to 100:")
  fmt.Println(sieve(100))
}
`,
  "Checkerboard": `package main

import (
  "browser"
  "fmt"
)

func main() {
  fmt.Println("Checkerboard canvas")
  w, h := 32, 16
  browser.CanvasSize(w, h)
  for y := 0; y < h; y++ {
    for x := 0; x < w; x++ {
      browser.CanvasSet(x, y, (x+y)%2==0)
    }
  }
  browser.CanvasFlush()
}
`,
  "Bouncing Ball": `package main

import (
  "browser"
  "fmt"
  "time"
)

func main() {
  fmt.Println("Bouncing ball demo")
  w, h := 48, 30
  browser.CanvasSize(w, h)
  x, y := 0, 0
  dx, dy := 1, 1
  for i := 0; i < 72; i++ {
    // clear
    for yy := 0; yy < h; yy++ { for xx := 0; xx < w; xx++ { browser.CanvasSet(xx, yy, 0==1) } }
    browser.CanvasSet(x, y, 1==1)
    browser.CanvasFlush()
    x += dx; y += dy
    if x <= 0 || x >= w-1 { dx = -dx }
    if y <= 0 || y >= h-1 { dy = -dy }
    time.Sleep(20)
  }
  fmt.Println("done")
}
`,"HTTP Fetch JSON": `package main

import (
  "fmt"
  "http"
  "strings"
)

func main() {
  fmt.Println("Fetching a JSON document (first 120 chars):")
  txt := http.GetText("https://jsonplaceholder.typicode.com/todos/1")
  if len(txt) == 0 {
    fmt.Println("fetch failed or CORS blocked")
    return
  }
  sn := strings.TrimSpace(txt)
  if len(sn) > 120 { sn = sn[:120] + "..." }
  fmt.Println(sn)
}
`,"Pipeline": `package main

import "fmt"

func producer(n int, out chan int) {
  for i := 1; i <= n; i++ { out <- i }
  close(out)
}

func squarer(in chan int, out chan int) {
  for v := range in { out <- v * v }
  close(out)
}

func main() {
  fmt.Println("Pipeline demo")
  a := make(chan int)
  b := make(chan int)
  go producer(5, a)
  go squarer(a, b)
  for v := range b { fmt.Println(v) }
}
`,"Structs & Methods": `package main

import "fmt"

type Point struct{ X, Y int }

func (p Point) String() string { return fmt.Sprintf("(%d,%d)", p.X, p.Y) }

func (p *Point) Move(dx, dy int) { p.X += dx; p.Y += dy }

func main() {
  p := Point{2,3}
  fmt.Println("start", p)
  p.Move(1, -1)
  fmt.Println("moved", p)
}
`,"Maps & Ranges": `package main

import "fmt"

func main() {
  m := map[string]int{"a":1, "b":2, "c":3}
  fmt.Println("map size:", len(m))
  for k, v := range m {
    fmt.Println(k, v)
  }
}
`,"Timer Ticker": `package main

import (
  "fmt"
  "time"
)

func main() {
  fmt.Println("Timer/Ticker demo (short)")
  t := time.NewTimer(200)
  <-t.C
  fmt.Println("Timer fired")
  tick := time.NewTicker(100)
  for i := 0; i < 3; i++ { <-tick.C; fmt.Println("tick", i) }
  tick.Stop()
  fmt.Println("done")
}
`,"JSON Roundtrip": `package main

import (
  "fmt"
  json "encoding/json"
)

func main() {
  obj := map[string]any{"name":"nanoGo","v":1}
  b, _ := json.Marshal(obj)
  fmt.Println("json:", string(b))
  // nanoGo's JSON facade returns the decoded value instead of filling a
  // pointer like encoding/json.Unmarshal in the Go standard library.
  decoded := json.Unmarshal(b)
  fmt.Println("unmarshalled:", decoded)
}
`,"Virtual FS (os)": `package main

import (
  "fmt"
  "os"
)

func main() {
  // Write files to the virtual filesystem
  err := os.WriteFile("/tmp/hello.txt", "Hello from nanoGo VFS!", 0644)
  if err != nil {
    fmt.Println("write error:", err)
    return
  }
  fmt.Println("Wrote /tmp/hello.txt")

  // Read it back
  content, err := os.ReadFile("/tmp/hello.txt")
  if err != nil {
    fmt.Println("read error:", err)
    return
  }
  fmt.Println("Content:", content)

  // Write a second file
  _ = os.WriteFile("/tmp/data.txt", "line one\\nline two\\n", 0644)

  // List /tmp directory
  entries, err := os.ReadDir("/tmp")
  if err != nil {
    fmt.Println("readdir error:", err)
    return
  }
  fmt.Println("Files in /tmp:")
  for _, e := range entries {
    fmt.Println(" -", e.Name)
  }

  // Environment variables
  fmt.Println("HOME:", os.Getenv("HOME"))
  fmt.Println("TMP:", os.TempDir())
}
`,"Closures": `package main

import "fmt"

// counter returns a function that increments and returns a counter.
func counter(start int) func() int {
  n := start
  return func() int {
    n++
    return n
  }
}

// adder returns a function that adds x to its argument.
func adder(x int) func(int) int {
  return func(y int) int { return x + y }
}

// accumulate returns a closure that sums all values passed to it.
func accumulate() func(int) int {
  total := 0
  return func(v int) int {
    total += v
    return total
  }
}

func main() {
  c1 := counter(0)
  c2 := counter(10)
  fmt.Println("c1:", c1(), c1(), c1()) // 1 2 3
  fmt.Println("c2:", c2(), c2())       // 11 12

  add5 := adder(5)
  fmt.Println("add5(3):", add5(3))   // 8
  fmt.Println("add5(10):", add5(10)) // 15

  acc := accumulate()
  fmt.Println("acc(1):", acc(1)) // 1
  fmt.Println("acc(4):", acc(4)) // 5
  fmt.Println("acc(5):", acc(5)) // 10
}
`,"Error Handling": `package main

import (
  "fmt"
  "strconv"
)

// safeDivide divides a by b, returning an error string on bad input.
func safeDivide(a, b float64) (float64, string) {
  if b == 0 {
    return 0, "division by zero"
  }
  return a / b, ""
}

func main() {
  result, errMsg := safeDivide(10, 3)
  if errMsg != "" {
    fmt.Println("Error:", errMsg)
  } else {
    fmt.Printf("10 / 3 = %.4f\n", result)
  }

  _, errMsg = safeDivide(5, 0)
  if errMsg != "" {
    fmt.Println("Expected error:", errMsg)
  }

  // strconv demonstrates idiomatic val, err pattern
  n, err := strconv.Atoi("42")
  if err != nil {
    fmt.Println("parse error:", err)
  } else {
    fmt.Println("Parsed int:", n)
  }

  _, err = strconv.Atoi("not-a-number")
  if err != nil {
    fmt.Println("Expected parse error for non-numeric string")
  }
}
`,"Math": `package main

import (
  "fmt"
  "math"
)

func main() {
  fmt.Println("Constants:")
  fmt.Printf("  Pi = %.6f\n", math.Pi)
  fmt.Printf("  E  = %.6f\n", math.E)

  fmt.Println("Functions:")
  fmt.Printf("  Sqrt(2)       = %.6f\n", math.Sqrt(2))
  fmt.Printf("  Pow(2, 10)    = %.0f\n",  math.Pow(2, 10))
  fmt.Printf("  Abs(-5.5)     = %.1f\n",  math.Abs(-5.5))
  fmt.Printf("  Floor(3.7)    = %.1f\n",  math.Floor(3.7))
  fmt.Printf("  Ceil(3.2)     = %.1f\n",  math.Ceil(3.2))
  fmt.Printf("  Round(3.5)    = %.1f\n",  math.Round(3.5))
  fmt.Printf("  Log(E)        = %.6f\n",  math.Log(math.E))
  fmt.Printf("  Log2(1024)    = %.1f\n",  math.Log2(1024))
  fmt.Printf("  Sin(Pi/2)     = %.6f\n",  math.Sin(math.Pi/2))
  fmt.Printf("  Cos(Pi)       = %.6f\n",  math.Cos(math.Pi))
  fmt.Printf("  Max(3.0, 7.0) = %.1f\n",  math.Max(3, 7))
  fmt.Printf("  Min(3.0, 7.0) = %.1f\n",  math.Min(3, 7))
}
`,"Strconv": `package main

import (
  "fmt"
  "strconv"
)

func main() {
  // int <-> string
  s := strconv.Itoa(42)
  fmt.Println("Itoa(42):", s)

  n, err := strconv.Atoi("123")
  if err != nil {
    fmt.Println("Atoi error:", err)
  } else {
  fmt.Println("Atoi 123:", n)
  }

  // float formatting
  f := strconv.FormatFloat(3.14159, 'f', 3, 64)
  fmt.Println("FormatFloat(3.14159, 'f', 3):", f)

  // bool <-> string
  fmt.Println("FormatBool(true):", strconv.FormatBool(true))
  b, err := strconv.ParseBool("false")
  if err != nil {
    fmt.Println("ParseBool error:", err)
  } else {
    fmt.Println("ParseBool false:", b)
  }

  // int with base
  hex := strconv.FormatInt(255, 16)
  fmt.Println("FormatInt(255, 16):", hex) // ff

  bin := strconv.FormatInt(10, 2)
  fmt.Println("FormatInt(10, 2):", bin) // 1010
}
`,"Test: Table-driven": `package main

import (
  "fmt"
  "testing"
)

func clamp(n, low, high int) int {
  if n < low { return low }
  if n > high { return high }
  return n
}

type clampCase struct {
  name string
  in int
  want int
}

// This uses nanoGo's supported testing subset. In a package, the loader
// discovers supported TestXxx functions; the playground calls it explicitly
// so it can run from one editable file.
func TestClamp(t *testing.T) {
  cases := []clampCase{
    clampCase{"below range", -2, 0},
    clampCase{"inside range", 4, 4},
    clampCase{"above range", 12, 10},
  }

  for _, tc := range cases {
    tc := tc
    t.Run(tc.name, func(t *testing.T) {
      if got := clamp(tc.in, 0, 10); got != tc.want {
        t.Errorf("clamp(%d) = %d, want %d", tc.in, got, tc.want)
      }
    })
  }
}

func main() {
  var t testing.T
  TestClamp(&t)
  fmt.Println("PASS: TestClamp (3 subtests)")
  fmt.Println("Try changing an expected value to see a failing assertion.")
}
`,"Benchmark: Checksum": `package main

import (
  "fmt"
  "testing"
  "time"
)

func checksum(s string) int {
  total := 0
  for _, r := range s { total += int(r) }
  return total
}

// Same function signature as a regular go test benchmark. The iteration
// count is fixed in the playground so runs stay short and comparable.
func BenchmarkChecksum(b *testing.B) {
  input := "nanoGo makes Go experiments small and repeatable"
  guard := 0
  b.ResetTimer()
  for i := 0; i < b.N; i++ { guard += checksum(input) }
  b.StopTimer()
  fmt.Println("checksum guard:", guard)
}

func main() {
  b := testing.B{N: 400}
  started := time.Now()
  BenchmarkChecksum(&b)
  fmt.Printf("BenchmarkChecksum: N=%d, wall=%dms\\n", b.N, time.Since(started))
  fmt.Println("The loader also reports deterministic interpreter steps/op.")
}
`,"Worker Pool": `package main

import (
  "fmt"
  "sync"
)

func worker(id int, jobs <-chan int, results chan<- int, wg *sync.WaitGroup) {
  defer wg.Done()
  for job := range jobs {
    results <- job * job
    fmt.Printf("worker %d finished job %d\\n", id, job)
  }
}

func main() {
  jobs := make(chan int, 4)
  results := make(chan int, 4)
  var wg sync.WaitGroup

  for id := 1; id <= 2; id++ {
    wg.Add(1)
    go worker(id, jobs, results, &wg)
  }
  for job := 1; job <= 4; job++ { jobs <- job }
  close(jobs)
  wg.Wait()
  close(results)

  total := 0
  for result := range results { total += result }
  fmt.Println("result total:", total)
}
`};
