/* global window */
window.EXAMPLES = {
  "Basics": `package main

import "fmt"

func main() {
  fmt.Println("Hello from nanoGo!")
}
`,
  "Canvas": `package main

import "fmt"

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
  "Template + DOM": `package main

import (
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
  w, h := 64, 40
  browser.CanvasSize(w, h)
  grid := make([][]int, h)
  for y := 0; y < h; y++ {
    row := make([]int, w)
    for x := 0; x < w; x++ { if (x*y + y) % 7 == 0 { row[x] = 1 } }
    grid[y] = row
  }
  for i := 0; i < 200; i++ {
    for y := 0; y < h; y++ {
      for x := 0; x < w; x++ {
        browser.CanvasSet(x, y, grid[y][x] == 1)
      }
    }
    browser.CanvasFlush()
    grid = lifeStep(grid)
    time.Sleep(30)
  }
  fmt.Println("done")
}
`,
  "HTTP + Storage": `package main

import (
  "fmt"
  "strings"
)

func main() {
  // Fetch a small text (same-origin or CORS allowed)
  txt := http.GetText("examples.js")
  fmt.Println("fetched len:", len(txt))

  // Store in localStorage
  storage.SetItem("lastFetchLen", fmt.Sprintf("%d", len(txt)))
  v := storage.GetItem("lastFetchLen")
  fmt.Println("localStorage lastFetchLen:", v)

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

import "fmt"

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
  "fmt"
  "time"
)

func main() {
  fmt.Println("Bouncing ball demo")
  w, h := 64, 40
  browser.CanvasSize(w, h)
  x, y := 0, 0
  dx, dy := 1, 1
  for i := 0; i < 200; i++ {
    // clear
    for yy := 0; yy < h; yy++ { for xx := 0; xx < w; xx++ { browser.CanvasSet(xx, yy, 0==1) } }
    browser.CanvasSet(x, y, 1==1)
    browser.CanvasFlush()
    x += dx; y += dy
    if x <= 0 || x >= w-1 { dx = -dx }
    if y <= 0 || y >= h-1 { dy = -dy }
    time.Sleep(30)
  }
  fmt.Println("done")
}
`,"HTTP Fetch JSON": `package main

import (
  "fmt"
  "strings"
)

func main() {
  fmt.Println("Fetching examples.js (first 120 chars):")
  txt := http.GetText("examples.js")
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
  var m map[string]any
  _ = json.Unmarshal(b, &m)
  fmt.Println("unmarshalled:", m["name"], m["v"])
}
`};