# nanoGo

> **A lightweight Go interpreter designed for WebAssembly** — Bringing the power of Go to the browser with minimal overhead

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Go Version](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go)](https://go.dev/)
[![WebAssembly](https://img.shields.io/badge/WebAssembly-654FF0?logo=webassembly&logoColor=white)](https://webassembly.org/)

## 🚀 Overview

nanoGo is a **minimalist Go interpreter** that runs entirely in your web browser via WebAssembly. While projects like TinyGo focus on compiling Go to WASM, nanoGo takes a different approach: it provides an **interpreted Go runtime** with a footprint even smaller than TinyGo, enabling dynamic Go code execution directly in the browser.

**Key Distinction:** Instead of compiling Go programs ahead-of-time to WASM, nanoGo is an interpreter written in Go, compiled to WASM, that can execute Go source code dynamically at runtime.

## ✨ Why Go in the Browser (via WebAssembly)?

Running Go in the browser through WebAssembly opens up exciting possibilities that traditional JavaScript development cannot easily match:

### 🎯 **Type Safety & Performance**
- **Strong Static Typing**: Catch errors at compile-time rather than runtime, leading to more robust browser applications
- **Near-Native Performance**: WebAssembly executes at near-native speed, making Go code in the browser significantly faster than equivalent JavaScript for compute-intensive tasks
- **Predictable Performance**: Go's garbage collector and memory management provide consistent performance characteristics

### 🔧 **Developer Experience**
- **Familiar Tooling**: Use the Go toolchain, testing framework, and ecosystem you already know
- **Code Reuse**: Share business logic between backend (Go servers) and frontend (browser) without rewrites
- **Goroutines in the Browser**: Leverage Go's powerful concurrency primitives (goroutines, channels) for complex async operations
- **Standard Library Access**: Use familiar Go packages like `fmt`, `time`, `sync`, `strings`, `regexp`, and more

### 🛡️ **Safety & Security**
- **Memory Safety**: Go's memory management eliminates entire classes of security vulnerabilities (buffer overflows, use-after-free)
- **Sandboxed Execution**: WebAssembly provides a secure sandbox, and nanoGo's interpreter adds another isolation layer
- **Type Safety**: Prevent common JavaScript pitfalls like type coercion bugs and undefined behavior

### 🌐 **Universal Platform**
- **Write Once, Run Everywhere**: The same Go code can run on servers, desktop, mobile, and now browsers
- **No Transpilation Hassles**: Unlike TypeScript or other compile-to-JS languages, you're running actual Go semantics
- **Future-Proof**: WebAssembly is a W3C standard supported by all major browsers

### 📦 **Lightweight & Portable**
- **Small Binary Size**: nanoGo's interpreter is extremely compact, making it ideal for embedded playground scenarios
- **No Runtime Dependencies**: Everything needed to run Go code is bundled in the WASM module
- **Instant Load Times**: Fast initialization means your Go code starts executing quickly

### 💡 **Unique Use Cases**
- **Interactive Tutorials**: Create Go learning platforms that run entirely in the browser
- **Browser-Based IDEs**: Build web-based development environments without server-side execution
- **Client-Side Data Processing**: Perform complex computations on user data without sending it to servers
- **Live Code Demonstrations**: Showcase Go algorithms and patterns with interactive examples
- **Educational Tools**: Teach Go programming with zero installation requirements
- **Prototyping & Experimentation**: Quickly test Go ideas without local setup

## 🌟 Features

### Core Capabilities
- ✅ **Go Language Support**: Variables, functions, structs, interfaces, slices, maps
- ✅ **Concurrency**: Full goroutine and channel support in the browser
- ✅ **Built-in Packages**: `fmt`, `time`, `sync`, `math`, `strings`, `regexp`, `json`, `sort`, and more
- ✅ **Browser Integration**: Special `browser` package for DOM manipulation and canvas drawing
- ✅ **HTTP Client**: Make HTTP requests from Go code in the browser
- ✅ **Template Engine**: `text/template` support for dynamic content generation
- ✅ **Local Storage**: Persist data using browser's localStorage API
- ✅ **Math & Random**: Full `math` and `math/rand` package support

### Execution Modes
- **🌐 Web Playground**: Interactive browser-based Go editor with live execution
- **🖥️ CLI Interpreter**: Run Go scripts from the command line
- **📝 REPL Mode**: Interactive Read-Eval-Print-Loop for experimentation

### Safety Features
- **Sandboxed Execution**: Safe interpreter environment prevents malicious code
- **Controlled Natives**: Limited host function access for security
- **No File System Access**: Browser environment restrictions enforced

## 🎮 Quick Start

### Try It Online

Visit the **[nanoGo Web Playground](#)** to start writing Go code in your browser immediately—no installation required!

### Build Locally

```bash
# Clone the repository
git clone https://github.com/SimonWaldherr/nanoGo.git
cd nanoGo

# Build WebAssembly module for web playground
make build-wasm

# Build native CLI interpreter
make build-cli

# Run a demo
make run-demo
```

### Project Structure

```
nanoGo/
├── cmd/
│   ├── wasm/       # WebAssembly build for browser
│   ├── cli/        # Command-line interpreter
│   └── repl/       # Interactive REPL
├── interp/         # Go interpreter implementation
├── runtime/        # Runtime support (browser APIs, stdlib)
├── samples/        # Example Go programs
└── web/            # Web playground frontend
    ├── index.html
    ├── app.js
    └── nanogo.wasm
```

## 📖 Usage Examples

### Example 1: Hello World

```go
package main

import "fmt"

func main() {
    fmt.Println("Hello from Go in the browser!")
}
```

### Example 2: Goroutines & Channels

```go
package main

import (
    "fmt"
    "sync"
)

func main() {
    ch := make(chan int, 3)
    var wg sync.WaitGroup
    
    wg.Add(1)
    go func() {
        defer wg.Done()
        for i := 0; i < 5; i++ {
            ch <- i * 2
        }
        close(ch)
    }()
    
    for val := range ch {
        fmt.Println("Received:", val)
    }
    
    wg.Wait()
    fmt.Println("Done!")
}
```

### Example 3: Browser DOM Manipulation

```go
package main

import "browser"

func main() {
    // Draw shapes on canvas
    canvas := browser.GetCanvas()
    canvas.FillRect(10, 10, 50, 50, "blue")
    canvas.FillCircle(100, 100, 30, "red")
    
    // Access browser APIs
    browser.Alert("Hello from Go!")
    browser.Log("Debug message from Go")
}
```

### Example 4: Time & Async Operations

```go
package main

import (
    "fmt"
    "time"
)

func main() {
    fmt.Println("Starting timer...")
    start := time.Now()
    
    time.Sleep(1000) // Sleep for 1 second (milliseconds in nanoGo)
    
    elapsed := time.Since(start)
    fmt.Printf("Elapsed: %v\n", elapsed)
}
```

### Example 5: JSON Processing

```go
package main

import (
    "fmt"
    "json"
)

func main() {
    data := map[string]any{
        "name": "nanoGo",
        "version": "1.0",
        "features": []string{"wasm", "browser", "lightweight"},
    }
    
    jsonStr := json.Marshal(data)
    fmt.Println("JSON:", jsonStr)
    
    parsed := json.Unmarshal(jsonStr)
    fmt.Println("Parsed:", parsed)
}
```

## 🏗️ Architecture

### Interpreter Design

nanoGo implements a **tree-walking interpreter** that parses Go source code into an Abstract Syntax Tree (AST) and evaluates it directly:

1. **Lexing & Parsing**: Go source → AST using Go's `go/parser` package
2. **Type Resolution**: Basic type checking and struct/interface definition
3. **Evaluation**: Tree-walking evaluation with environment chaining
4. **Runtime**: Native function bindings for stdlib-like functionality

### WebAssembly Integration

```
┌─────────────────┐
│   Web Browser   │
│                 │
│  ┌───────────┐  │
│  │  HTML/JS  │  │
│  └─────┬─────┘  │
│        │        │
│  ┌─────▼─────┐  │
│  │   WASM    │  │
│  │  (nanoGo) │  │
│  └─────┬─────┘  │
│        │        │
│  ┌─────▼─────┐  │
│  │ Interpreter│ │
│  └─────┬─────┘  │
│        │        │
│  ┌─────▼─────┐  │
│  │  Go Code  │  │
│  └───────────┘  │
└─────────────────┘
```

### Package System

nanoGo includes a curated set of built-in packages:

- **Core**: `fmt`, `sync`, `time`
- **Data**: `json`, `strings`, `regexp`, `sort`
- **Math**: `math`, `math/rand`
- **Text**: `text/template`
- **Web**: `http`, `browser`, `storage`

## 🔨 Building & Development

### Prerequisites

- Go 1.25.0 or later
- Make (optional, for convenience)

### Build Commands

```bash
# Build everything
make all

# Build WebAssembly module only
make build-wasm

# Build CLI interpreter
make build-cli

# Build REPL
make build-repl

# Run tests
make test

# Clean build artifacts
make clean

# Tidy and vet
make tidy
```

### Running the Web Playground

```bash
# Build WASM
make build-wasm

# Serve the web directory (use any HTTP server)
python3 -m http.server 8080 --directory web

# Open http://localhost:8080 in your browser
```

### Testing

```bash
# Run interpreter tests
go test ./interp

# Run CLI tests
go test ./cmd/cli

# Run all tests
make test
```

## 🎯 Use Cases

### 1. **Educational Platforms**
Create interactive Go tutorials where students can write and execute code without installing anything:
- No server-side execution needed
- Instant feedback
- Safe sandbox environment

### 2. **Live Documentation**
Embed executable Go examples directly in documentation:
- Readers can modify and run examples
- Interactive API demonstrations
- Real-time algorithm visualization

### 3. **Browser-Based Tools**
Build sophisticated web applications with Go logic:
- Data processing tools
- Calculators and simulators
- Algorithm visualizers
- Format converters

### 4. **Prototyping & Experimentation**
Quick Go experimentation without local setup:
- Test algorithms
- Explore Go features
- Share code snippets with colleagues

### 5. **Code Challenges & Competitions**
Host programming competitions with Go:
- Browser-based coding environment
- Immediate code execution
- Fair sandboxed evaluation

## 🤝 Contributing

Contributions are welcome! Here's how you can help:

1. **Report Bugs**: Open an issue describing the problem
2. **Suggest Features**: Propose new functionality or improvements
3. **Submit PRs**: Fix bugs or implement features
4. **Improve Docs**: Help make documentation clearer
5. **Share Examples**: Add interesting sample programs

### Development Workflow

```bash
# Fork and clone the repository
git clone https://github.com/YourUsername/nanoGo.git

# Create a feature branch
git checkout -b feature/amazing-feature

# Make your changes and test
make test

# Build and verify
make all

# Commit and push
git commit -m "Add amazing feature"
git push origin feature/amazing-feature

# Open a Pull Request
```

## 📊 Comparison with Alternatives

| Feature | nanoGo | TinyGo | GopherJS |
|---------|--------|--------|----------|
| **Compilation** | Interpreted | AOT to WASM | Transpiled to JS |
| **Binary Size** | Very Small | Small-Medium | Large |
| **Dynamic Execution** | ✅ Yes | ❌ No | ❌ No |
| **Full Go Stdlib** | ❌ Subset | ⚠️ Partial | ✅ Yes |
| **Concurrency** | ✅ Goroutines | ✅ Goroutines | ✅ Goroutines |
| **Performance** | Medium | High | Medium |
| **Use Case** | Playgrounds, REPL | Production apps | Legacy projects |

## 📝 Limitations

- **Subset of Go**: Not all Go features are supported (reflection, CGO, unsafe)
- **Performance**: Interpreted execution is slower than compiled WASM
- **Standard Library**: Limited subset of Go's stdlib available
- **No Reflection**: Advanced reflection features not implemented
- **Browser-Only WASM**: Desktop WASM runtimes not tested

## 🗺️ Roadmap

- [ ] **Enhanced Package Support**: More stdlib packages
- [ ] **Debugger Integration**: Step-through debugging in browser
- [ ] **Performance Optimizations**: JIT compilation, bytecode caching
- [ ] **Module System**: Support for importing external packages
- [ ] **Advanced Types**: Better interface and generics support
- [ ] **IDE Features**: Code completion, syntax highlighting improvements
- [ ] **Testing Framework**: Built-in Go testing support

## 📄 License

nanoGo is licensed under the **GNU General Public License v3.0**. See [LICENSE](LICENSE) for full details.

This means you can:
- ✅ Use nanoGo for commercial projects
- ✅ Modify and distribute nanoGo
- ✅ Use nanoGo in your applications

Under the condition that:
- ⚠️ Derivative works must also be GPL-3.0 licensed
- ⚠️ Source code must be made available

## 🙏 Acknowledgments

- Built with Go's standard `go/parser` and `go/ast` packages
- Inspired by minimal interpreter designs
- WebAssembly support powered by Go's WASM target
- Community contributions and feedback

## 📞 Contact & Links

- **Repository**: [github.com/SimonWaldherr/nanoGo](https://github.com/SimonWaldherr/nanoGo)
- **Author**: Simon Waldherr
- **Issues**: [GitHub Issues](https://github.com/SimonWaldherr/nanoGo/issues)

---

**⭐ Star this project if you find it useful!**

*Bringing the elegance of Go to the browser, one goroutine at a time.*
