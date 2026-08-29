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
  "Template Report": `package main

import (
  "fmt"
  "text/template"
)

// Types used as template data must be declared at package level, and slice
// elements spell out their type: []Row{Row{...}} rather than []Row{{...}}.
type Row struct {
  Name  string
  Qty   int
  Price int
}

type Report struct {
  Title string
  Rows  []Row
}

// One template text, executed once per row. nanoGo caches the parsed form,
// so the loop parses it only the first time round.
const line = "{{printf \\"%-8s\\" .Name}} {{printf \\"%4d\\" .Qty}} @ {{printf \\"%3d\\" .Price}}{{if eq .Qty 0}}  << out of stock{{end}}"

const summary = "{{.Title}}: {{len .Rows}} article(s){{range $i, $r := .Rows}}\\n  {{$i}}. {{$r.Name}}{{end}}"

func main() {
  rows := []Row{
    Row{Name: "bolt", Qty: 12, Price: 30},
    Row{Name: "nut", Qty: 0, Price: 10},
    Row{Name: "washer", Qty: 240, Price: 2},
  }

  total := 0
  for i := 0; i < len(rows); i++ {
    out, err := template.RenderString(line, rows[i])
    if err != nil {
      fmt.Println("render error:", err)
      return
    }
    fmt.Println(out)
    total = total + rows[i].Qty*rows[i].Price
  }

  out, _ := template.RenderString(summary, Report{Title: "Warehouse", Rows: rows})
  fmt.Println(out)

  // if-init scopes its own variables, so this err shadows the one above.
  if _, err := template.RenderString("{{.Unclosed", rows); err != nil {
    fmt.Println("malformed template rejected as expected")
  }

  fmt.Println("stock value:", total)
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
  "math/rand"
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

func newLifeGrid(w int, h int) [][]int {
  grid := make([][]int, h)
  for y := 0; y < h; y++ {
    row := make([]int, w)
    for x := 0; x < w; x++ {
      // A moderate density makes the first generations interesting without
      // immediately filling the whole board.
      if rand.Intn(100) < 29 { row[x] = 1 }
    }
    grid[y] = row
  }
  return grid
}

func main() {
  fmt.Println("Game of Life — 100 generations per random seed")
  // This compact field keeps two 100-generation rounds inside the
  // playground's deterministic evaluator step limit while still animating.
  w, h := 12, 8
  browser.CanvasSize(w, h)
  for round := 1; round <= 2; round++ {
    // The millisecond clock ensures that the second 100-generation round
    // starts from a genuinely new random seed.
    seed := time.Now() + round
    rand.Seed(seed)
    fmt.Println("round", round, "seed", seed)
    grid := newLifeGrid(w, h)
    for generation := 0; generation < 100; generation++ {
      for y := 0; y < h; y++ {
        for x := 0; x < w; x++ {
          browser.CanvasSet(x, y, grid[y][x] == 1)
        }
      }
      browser.CanvasFlush()
      grid = lifeStep(grid)
      time.Sleep(30)
    }
    fmt.Println("round", round, "complete")
  }
  fmt.Println("done")
}
`,
  "HTTP Client + Storage": `package main

import (
  "browser"
  "fmt"
  "http"
  "storage"
  "strings"
)

func main() {
  // Fetch a small JSON document. Public APIs must explicitly allow CORS.
  txt, err := http.GetText("https://jsonplaceholder.typicode.com/todos/1")
  if err != nil {
    fmt.Println("GET failed:", err)
    return
  }
  fmt.Println("GET status: success, bytes:", len(txt))

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
`,
  "Plasma Waves": `package main

import (
  "browser"
  "fmt"
  "time"
)

func main() {
  fmt.Println("Plasma waves — 48 animated frames")
  w, h := 48, 24
  browser.CanvasSize(w, h)
  for t := 0; t < 48; t++ {
    for y := 0; y < h; y++ {
      for x := 0; x < w; x++ {
        wave := (x*x + y*y + x*y + t*3) % 17
        browser.CanvasSet(x, y, wave < 8)
      }
    }
    browser.CanvasFlush()
    time.Sleep(28)
  }
  fmt.Println("done")
}
`,
  "Starfield": `package main

import (
  "browser"
  "fmt"
  "time"
)

func main() {
  fmt.Println("Starfield — deterministic particle motion")
  w, h := 48, 24
  browser.CanvasSize(w, h)
  for t := 0; t < 56; t++ {
    for y := 0; y < h; y++ {
      for x := 0; x < w; x++ { browser.CanvasSet(x, y, 0 == 1) }
    }
    for star := 0; star < 90; star++ {
      x := (star*11 + t*(star%4+1)) % w
      y := (star*star + t*(star%3+1)) % h
      browser.CanvasSet(x, y, 1 == 1)
    }
    browser.CanvasFlush()
    time.Sleep(24)
  }
  fmt.Println("done")
}
`,
  "Scanner": `package main

import (
  "browser"
  "fmt"
  "time"
)

func main() {
  fmt.Println("Scanner — 144 sweeping frames with pulsing targets")
  w, h := 48, 24
  browser.CanvasSize(w, h)
  for t := 0; t < 144; t++ {
    for y := 0; y < h; y++ {
      for x := 0; x < w; x++ { browser.CanvasSet(x, y, 0 == 1) }
    }
    scan := t % w
    for y := 0; y < h; y++ { browser.CanvasSet(scan, y, 1 == 1) }
    for target := 0; target < 12; target++ {
      x := (target*7 + 3) % w
      y := (target*11 + 5) % h
      if (t+target*3)%w < 5 { browser.CanvasSet(x, y, 1 == 1) }
    }
    browser.CanvasFlush()
    time.Sleep(22)
  }
  fmt.Println("done")
}
`,
  "Mandelbrot Set": `package main

import (
  "browser"
  "fmt"
)

func escapeSteps(cx float64, cy float64) int {
  zx, zy := 0.0, 0.0
  for step := 0; step < 48; step++ {
    xx := zx*zx - zy*zy + cx
    zy = 2*zx*zy + cy
    zx = xx
    if zx*zx+zy*zy > 4 { return step }
  }
  return 48
}

func main() {
  fmt.Println("Mandelbrot set — 80 × 48 palette samples")
  w, h := 80, 48
  browser.CanvasSize(w, h)
  for y := 0; y < h; y++ {
    cy := float64(y)*2.4/float64(h) - 1.2
    for x := 0; x < w; x++ {
      cx := float64(x)*3.2/float64(w) - 2.2
      depth := escapeSteps(cx, cy)
      level := 0
      if depth < 48 { level = 2 + depth%6 }
      browser.CanvasSetLevel(x, y, level)
    }
  }
  browser.CanvasFlush()
  fmt.Println("done")
}
`,
  "Langton's Ant": `package main

import (
  "browser"
  "fmt"
  "time"
)

func paint(grid [][]int) {
  for y := 0; y < len(grid); y++ {
    for x := 0; x < len(grid[y]); x++ {
      browser.CanvasSet(x, y, grid[y][x] == 1)
    }
  }
  browser.CanvasFlush()
}

func main() {
  w, h := 30, 18
  grid := make([][]int, h)
  for y := 0; y < h; y++ { grid[y] = make([]int, w) }
  x, y, dir := w/2, h/2, 0
  fmt.Println("Langton's ant — 480 rule-driven steps")
  browser.CanvasSize(w, h)
  for step := 0; step < 480; step++ {
    if grid[y][x] == 0 { dir = (dir + 1) % 4; grid[y][x] = 1 } else { dir = (dir + 3) % 4; grid[y][x] = 0 }
    if dir == 0 { x = (x + 1) % w }
    if dir == 1 { y = (y + 1) % h }
    if dir == 2 { x = (x - 1 + w) % w }
    if dir == 3 { y = (y - 1 + h) % h }
    if step%3 == 0 {
      paint(grid)
      browser.CanvasSet(x, y, true)
      browser.CanvasFlush()
      time.Sleep(24)
    }
  }
  fmt.Println("done")
}
`,
  "Sorting Visualizer": `package main

import (
  "browser"
  "fmt"
  "time"
)

func drawBars(values []int, h int) {
  for y := 0; y < h; y++ {
    for x := 0; x < len(values); x++ {
      browser.CanvasSet(x, h-1-y, values[x] > y)
    }
  }
  browser.CanvasFlush()
}

func main() {
  values := []int{5, 14, 2, 11, 8, 17, 4, 13, 1, 16, 7, 12, 3, 15, 6, 10, 9, 18}
  h := 18
  browser.CanvasSize(len(values), h)
  fmt.Println("Bubble sort — one visual frame per pass")
  for end := len(values)-1; end > 0; end-- {
    swapped := false
    for i := 0; i < end; i++ {
      if values[i] > values[i+1] {
        values[i], values[i+1] = values[i+1], values[i]
        swapped = true
      }
    }
    drawBars(values, h)
    time.Sleep(55)
    if !swapped { break }
  }
  drawBars(values, h)
  fmt.Println("sorted:", values)
}
`,
  "Lissajous Curve": `package main

import (
  "browser"
  "fmt"
  "math"
  "time"
)

func main() {
  w, h := 64, 36
  browser.CanvasSize(w, h)
  fmt.Println("Lissajous curve — 54 animated phases")
  for phase := 0; phase < 54; phase++ {
    for y := 0; y < h; y++ {
      for x := 0; x < w; x++ { browser.CanvasSetLevel(x, y, 0) }
    }
    for i := 0; i < 220; i++ {
      t := float64(i) * math.Pi * 2 / 220
      x := w/2 + int(math.Sin(3*t+float64(phase)*0.12)*float64(w/2-3))
      y := h/2 + int(math.Sin(2*t+float64(phase)*0.17)*float64(h/2-3))
      browser.CanvasSetLevel(x, y, 2+(i+phase)%6)
    }
    browser.CanvasFlush()
    time.Sleep(28)
  }
  fmt.Println("done")
}
`,
  "Fireworks": `package main

import (
  "browser"
  "fmt"
  "time"
)

func main() {
  w, h := 56, 32
  browser.CanvasSize(w, h)
  fmt.Println("Fireworks — deterministic particle burst")
  for frame := 0; frame < 48; frame++ {
    for y := 0; y < h; y++ {
      for x := 0; x < w; x++ { browser.CanvasSetLevel(x, y, 0) }
    }
    for particle := 0; particle < 56; particle++ {
      vx := particle%9 - 4
      vy := particle/9 - 3
      x := w/2 + vx*frame/4
      y := h/2 + vy*frame/5 + frame*frame/110
      if x >= 0 && x < w && y >= 0 && y < h {
        browser.CanvasSetLevel(x, y, 2+(particle+frame/4)%6)
        if frame < 22 { browser.CanvasSetLevel(w/2+vx*(frame-1)/4, h/2+vy*(frame-1)/5+(frame-1)*(frame-1)/110, 1) }
      }
    }
    browser.CanvasFlush()
    time.Sleep(30)
  }
  fmt.Println("done")
}
`,
  "Wave Interference": `package main

import (
  "browser"
  "fmt"
  "math"
  "time"
)

func main() {
  w, h := 48, 28
  browser.CanvasSize(w, h)
  fmt.Println("Wave interference — two moving sources")
  for frame := 0; frame < 30; frame++ {
    for y := 0; y < h; y++ {
      for x := 0; x < w; x++ {
        a := math.Sin(float64(x)*0.23 + float64(y)*0.17 + float64(frame)*0.16)
        b := math.Sin(float64(x)*0.11 - float64(y)*0.29 - float64(frame)*0.11)
        level := 2 + int((a+b+2)*1.25)
        browser.CanvasSetLevel(x, y, level)
      }
    }
    browser.CanvasFlush()
    time.Sleep(26)
  }
  fmt.Println("done")
}
`,
  "Pathfinding Wave": `package main

import (
  "browser"
  "fmt"
  "time"
)

func visit(grid [][]int, queue []int, x int, y int, nx int, ny int) []int {
  if nx >= 0 && nx < len(grid[0]) && ny >= 0 && ny < len(grid) && grid[ny][nx] == -1 {
    grid[ny][nx] = grid[y][x] + 1
    queue = append(queue, nx)
    queue = append(queue, ny)
  }
  return queue
}

func draw(grid [][]int, sx int, sy int, tx int, ty int) {
  for y := 0; y < len(grid); y++ {
    for x := 0; x < len(grid[y]); x++ {
      level := 0
      if grid[y][x] == -2 { level = 1 }
      if grid[y][x] >= 0 { level = 2 + grid[y][x]%6 }
      browser.CanvasSetLevel(x, y, level)
    }
  }
  browser.CanvasSetLevel(sx, sy, 7)
  browser.CanvasSetLevel(tx, ty, 7)
  browser.CanvasFlush()
}

func main() {
  w, h := 32, 20
  sx, sy, tx, ty := 1, 1, w-2, h-2
  grid := make([][]int, h)
  for y := 0; y < h; y++ {
    grid[y] = make([]int, w)
    for x := 0; x < w; x++ {
      grid[y][x] = -1
      if x == 0 || y == 0 || x == w-1 || y == h-1 || (x*11+y*7+x*y)%17 < 3 { grid[y][x] = -2 }
    }
  }
  grid[sy][sx] = 0
  grid[ty][tx] = -1
  queue := []int{sx, sy}
  fmt.Println("Pathfinding wave — breadth-first search")
  browser.CanvasSize(w, h)
  for head := 0; head < len(queue); head += 2 {
    x := queue[head]
    y := queue[head+1]
    queue = visit(grid, queue, x, y, x+1, y)
    queue = visit(grid, queue, x, y, x-1, y)
    queue = visit(grid, queue, x, y, x, y+1)
    queue = visit(grid, queue, x, y, x, y-1)
    if head%6 == 0 {
      draw(grid, sx, sy, tx, ty)
      time.Sleep(42)
    }
    if grid[ty][tx] >= 0 { break }
  }
  draw(grid, sx, sy, tx, ty)
  for pulse := 0; pulse < 28; pulse++ {
    browser.CanvasSetLevel(tx, ty, 2+pulse%6)
    browser.CanvasFlush()
    time.Sleep(42)
  }
  if grid[ty][tx] >= 0 { fmt.Println("route found in", grid[ty][tx], "steps") } else { fmt.Println("no route found") }
}
`,
  "Julia Set": `package main

import (
  "browser"
  "fmt"
)

func juliaSteps(zx float64, zy float64) int {
  for step := 0; step < 56; step++ {
    xx := zx*zx - zy*zy - 0.72
    zy = 2*zx*zy + 0.18
    zx = xx
    if zx*zx+zy*zy > 9 { return step }
  }
  return 56
}

func main() {
  w, h := 80, 48
  fmt.Println("Julia set — fixed complex constant, 80 × 48 samples")
  browser.CanvasSize(w, h)
  for y := 0; y < h; y++ {
    zy := float64(y)*2.5/float64(h) - 1.25
    for x := 0; x < w; x++ {
      zx := float64(x)*3.4/float64(w) - 1.7
      depth := juliaSteps(zx, zy)
      level := 0
      if depth < 56 { level = 2 + depth%6 }
      browser.CanvasSetLevel(x, y, level)
    }
  }
  browser.CanvasFlush()
  fmt.Println("done")
}
`,
  "Rule 30": `package main

import (
  "browser"
  "fmt"
  "time"
)

func main() {
  w, h := 64, 36
  row := make([]int, w)
  row[w/2] = 1
  fmt.Println("Rule 30 — an elementary cellular automaton")
  browser.CanvasSize(w, h)
  for y := 0; y < h; y++ {
    for x := 0; x < w; x++ {
      if row[x] == 1 { browser.CanvasSetLevel(x, y, 2+y%6) }
    }
    browser.CanvasFlush()
    next := make([]int, w)
    for x := 0; x < w; x++ {
      left, center, right := 0, row[x], 0
      if x > 0 { left = row[x-1] }
      if x < w-1 { right = row[x+1] }
      if left == 1 {
        if center == 0 && right == 0 { next[x] = 1 }
      } else if center == 1 || right == 1 {
        next[x] = 1
      }
    }
    row = next
    time.Sleep(58)
  }
  fmt.Println("done")
}
`,
  "Knight's Tour": `package main

import (
  "browser"
  "fmt"
  "time"
)

func onward(board []int, x int, y int) int {
  dx := []int{1, 2, 2, 1, -1, -2, -2, -1}
  dy := []int{-2, -1, 1, 2, 2, 1, -1, -2}
  count := 0
  for i := 0; i < 8; i++ {
    nx, ny := x+dx[i], y+dy[i]
    if nx >= 0 && nx < 8 && ny >= 0 && ny < 8 {
      if board[ny*8+nx] == 0 { count++ }
    }
  }
  return count
}

func drawTour(board []int, px int, py int) {
  for y := 0; y < 8; y++ {
    for x := 0; x < 8; x++ {
      level := 1 + (x+y)%2
      if board[y*8+x] > 0 { level = 2 + board[y*8+x]%6 }
      browser.CanvasSetLevel(x, y, level)
    }
  }
  browser.CanvasSetLevel(px, py, 7)
  browser.CanvasFlush()
}

func main() {
  board := make([]int, 64)
  dx := []int{1, 2, 2, 1, -1, -2, -2, -1}
  dy := []int{-2, -1, 1, 2, 2, 1, -1, -2}
  x, y := 0, 0
  board[y*8+x] = 1
  browser.CanvasSize(8, 8)
  fmt.Println("Knight's tour — Warnsdorff's heuristic")
  for step := 2; step <= 64; step++ {
    best, bestScore := -1, 9
    for move := 0; move < 8; move++ {
      nx, ny := x+dx[move], y+dy[move]
      if nx >= 0 && nx < 8 && ny >= 0 && ny < 8 {
        if board[ny*8+nx] == 0 {
          score := onward(board, nx, ny)
          if score < bestScore { best, bestScore = move, score }
        }
      }
    }
    if best < 0 { break }
    x, y = x+dx[best], y+dy[best]
    board[y*8+x] = step
    drawTour(board, x, y)
    time.Sleep(45)
  }
  fmt.Println("done")
}
`,
  "Metaballs": `package main

import (
  "browser"
  "fmt"
  "time"
)

func main() {
  w, h := 56, 32
  browser.CanvasSize(w, h)
  fmt.Println("Metaballs — 96 additive distance-field frames")
  for frame := 0; frame < 96; frame++ {
    ax, ay := 12+frame%24, 8+(frame*3)%14
    bx, by := 42-(frame*2)%24, 22-(frame*5)%14
    for y := 0; y < h; y++ {
      for x := 0; x < w; x++ {
        da := (x-ax)*(x-ax) + (y-ay)*(y-ay)
        db := (x-bx)*(x-bx) + (y-by)*(y-by)
        level := 0
        if da+db < 430 { level = 2 }
        if da+db < 260 { level = 4 }
        if da+db < 150 { level = 6 }
        if da < 16 || db < 16 { level = 7 }
        browser.CanvasSetLevel(x, y, level)
      }
    }
    browser.CanvasFlush()
    time.Sleep(30)
  }
  fmt.Println("done")
}
`,
  "Orbit Simulator": `package main

import (
  "browser"
  "fmt"
  "math"
  "time"
)

func main() {
  w, h := 56, 32
  browser.CanvasSize(w, h)
  fmt.Println("Orbit simulator — 144 harmonic orbit frames")
  for frame := 0; frame < 144; frame++ {
    for y := 0; y < h; y++ {
      for x := 0; x < w; x++ { browser.CanvasSetLevel(x, y, 0) }
    }
    browser.CanvasSetLevel(w/2, h/2, 7)
    for planet := 0; planet < 3; planet++ {
      angle := float64(frame)*(0.08+float64(planet)*0.035) + float64(planet)*2.1
      radius := 7 + planet*4
      x := w/2 + int(math.Cos(angle)*float64(radius))
      y := h/2 + int(math.Sin(angle)*float64(radius)/2)
      browser.CanvasSetLevel(x, y, 3+planet*2)
      browser.CanvasSetLevel((x+w/2)/2, (y+h/2)/2, 1+planet)
    }
    browser.CanvasFlush()
    time.Sleep(28)
  }
  fmt.Println("done")
}
`,
  "Sierpinski Triangle": `package main

import (
  "browser"
  "fmt"
)

func visible(x int, y int) bool {
  for x > 0 || y > 0 {
    if x%2 == 1 && y%2 == 1 { return false }
    x = x / 2
    y = y / 2
  }
  return true
}

func main() {
  w, h := 64, 40
  browser.CanvasSize(w, h)
  fmt.Println("Sierpinski triangle — Pascal's triangle modulo two")
  for y := 0; y < h; y++ {
    for x := 0; x < w; x++ {
      localX := x - (w-h)/2
      if localX >= 0 && localX <= y && visible(localX, y) {
        browser.CanvasSetLevel(x, y, 2+y%6)
      } else {
        browser.CanvasSetLevel(x, y, 0)
      }
    }
  }
  browser.CanvasFlush()
  fmt.Println("done")
}
`,
  "Monte Carlo Pi": `package main

import (
  "browser"
  "fmt"
  "time"
)

func next(state int) int {
  return (state*25173 + 13849) % 65536
}

func main() {
  w, h := 40, 40
  state, inside := 17, 0
  browser.CanvasSize(w, h)
  fmt.Println("Monte Carlo π — deterministic random samples")
  for sample := 1; sample <= 1440; sample++ {
    state = next(state)
    x := state % w
    state = next(state)
    y := state % h
    dx, dy := x-w/2, y-h/2
    level := 1
    if dx*dx+dy*dy < (w/2)*(w/2) {
      inside++
      level = 5
    }
    browser.CanvasSetLevel(x, y, level)
    if sample%16 == 0 {
      browser.CanvasFlush()
      time.Sleep(24)
    }
  }
  fmt.Println("π estimate:", float64(inside)*4/1440)
}
`,
  "Turing Machine": `package main

import (
  "browser"
  "fmt"
  "time"
)

func drawTape(tape []int, head int, state int) {
  for x := 0; x < len(tape); x++ {
    level := 1
    if tape[x] == 1 { level = 4 }
    browser.CanvasSetLevel(x, 1, level)
    browser.CanvasSetLevel(x, 2, level)
  }
  browser.CanvasSetLevel(head, 0, 7)
  browser.CanvasSetLevel(head, 3, 2+state*3)
  browser.CanvasFlush()
}

func main() {
  tape := make([]int, 48)
  head, state := 24, 0
  browser.CanvasSize(48, 4)
  fmt.Println("Turing machine — two states, a growing tape")
  for step := 0; step < 144; step++ {
    drawTape(tape, head, state)
    if state == 0 {
      if tape[head] == 0 { tape[head] = 1; head++; state = 1 } else { tape[head] = 0; head--; state = 1 }
    } else {
      if tape[head] == 0 { tape[head] = 1; head--; state = 0 } else { tape[head] = 1; head++; state = 0 }
    }
    if head < 2 { head = 2 }
    if head > len(tape)-3 { head = len(tape)-3 }
    time.Sleep(38)
  }
  fmt.Println("done")
}
`,
  "HTTP Client GET": `package main

import (
  "fmt"
  "http"
  "strings"
)

func main() {
  fmt.Println("GET /todos/1 — first 120 characters:")
  txt, err := http.GetText("https://jsonplaceholder.typicode.com/todos/1")
  if err != nil {
    fmt.Println("GET failed (network/CORS):", err)
    return
  }
  sn := strings.TrimSpace(txt)
  if len(sn) > 120 { sn = sn[:120] + "..." }
  fmt.Println(sn)
}
`,
  "HTTP Client POST": `package main

import (
  json "encoding/json"
  "fmt"
  "http"
  "strings"
)

func main() {
  // PostText sends text with application/json by default.
  payload := map[string]any{"title":"nanoGo from WASM", "completed":false}
  bodyBytes, _ := json.Marshal(payload)
  body := string(bodyBytes)
  reply, err := http.PostText("https://jsonplaceholder.typicode.com/todos", body)
  if err != nil {
    fmt.Println("POST failed (network/CORS):", err)
    return
  }
  reply = strings.TrimSpace(reply)
  if len(reply) > 120 { reply = reply[:120] + "..." }
  fmt.Println("POST accepted; response:")
  fmt.Println(reply)
}
`,
  "HTTP Client Errors": `package main

import (
  "fmt"
  "http"
)

func main() {
  // Two-value assignment lets code handle non-2xx responses explicitly.
  body, err := http.GetText("https://jsonplaceholder.typicode.com/nanogo-missing-route")
  if err != nil {
    fmt.Println("expected HTTP error:", err)
    return
  }
  fmt.Println("unexpected success:", body)
}
`,
  "HTTP Server Router (simulated)": `package main

import "fmt"

// A GitHub Pages app cannot listen on a network port. This pure handler shows
// the same routing and response decisions a tiny HTTP server would make.
func handleRequest(method string, path string, body string) string {
  if method == "GET" && path == "/health" {
    return "200 OK {status:healthy}"
  }
  if method == "POST" && path == "/echo" {
    return "201 Created echo=" + body
  }
  return "404 Not Found"
}

func main() {
  fmt.Println("GET /health  ->", handleRequest("GET", "/health", ""))
  fmt.Println("POST /echo   ->", handleRequest("POST", "/echo", "hello wasm"))
  fmt.Println("DELETE /todo ->", handleRequest("DELETE", "/todo", ""))
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
    fmt.Printf("10 / 3 = %.4f\\n", result)
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
  fmt.Printf("  Pi = %.6f\\n", math.Pi)
  fmt.Printf("  E  = %.6f\\n", math.E)

  fmt.Println("Functions:")
  fmt.Printf("  Sqrt(2)       = %.6f\\n", math.Sqrt(2))
  fmt.Printf("  Pow(2, 10)    = %.0f\\n",  math.Pow(2, 10))
  fmt.Printf("  Abs(-5.5)     = %.1f\\n",  math.Abs(-5.5))
  fmt.Printf("  Floor(3.7)    = %.1f\\n",  math.Floor(3.7))
  fmt.Printf("  Ceil(3.2)     = %.1f\\n",  math.Ceil(3.2))
  fmt.Printf("  Round(3.5)    = %.1f\\n",  math.Round(3.5))
  fmt.Printf("  Log(E)        = %.6f\\n",  math.Log(math.E))
  fmt.Printf("  Log2(1024)    = %.1f\\n",  math.Log2(1024))
  fmt.Printf("  Sin(Pi/2)     = %.6f\\n",  math.Sin(math.Pi/2))
  fmt.Printf("  Cos(Pi)       = %.6f\\n",  math.Cos(math.Pi))
  fmt.Printf("  Max(3.0, 7.0) = %.1f\\n",  math.Max(3, 7))
  fmt.Printf("  Min(3.0, 7.0) = %.1f\\n",  math.Min(3, 7))
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
`,"Select Statement": `package main

import (
  "fmt"
  "time"
)

func worker(id int, delayMs int, out chan string) {
  time.Sleep(delayMs)
  out <- fmt.Sprintf("worker %d finished", id)
}

func main() {
  fmt.Println("select demo: multiplexing channels with a timeout")
  a := make(chan string, 1)
  b := make(chan string, 1)
  go worker(1, 30, a)
  go worker(2, 400, b)

  for i := 0; i < 2; i++ {
    timeout := time.NewTimer(150)
    select {
    case msg := <-a:
      fmt.Println("a:", msg)
      timeout.Stop()
    case msg := <-b:
      fmt.Println("b:", msg)
      timeout.Stop()
    case <-timeout.C:
      fmt.Println("timeout: no worker replied in time")
    }
  }

  select {
  case msg := <-a:
    fmt.Println("late a:", msg)
  default:
    fmt.Println("a has nothing buffered right now")
  }
}
`,"Interfaces & Polymorphism": `package main

import "fmt"

type Shape interface {
  Area() float64
  Name() string
}

type Circle struct{ Radius float64 }
func (c Circle) Area() float64 { return 3.14159 * c.Radius * c.Radius }
func (c Circle) Name() string  { return "circle" }

type Rectangle struct{ Width, Height float64 }
func (r Rectangle) Area() float64 { return r.Width * r.Height }
func (r Rectangle) Name() string  { return "rectangle" }

func describe(s Shape) {
  fmt.Printf("%s has area %.2f\\n", s.Name(), s.Area())
}

func main() {
  fmt.Println("Interfaces & polymorphism demo")
  shapes := []Shape{
    Circle{Radius: 2.0},
    Rectangle{Width: 3.0, Height: 4.0},
    Circle{Radius: 0.5},
  }

  total := 0.0
  for _, s := range shapes {
    describe(s)
    total += s.Area()
  }
  fmt.Printf("total area: %.2f\\n", total)
}
`,"Custom Errors": `package main

import "fmt"

type ValidationError struct {
  Field string
  Msg   string
}

func (e *ValidationError) Error() string {
  return "validation failed on " + e.Field + ": " + e.Msg
}

func validateAge(age int) error {
  if age < 0 {
    return &ValidationError{Field: "age", Msg: "must not be negative"}
  }
  if age > 150 {
    return &ValidationError{Field: "age", Msg: "unrealistically large"}
  }
  return nil
}

func main() {
  fmt.Println("Custom error type demo")
  for _, age := range []int{30, -5, 200, 65} {
    err := validateAge(age)
    if err != nil {
      fmt.Println("rejected", age, "-", err.Error())
      continue
    }
    fmt.Println("accepted", age)
  }
}
`,"Stack (LIFO)": `package main

import "fmt"

func push(stack []int, v int) []int { return append(stack, v) }

func main() {
  fmt.Println("Stack demo (LIFO)")
  var stack []int
  for i := 1; i <= 5; i++ {
    stack = push(stack, i*i)
    fmt.Println("pushed", i*i, "- size:", len(stack))
  }

  fmt.Println("popping all values:")
  for len(stack) > 0 {
    top := stack[len(stack)-1]
    stack = stack[:len(stack)-1]
    fmt.Println("popped:", top, "- remaining:", len(stack))
  }
}
`,"Binary Search": `package main

import "fmt"

// binarySearch returns the index of target in a sorted slice, or -1.
// The midpoint uses a bit shift instead of /2 to keep the computation exact.
func binarySearch(sorted []int, target int) int {
  lo, hi := 0, len(sorted)-1
  steps := 0
  for lo <= hi {
    steps++
    mid := (lo + hi) >> 1
    switch {
    case sorted[mid] == target:
      fmt.Println("  found after", steps, "step(s)")
      return mid
    case sorted[mid] < target:
      lo = mid + 1
    default:
      hi = mid - 1
    }
  }
  fmt.Println("  exhausted search after", steps, "step(s)")
  return -1
}

func main() {
  fmt.Println("Binary search demo")
  nums := []int{1, 3, 5, 7, 9, 11, 13, 17, 21, 33, 40, 55}
  fmt.Println("sorted input:", len(nums), "elements")

  for _, target := range []int{7, 55, 4, 21} {
    fmt.Println("searching for", target)
    idx := binarySearch(nums, target)
    if idx >= 0 {
      fmt.Println("  ->", target, "is at index", idx)
    } else {
      fmt.Println("  ->", target, "is not in the slice")
    }
  }
}
`,
  "Interface Dispatch": `package main

import "fmt"

type Money struct{ Cents int }

func (m Money) Text() string {
  sign, c := "", m.Cents
  if c < 0 {
    sign, c = "-", -c
  }
  return fmt.Sprintf("%s%d.%02d", sign, c/100, c%100)
}

// Charge is satisfied by any struct that has these two methods.
type Charge interface {
  Label() string
  Amount() Money
}

type LineItem struct {
  Product string
  Unit    Money
  Qty     int
}

func (li LineItem) Label() string { return fmt.Sprintf("%s x%d", li.Product, li.Qty) }
func (li LineItem) Amount() Money { return Money{Cents: li.Unit.Cents * li.Qty} }

type Rebate struct {
  Reason string
  Off    Money
}

func (r Rebate) Label() string { return "rebate: " + r.Reason }
func (r Rebate) Amount() Money { return Money{Cents: -r.Off.Cents} }

func main() {
  invoice := []Charge{
    LineItem{Product: "Mechanical keyboard", Unit: Money{Cents: 4999}, Qty: 2},
    LineItem{Product: "USB-C cable", Unit: Money{Cents: 1290}, Qty: 3},
    Rebate{Reason: "loyalty tier", Off: Money{Cents: 1500}},
  }

  total := 0
  for _, c := range invoice {
    // This loop never names LineItem or Rebate: the value carries its own methods.
    fmt.Printf("%-24s %9s EUR", c.Label(), c.Amount().Text())
    total += c.Amount().Cents
  }
  fmt.Printf("%-24s %9s EUR", "TOTAL", Money{Cents: total}.Text())
}`,
  "Type Assertions": `package main

import "fmt"

// Every alert sink can deliver.
type Sink interface {
  Deliver(msg string) string
}

// Retryable is an optional add-on interface: only some sinks implement it.
type Retryable interface {
  MaxAttempts() int
}

type PagerSink struct {
  Rotation string
}

func (p PagerSink) Deliver(msg string) string { return "page " + p.Rotation + " -- " + msg }
func (p PagerSink) MaxAttempts() int          { return 5 }

type ConsoleSink struct {
  Prefix string
}

func (c ConsoleSink) Deliver(msg string) string { return c.Prefix + " " + msg }

func main() {
  sinks := []Sink{
    PagerSink{Rotation: "sre-primary"},
    ConsoleSink{Prefix: "[local]"},
  }

  for _, s := range sinks {
    fmt.Println(s.Deliver("disk usage at 91 percent"))

    // nanoGo has no switch x.(type); a comma-ok assertion does the same job.
    // Asserting to an interface asks "does this value also do that?"
    if r, ok := s.(Retryable); ok {
      fmt.Println("   retries up to", r.MaxAttempts(), "times")
    } else {
      fmt.Println("   delivered once, no retries")
    }

    // Asserting to a concrete type hands back that struct's own fields.
    if p, ok := s.(PagerSink); ok {
      fmt.Println("   escalates to rotation", p.Rotation)
    }
  }
}`,
  "Method Chaining": `package main

import "fmt"

type Version struct {
  Major int
  Minor int
  Patch int
}

func (v Version) Text() string {
  return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Each method derives a NEW Version instead of editing the receiver,
// which is what lets the calls chain.
func (v Version) NextPatch() Version {
  return Version{Major: v.Major, Minor: v.Minor, Patch: v.Patch + 1}
}

func (v Version) NextMinor() Version {
  return Version{Major: v.Major, Minor: v.Minor + 1, Patch: 0}
}

func (v Version) rank() int {
  return v.Major*1000000 + v.Minor*1000 + v.Patch
}

func (v Version) NewerThan(other Version) bool {
  return v.rank() > other.rank()
}

func main() {
  released := Version{Major: 1, Minor: 4, Patch: 2}
  candidate := released.NextMinor().NextPatch().NextPatch()

  fmt.Println("released: ", released.Text())
  fmt.Println("candidate:", candidate.Text(), "(built by chaining three methods)")
  fmt.Println("hotfix:   ", released.NextPatch().Text())

  tags := []Version{
    Version{Major: 0, Minor: 9, Patch: 9},
    Version{Major: 1, Minor: 4, Patch: 2},
    Version{Major: 2, Minor: 0, Patch: 0},
  }
  for _, t := range tags {
    fmt.Printf("%-9s newer than released: %t", t.Text(), t.NewerThan(released))
  }
}`,
  "Nested Structs": `package main

import "fmt"

type Endpoint struct {
  Host string
  Port int
}

func (e Endpoint) Addr() string { return fmt.Sprintf("%s:%d", e.Host, e.Port) }

type Quota struct {
  RequestsPerSec int
  BurstFactor    int
}

func (q Quota) Burst() int { return q.RequestsPerSec * q.BurstFactor }

// Service holds two Endpoints and a Quota as ordinary fields.
type Service struct {
  Name    string
  Primary Endpoint
  Replica Endpoint
  Limits  Quota
}

// The outer method reaches through the nesting and reuses the inner methods.
func (s Service) Summary() string {
  return fmt.Sprintf("%-9s %-18s %-18s %6d rps burst",
    s.Name, s.Primary.Addr(), s.Replica.Addr(), s.Limits.Burst())
}

func main() {
  fleet := []Service{
    Service{Name: "checkout",
      Primary: Endpoint{Host: "eu-west-1.svc", Port: 8080},
      Replica: Endpoint{Host: "eu-west-2.svc", Port: 8080},
      Limits:  Quota{RequestsPerSec: 400, BurstFactor: 3}},
    Service{Name: "search",
      Primary: Endpoint{Host: "eu-west-1.svc", Port: 9200},
      Replica: Endpoint{Host: "us-east-1.svc", Port: 9200},
      Limits:  Quota{RequestsPerSec: 150, BurstFactor: 4}},
  }

  capacity := 0
  for _, s := range fleet {
    fmt.Println(s.Summary())
    capacity += s.Limits.Burst()
  }
  fmt.Println("fleet burst capacity:", capacity, "rps")
  fmt.Println("checkout replica host:", fleet[0].Replica.Host)
}`,
  "Closure Ledgers": `package main

import "fmt"

// newLedger seeds a balance from any number of opening deposits, then
// returns a closure over it. Every call to newLedger creates a fresh
// balance that outlives the return and is reachable only through the
// function handed back -- no globals, nothing shared between ledgers.
func newLedger(owner string, opening ...int) func(int) int {
  balance := 0
  for _, amount := range opening {
    balance += amount
  }
  fmt.Printf("%-6s opened with %d deposit(s), balance %d", owner, len(opening), balance)
  return func(delta int) int {
    balance += delta
    fmt.Printf("%-6s %+5d -> %4d", owner, delta, balance)
    return balance
  }
}

func main() {
  fmt.Println("-- opening accounts --")
  alice := newLedger("alice", 400, 250)
  bob := newLedger("bob", 90)
  seed := []int{30, 30, 30}
  carol := newLedger("carol", seed...) // spread a slice into the variadic

  fmt.Println("-- transactions --")
  alice(-120)
  bob(60)
  carol(-25)
  aliceEnd := alice(75)
  bobEnd := bob(-40)
  carolEnd := carol(-15)

  fmt.Println("-- final --")
  fmt.Println("alice", aliceEnd, "| bob", bobEnd, "| carol", carolEnd)
  fmt.Println("three closures, three private balances")
}`,
  "Defer Order": `package main

import "fmt"

func acquire(name string) string {
  fmt.Println("  acquire", name)
  return name
}

func release(name string) {
  fmt.Println("  release", name)
}

func runMigration() {
  // Each defer pushes onto a stack that unwinds last-in-first-out, so
  // resources are released in the exact reverse of acquisition order.
  // Note acquire() runs NOW -- only release() is postponed.
  defer release(acquire("config"))
  defer release(acquire("database"))
  defer release(acquire("audit-log"))
  fmt.Println("  applying schema changes")
}

func reportRetries() {
  for attempt := 1; attempt <= 3; attempt++ {
    // attempt is evaluated here, at defer time, not at unwind time
    defer fmt.Println("  rolled back attempt", attempt)
  }
  fmt.Println("  loop finished")
}

func main() {
  fmt.Println("migration:")
  runMigration()
  fmt.Println("retry log:")
  reportRetries()
  fmt.Println("done")
}`,
  "Panic and Recover": `package main

import (
  "fmt"
  "strconv"
  "strings"
)

// mustPort is written the "can't fail" way: it panics instead of
// returning an error, so callers below it stay free of error checks.
func mustPort(line string) int {
  fields := strings.Split(line, "=")
  if len(fields) != 2 {
    panic("malformed line: " + line)
  }
  value := strings.TrimSpace(fields[1])
  port := strconv.Atoi(value)
  if port < 1 || port > 65535 {
    panic("port out of range: " + value)
  }
  return port
}

func bindService(line string) {
  // recover() only works from inside a deferred function. It stops the
  // panic unwinding here, so main survives and the loop keeps going --
  // note "listener ready" never prints for a line that panicked.
  defer func() {
    if r := recover(); r != nil {
      fmt.Println("  skipped:", r)
    }
  }()
  fmt.Println("  bound on port", mustPort(line))
  fmt.Println("  listener ready")
}

func main() {
  config := []string{"http = 8080", "https = 443", "metrics", "debug = 70000", "ssh = 22"}
  for _, line := range config {
    fmt.Println("config:", line)
    bindService(line)
  }
  fmt.Println("startup complete despite 2 bad lines")
}`,
  "Labeled Break and Goto": `package main

import "fmt"

// One string per conveyor lane: '.' is a good unit, '#' means the lane
// is offline, 'x' is a jam that halts the whole scan.
var lanes = []string{
  "..#.",
  "....",
  ".x..",
}

func main() {
  fmt.Println("-- labeled continue and break --")
scan:
  for lane := 0; lane < len(lanes); lane++ {
    for slot := 0; slot < len(lanes[lane]); slot++ {
      unit := lanes[lane][slot]
      if unit == '#' {
        // plain continue would advance slot; the label advances lane
        fmt.Println("lane", lane, "offline at slot", slot, "- next lane")
        continue scan
      }
      if unit == 'x' {
        // plain break would escape only the inner loop
        fmt.Println("lane", lane, "JAM at slot", slot, "- halting scan")
        break scan
      }
      fmt.Println("lane", lane, "slot", slot, "ok")
    }
  }

  fmt.Println("-- goto --")
  attempt := 0
retry:
  attempt++
  fmt.Println("clearing jam, attempt", attempt)
  if attempt < 3 {
    goto retry // jumps backwards to the label: a retry loop without a loop
  }
  fmt.Println("jam cleared after", attempt, "attempts")
}`,
  "Password Strength": `package main

import (
  "fmt"
  "testing"
)

// classify scores a password's strength on a 0-4 scale by counting how many
// distinct character classes it uses -- the same heuristic a signup form
// might run client-side.
func classify(pw string) int {
  if len(pw) < 8 {
    return 0
  }
  hasUpper, hasLower, hasDigit, hasSymbol := false, false, false, false
  for _, r := range pw {
    switch {
    case r >= 'A' && r <= 'Z':
      hasUpper = true
    case r >= 'a' && r <= 'z':
      hasLower = true
    case r >= '0' && r <= '9':
      hasDigit = true
    default:
      hasSymbol = true
    }
  }
  score := 0
  if hasUpper {
    score++
  }
  if hasLower {
    score++
  }
  if hasDigit {
    score++
  }
  if hasSymbol {
    score++
  }
  return score
}

func label(score int) string {
  switch score {
  case 0:
    return "too short"
  case 1, 2:
    return "weak"
  case 3:
    return "good"
  default:
    return "strong"
  }
}

func TestClassify(t *testing.T) {
  t.Run("too_short", func(t *testing.T) {
    if got := classify("Ab1!"); got != 0 {
      t.Errorf("classify(short) = %d, want 0", got)
    }
  })
  t.Run("digits_only", func(t *testing.T) {
    if got := classify("12345678"); got != 1 {
      t.Errorf("classify(digits only) = %d, want 1", got)
    }
  })
  t.Run("all_classes", func(t *testing.T) {
    if got := classify("Secret1!"); got != 4 {
      t.Errorf("classify(all classes) = %d, want 4", got)
    }
  })
}

func main() {
  passwords := []string{"abc", "12345678", "password1", "Secret1!"}
  for i := 0; i < len(passwords); i++ {
    score := classify(passwords[i])
    fmt.Printf("%-11s score=%d (%s)", passwords[i], score, label(score))
  }
}`,
  "Balanced Brackets": `package main

import (
  "fmt"
  "testing"
)

// Stack is a LIFO of strings, backed by a slice. Declared at package level:
// nanoGo has no function-local type declarations.
type Stack struct {
  items []string
}

func (s *Stack) Push(v string) {
  s.items = append(s.items, v)
}

// Pop panics on an empty stack rather than returning a second "ok" value --
// deliberately, so the test below can exercise recover().
func (s *Stack) Pop() string {
  n := len(s.items)
  if n == 0 {
    panic("pop from empty stack")
  }
  top := s.items[n-1]
  s.items = s.items[:n-1]
  return top
}

func (s *Stack) Len() int { return len(s.items) }

func TestStackOrdering(t *testing.T) {
  s := &Stack{}
  s.Push("a")
  s.Push("b")
  s.Push("c")
  if got := s.Pop(); got != "c" {
    t.Fatalf("Pop() = %q, want %q (LIFO order broken)", got, "c")
  }
  if got := s.Len(); got != 2 {
    t.Errorf("Len() after one pop = %d, want 2", got)
  }
}

func TestStackPopEmptyPanics(t *testing.T) {
  defer func() {
    if r := recover(); r == nil {
      t.Fatal("Pop() on an empty stack did not panic")
    }
  }()
  empty := &Stack{}
  empty.Pop()
}

// balanced reports whether brackets in expr nest correctly, using Stack as
// its scratch space -- a stack's natural job. Characters are compared as
// one-character substrings (expr[i:i+1]) rather than converted between int
// and string, which follow different rules here than plain Go.
func balanced(expr string) bool {
  s := &Stack{}
  for i := 0; i < len(expr); i++ {
    c := expr[i : i+1]
    switch c {
    case "(", "[", "{":
      s.Push(c)
    case ")", "]", "}":
      if s.Len() == 0 {
        return false
      }
      top := s.Pop()
      opensThis := (c == ")" && top == "(") || (c == "]" && top == "[") || (c == "}" && top == "{")
      if !opensThis {
        return false
      }
    }
  }
  return s.Len() == 0
}

func main() {
  exprs := []string{"(a+b)*[c-d]", "{[()]}", "(a+b]", "((("}
  for i := 0; i < len(exprs); i++ {
    fmt.Printf("%-14s balanced=%t", exprs[i], balanced(exprs[i]))
  }
}`,
  "Allocation Benchmark": `package main

import (
  "fmt"
  "testing"
  "time"
)

// buildLine grows a slice one append at a time -- a common allocation-heavy
// pattern worth benchmarking on its own, distinct from a plain string scan.
func buildLine(n int) []int {
  var out []int
  for i := 0; i < n; i++ {
    out = append(out, i*i)
  }
  return out
}

// BenchmarkBuildLine reports allocations as well as time -- b.ReportAllocs()
// is the piece a bare timing loop misses, and it is what tells you an
// append-heavy hot path is worth pre-sizing with make([]int, 0, n).
func BenchmarkBuildLine(b *testing.B) {
  b.ReportAllocs()
  for i := 0; i < b.N; i++ {
    buildLine(64)
  }
}

func main() {
  b := testing.B{N: 300}
  started := time.Now()
  BenchmarkBuildLine(&b)
  fmt.Printf("BenchmarkBuildLine: N=%d, wall=%dms", b.N, time.Since(started))
  fmt.Println("(ReportAllocs is what a plain time.Since loop can't tell you)")

  sample := buildLine(8)
  line := ""
  for i := 0; i < len(sample); i++ {
    if i > 0 {
      line = line + " "
    }
    line = line + fmt.Sprintf("%d", sample[i])
  }
  fmt.Println("sample output: [" + line + "]")
}`,
  "Word Frequency": `package main

import (
  "fmt"
  "sort"
  "strings"
)

// Count pairs a word with how often it appeared, so the frequency table can
// be sorted -- Go maps have no defined iteration order.
type Count struct {
  Word string
  N    int
}

func main() {
  text := "the quick brown fox jumps over the lazy dog the fox runs the dog barks"
  words := strings.Split(text, " ")

  freq := map[string]int{}
  for i := 0; i < len(words); i++ {
    freq[words[i]] = freq[words[i]] + 1
  }

  // Collect into a slice so sort.Strings (the only sort nanoGo exposes
  // besides Ints/Float64s) can order it deterministically.
  unique := []string{}
  for w := range freq {
    unique = append(unique, w)
  }
  sort.Strings(unique)

  counts := []Count{}
  for i := 0; i < len(unique); i++ {
    counts = append(counts, Count{Word: unique[i], N: freq[unique[i]]})
  }

  fmt.Println("word frequencies (alphabetical):")
  for i := 0; i < len(counts); i++ {
    bar := strings.Repeat("#", counts[i].N)
    fmt.Printf("%-7s %d %s", counts[i].Word, counts[i].N, bar)
  }
  fmt.Println("distinct words:", len(unique))
}`,
  "Matrix Multiply": `package main

import "fmt"

// Matrix is a small fixed-shape 2D grid of ints, stored row-major.
type Matrix struct {
  Rows int
  Cols int
  Data []int
}

func (m Matrix) At(r, c int) int { return m.Data[r*m.Cols+c] }

func newMatrix(rows, cols int, values []int) Matrix {
  return Matrix{Rows: rows, Cols: cols, Data: values}
}

// multiply computes a*b the textbook way: each output cell is a dot product
// of a row of a and a column of b.
func multiply(a Matrix, b Matrix) Matrix {
  if a.Cols != b.Rows {
    panic("incompatible matrix shapes")
  }
  out := make([]int, a.Rows*b.Cols)
  for r := 0; r < a.Rows; r++ {
    for c := 0; c < b.Cols; c++ {
      sum := 0
      for k := 0; k < a.Cols; k++ {
        sum = sum + a.At(r, k)*b.At(k, c)
      }
      out[r*b.Cols+c] = sum
    }
  }
  return newMatrix(a.Rows, b.Cols, out)
}

func printMatrix(m Matrix) {
  for r := 0; r < m.Rows; r++ {
    line := ""
    for c := 0; c < m.Cols; c++ {
      line = line + fmt.Sprintf("%4d", m.At(r, c))
    }
    fmt.Println(line)
  }
}

func main() {
  a := newMatrix(2, 3, []int{1, 2, 3, 4, 5, 6})
  b := newMatrix(3, 2, []int{7, 8, 9, 10, 11, 12})

  fmt.Println("A (2x3):")
  printMatrix(a)
  fmt.Println("B (3x2):")
  printMatrix(b)

  product := multiply(a, b)
  fmt.Println("A * B (2x2):")
  printMatrix(product)
}`,
  "Anagram Groups": `package main

import (
  "fmt"
  "sort"
  "strings"
)

// letterKey reduces a word to a canonical form -- its letters sorted -- so
// two anagrams of each other always produce the same key.
func letterKey(word string) string {
  letters := strings.Split(strings.ToLower(word), "")
  sort.Strings(letters)
  return strings.Join(letters, "")
}

func main() {
  words := []string{"listen", "silent", "enlist", "banana", "orange", "ratio", "riota"}

  groups := map[string][]string{}
  keys := []string{}
  for i := 0; i < len(words); i++ {
    key := letterKey(words[i])
    if _, seen := groups[key]; !seen {
      // A missing map entry reads back as nil, and appending to nil
      // needs an explicit empty slice first -- this is the one line
      // that does that, before the append below ever runs on this key.
      groups[key] = []string{}
      keys = append(keys, key)
    }
    groups[key] = append(groups[key], words[i])
  }
  sort.Strings(keys)

  fmt.Println("anagram groups:")
  for i := 0; i < len(keys); i++ {
    members := groups[keys[i]]
    if len(members) > 1 {
      fmt.Println(" ", strings.Join(members, " = "))
    } else {
      fmt.Println(" ", members[0], "(no match)")
    }
  }
}`,
  "Monthly Reports (VFS)": `package main

import (
  "fmt"
  "os"
)

// This program writes files to nanoGo's virtual filesystem, lists the
// directory it just populated, and reads one file back -- all sandboxed:
// nothing here touches the real disk, only the VFS the playground grants
// this program read/write access to.
func main() {
  if err := os.MkdirAll("/reports", 0755); err != nil {
    fmt.Println("mkdir failed:", err)
    return
  }

  names := []string{"january.txt", "february.txt", "march.txt"}
  totals := []int{1200, 980, 1450}
  for i := 0; i < len(names); i++ {
    content := fmt.Sprintf("month total: %d", totals[i])
    path := "/reports/" + names[i]
    if _, err := os.WriteFile(path, content, 0644); err != nil {
      fmt.Println("write failed:", err)
      return
    }
  }
  fmt.Println("wrote", len(names), "report(s) to /reports")

  entries, err := os.ReadDir("/reports")
  if err != nil {
    fmt.Println("readdir failed:", err)
    return
  }
  fmt.Println("directory listing:")
  sum := 0
  for i := 0; i < len(entries); i++ {
    info := entries[i]
    data, readErr := os.ReadFile("/reports/" + info.Name)
    if readErr != nil {
      fmt.Println("  read failed:", readErr)
      continue
    }
    fmt.Printf("  %-16s %d bytes", info.Name, info.Size)
    fmt.Println("   ", data)
    sum++
  }
  fmt.Println("read back", sum, "file(s) successfully")
}`,
};
