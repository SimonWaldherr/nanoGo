package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
	"simonwaldherr.de/go/nanogo/interp/loader"
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
	case "test":
		if len(os.Args) < 3 || len(os.Args) > 4 {
			fmt.Fprintln(os.Stderr, "usage: nanogo-cli test <module-dir> [package-name]")
			os.Exit(1)
		}
		packageName := ""
		if len(os.Args) == 4 {
			packageName = os.Args[3]
		}
		if err := runModuleTests(os.Args[2], packageName); err != nil {
			fmt.Fprintln(os.Stderr, "test error:", err)
			os.Exit(1)
		}
	default:
		// Original behaviour: nanogo-cli <file.go> [timeout-seconds]
		runFile(os.Args[1], os.Args[2:])
	}
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: nanogo-cli <file.go> [timeout-seconds]")
	fmt.Fprintln(os.Stderr, "       nanogo-cli fmt <file.go>")
	fmt.Fprintln(os.Stderr, "       nanogo-cli vet <file.go>")
	fmt.Fprintln(os.Stderr, "       nanogo-cli test <module-dir> [package-name]")
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

// runModuleTests snapshots a host module into a read-only VFS and runs the
// loader's supported testing.T subset. This deliberately is not `go test`:
// it never executes the host compiler, downloads modules, or grants guest
// code direct host-disk access.
func runModuleTests(moduleDir, packageName string) error {
	vfs := interp.NewVFS()
	if err := vfs.ImportDir("/module", moduleDir, interp.VFSImportOptions{ReadOnly: true}); err != nil {
		return fmt.Errorf("import module: %w", err)
	}
	prog, err := loader.LoadModule(vfs, "/module", loader.Options{})
	if err != nil {
		return err
	}

	vm := interp.NewInterpreterWithVFS(vfs)
	vm.Capabilities.FileSystem.Read = true
	registerSafeNatives(vm)
	interp.RegisterBuiltinPackages(vm)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	packages := prog.Order
	if packageName != "" {
		dir, ok := findPackageDir(prog, packageName)
		if !ok {
			return fmt.Errorf("package %q not found", packageName)
		}
		packages = []string{dir}
	}

	failed := 0
	for _, dir := range packages {
		name := prog.Packages[dir].Name
		results, err := loader.RunPackageTests(ctx, vm, prog, name)
		if err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}
		if len(results) == 0 {
			fmt.Printf("?\t%s\t[no nanoGo tests]\n", name)
			continue
		}
		packageFailed := 0
		for _, result := range results {
			if !result.Pass {
				packageFailed++
				fmt.Printf("FAIL\t%s\t%s\t%d:%d %s\n", name, result.Name, result.Line, result.Column, result.Category)
				for _, message := range result.Messages {
					fmt.Printf("\t%s\n", message)
				}
			}
		}
		if packageFailed == 0 {
			fmt.Printf("ok\t%s\t%d test(s)\n", name, len(results))
		} else {
			failed += packageFailed
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d test(s) failed", failed)
	}
	return nil
}

func findPackageDir(prog *loader.Program, packageName string) (string, bool) {
	for _, dir := range prog.Order {
		if prog.Packages[dir].Name == packageName {
			return dir, true
		}
	}
	return "", false
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
func RunSafe(source string, timeout time.Duration) error {
	return RunSafeWithCapabilities(source, timeout, interp.Capabilities{})
}

// RunSafeWithCapabilities executes guest code with an explicit capability
// policy. RunSafe uses the zero policy, which denies the curated filesystem and
// HTTP packages even if the CLI host has registered matching natives.
func RunSafeWithCapabilities(source string, timeout time.Duration, capabilities interp.Capabilities) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("panic recovered: %v", r)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err := runInterpreted(ctx, source, capabilities)
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("execution timed out after %s: %w", timeout, err)
	}
	return err
}

// runInterpreted creates a sandboxed interpreter, registers only the
// host functions we choose to expose, and executes the source.
func runInterpreted(ctx context.Context, source string, capabilities interp.Capabilities) error {
	vm := interp.NewInterpreter()
	vm.Capabilities = capabilities
	registerSafeNatives(vm)
	interp.RegisterBuiltinPackages(vm)
	return vm.RunContext(ctx, source)
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

	// Host-proxied read-only file access (whitelist). This is registered
	// with RegisterInternalNative, not RegisterNative: it must only ever be
	// reachable through fs.ReadFile's capability check (interp/packages.go),
	// never as a directly guest-callable HostReadFile(...) identifier —
	// registering it with RegisterNative would let guest code bypass the
	// capability check (and this whitelist) entirely.
	vm.RegisterInternalNative("HostReadFile", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		p := interp.ToString(args[0])
		// fs.ReadFile resolves the guest-supplied path through the VFS's own
		// cwd first (see interp.Capabilities/requireFileRead), so p arrives
		// as an absolute VFS-style path such as "/home/user/README.md", not
		// the bare relative path a guest wrote — the same convention
		// interp's own tests pin (interp/fs_http_test.go,
		// TestFSReadFilePassesCanonicalAuthorizedPathToHost in
		// interp/interp_test.go). Strip that default VFS home prefix before
		// applying the repo-relative whitelist below. A path outside that
		// prefix (e.g. the guest changed the VFS cwd via os.Chdir first)
		// is left as-is and simply won't match the whitelist.
		p = strings.TrimPrefix(p, "/home/user/")
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
				ok = true
				break
			}
		}
		if !ok {
			return nil, fmt.Errorf("access denied: path not in whitelist")
		}
		wd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		full := filepath.Join(wd, clean)
		// Ensure the file is inside the repo working directory
		if !strings.HasPrefix(full, wd) {
			return nil, fmt.Errorf("access denied")
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return nil, err
		}
		return string(b), nil
	})

	// Host-proxied HTTP client (simple rate-limited GetText and PostText)
	var httpMu sync.Mutex
	var lastReq time.Time
	minInterval := 200 * time.Millisecond

	doHTTP := func(ctx context.Context, method, url, body, contentType string) (string, error) {
		httpMu.Lock()
		now := time.Now()
		if !lastReq.IsZero() {
			wait := minInterval - now.Sub(lastReq)
			if wait > 0 {
				httpMu.Unlock()
				timer := time.NewTimer(wait)
				select {
				case <-timer.C:
				case <-ctx.Done():
					timer.Stop()
					return "", ctx.Err()
				}
				httpMu.Lock()
			}
		}
		lastReq = time.Now()
		httpMu.Unlock()

		client := &http.Client{Timeout: 5 * time.Second}
		var request *http.Request
		var err error
		if method == "POST" {
			request, err = http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader(body))
			if err == nil {
				request.Header.Set("Content-Type", contentType)
			}
		} else {
			request, err = http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		}
		if err != nil {
			return "", err
		}
		resp, err := client.Do(request)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return "", fmt.Errorf("HTTP status %d", resp.StatusCode)
		}
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	// Registered with RegisterInternalNativeContext (not
	// RegisterNativeContext) for the same reason as HostReadFile above:
	// these must only be reachable through http.GetText/PostText's
	// capability check (interp/packages.go), never as bare guest-callable
	// identifiers.
	vm.RegisterInternalNativeContext("HTTPGetText", func(ctx context.Context, args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		return doHTTP(ctx, "GET", interp.ToString(args[0]), "", "")
	})

	vm.RegisterInternalNativeContext("HTTPPostText", func(ctx context.Context, args []any) (any, error) {
		if len(args) < 2 {
			return "", nil
		}
		contentType := "application/json"
		if len(args) >= 3 {
			contentType = interp.ToString(args[2])
		}
		return doHTTP(ctx, "POST", interp.ToString(args[0]), interp.ToString(args[1]), contentType)
	})
}
