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
      time.Sleep(20)
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
  fmt.Println("Scanner — a sweeping line with targets")
  w, h := 48, 24
  browser.CanvasSize(w, h)
  for t := 0; t < 72; t++ {
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
  fmt.Println("Langton's ant — 200 rule-driven steps")
  browser.CanvasSize(w, h)
  for step := 0; step < 200; step++ {
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
    if head%10 == 0 {
      draw(grid, sx, sy, tx, ty)
      time.Sleep(30)
    }
    if grid[ty][tx] >= 0 { break }
  }
  draw(grid, sx, sy, tx, ty)
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
    time.Sleep(34)
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
  fmt.Println("Metaballs — additive distance fields")
  for frame := 0; frame < 48; frame++ {
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
`};
