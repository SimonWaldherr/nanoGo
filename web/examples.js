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
  "Empty": "package main\n\nfunc main() {\n}\n"
};
