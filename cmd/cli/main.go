package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	switch os.Args[1] {
	case "fmt":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: nanogo-cli fmt <file.go>")
			os.Exit(1)
		}
		runFmt(os.Args[2])
	case "vet":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "usage: nanogo-cli vet <file.go>")
			os.Exit(1)
		}
		runVet(os.Args[2])
	default:
		// Original behaviour: nanogo-cli <file.go> [timeout-seconds]
		runFile(os.Args[1], os.Args[2:])
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: nanogo-cli <file.go> [timeout-seconds]")
	fmt.Fprintln(os.Stderr, "       nanogo-cli fmt <file.go>")
	fmt.Fprintln(os.Stderr, "       nanogo-cli vet <file.go>")
}

// runFmt prints the gofmt-formatted version of file to stdout.
func runFmt(path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read error:", err)
		os.Exit(1)
	}
	formatted, err := interp.FormatSource(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, "format error:", err)
		os.Exit(1)
	}
	fmt.Print(formatted)
}

// runVet prints vet issues for file and exits with code 1 if any are found.
func runVet(path string) {
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read error:", err)
		os.Exit(1)
	}
	issues, err := interp.VetSource(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, "parse error:", err)
		os.Exit(1)
	}
	for _, issue := range issues {
		fmt.Printf("%s:%s\n", path, issue)
	}
	if len(issues) > 0 {
		os.Exit(1)
	}
}

// runFile executes a Go source file in the interpreter (original behaviour).
func runFile(path string, extraArgs []string) {
	src, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "read error:", err)
		os.Exit(1)
	}

	timeout := 10 * time.Second
	if len(extraArgs) >= 1 {
		d, err := time.ParseDuration(extraArgs[0] + "s")
		if err == nil {
			timeout = d
		}
	}

	if err := RunSafe(string(src), timeout); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}

// RunSafe executes untrusted Go source inside the nanoGo interpreter
// with a context-based timeout. It recovers from panics so the host
// application is never crashed by user code.
func RunSafe(source string, timeout time.Duration) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("panic recovered: %v", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- runInterpreted(source)
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("execution timed out after %s", timeout)
	}
}

// runInterpreted creates a sandboxed interpreter, registers only the
// host functions we choose to expose, and executes the source.
func runInterpreted(source string) error {
	vm := interp.NewInterpreter()
	registerSafeNatives(vm)
	interp.RegisterBuiltinPackages(vm)
	return vm.Run(source)
}

// registerSafeNatives installs only the minimal set of host functions
// needed for console output. Anything dangerous (file access, network,
// DOM, etc.) is intentionally omitted.
func registerSafeNatives(vm *interp.Interpreter) {
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Println(interp.ToString(args[0]))
		}
		return nil, nil
	})

	vm.RegisterNative("ConsoleWarn", func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Fprintln(os.Stderr, "[warn]", interp.ToString(args[0]))
		}
		return nil, nil
	})

	vm.RegisterNative("ConsoleError", func(args []any) (any, error) {
		if len(args) > 0 {
			fmt.Fprintln(os.Stderr, "[error]", interp.ToString(args[0]))
		}
		return nil, nil
	})

	vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		format := interp.ToString(args[0])
		fmtArgs := make([]any, 0, len(args)-1)
		for _, a := range args[1:] {
			fmtArgs = append(fmtArgs, a)
		}
		return fmt.Sprintf(format, fmtArgs...), nil
	})

	// Host-proxied read-only file access (whitelist).
	vm.RegisterNative("HostReadFile", func(args []any) (any, error) {
		if len(args) == 0 { return "", nil }
		p := interp.ToString(args[0])
		// Clean and forbid absolute or upward paths
		clean := filepath.Clean(p)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") || strings.Contains(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("access denied: absolute or parent paths not allowed")
		}
		// Whitelist top-level folders/files allowed to be read
		allowed := []string{"samples", "web", "README.md", "LICENSE"}
		ok := false
		for _, a := range allowed {
			if clean == a || strings.HasPrefix(clean, a+string(filepath.Separator)) {
				ok = true; break
			}
		}
		if !ok { return nil, fmt.Errorf("access denied: path not in whitelist") }
		wd, err := os.Getwd()
		if err != nil { return nil, err }
		full := filepath.Join(wd, clean)
		// Ensure the file is inside the repo working directory
		if !strings.HasPrefix(full, wd) { return nil, fmt.Errorf("access denied") }
		b, err := os.ReadFile(full)
		if err != nil { return nil, err }
		return string(b), nil
	})

	// Host-proxied HTTP client (simple rate-limited GetText and PostText)
	var httpMu sync.Mutex
	var lastReq time.Time
	minInterval := 200 * time.Millisecond

	doHTTP := func(method, url, body, contentType string) (string, error) {
		httpMu.Lock()
		now := time.Now()
		if !lastReq.IsZero() {
			wait := minInterval - now.Sub(lastReq)
			if wait > 0 {
				httpMu.Unlock()
				time.Sleep(wait)
				httpMu.Lock()
			}
		}
		lastReq = time.Now()
		httpMu.Unlock()

		client := &http.Client{Timeout: 5 * time.Second}
		var resp *http.Response
		var err error
		if method == "POST" {
			resp, err = client.Post(url, contentType, strings.NewReader(body))
		} else {
			resp, err = client.Get(url)
		}
		if err != nil { return "", err }
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("HTTP status %d", resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil { return "", err }
		return string(data), nil
	}

	vm.RegisterNative("HTTPGetText", func(args []any) (any, error) {
		if len(args) == 0 { return "", nil }
		return doHTTP("GET", interp.ToString(args[0]), "", "")
	})

	vm.RegisterNative("HTTPPostText", func(args []any) (any, error) {
		if len(args) < 2 { return "", nil }
		contentType := "application/json"
		if len(args) >= 3 { contentType = interp.ToString(args[2]) }
		return doHTTP("POST", interp.ToString(args[0]), interp.ToString(args[1]), contentType)
	})
}
