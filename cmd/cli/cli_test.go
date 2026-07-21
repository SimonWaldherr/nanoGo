package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
)

func TestRunSafeHelloWorld(t *testing.T) {
	err := RunSafe(`
package main
import "fmt"
func main() { fmt.Println("hello") }
`, 5*time.Second)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
}

func TestRunSafePanicRecovery(t *testing.T) {
	err := RunSafe(`
package main
func main() { panic("kaboom") }
`, 5*time.Second)
	if err == nil {
		t.Fatal("expected error from panic")
	}
	if !strings.Contains(err.Error(), "kaboom") {
		t.Errorf("expected 'kaboom' in error, got %q", err.Error())
	}
}

func TestRunSafeTimeout(t *testing.T) {
	err := RunSafe(`
package main
import "time"
func main() { time.Sleep(10000) }
`, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Errorf("expected 'timed out' in error, got %q", err.Error())
	}
}

func TestRunSafeSyntaxError(t *testing.T) {
	err := RunSafe(`this is not valid go code at all`, 5*time.Second)
	if err == nil {
		t.Fatal("expected syntax error")
	}
}

func TestRunSafeNoMain(t *testing.T) {
	err := RunSafe(`
package main
func helper() int { return 1 }
`, 5*time.Second)
	if err == nil {
		t.Fatal("expected error for missing main()")
	}
}

func TestRunSafeArithmetic(t *testing.T) {
	err := RunSafe(`
package main
import "fmt"
func main() {
	x := 2 + 3
	fmt.Println(x)
}
`, 5*time.Second)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ---------- fmt / vet helpers ----------

func writeTempFile(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "nanogo_*.go")
	if err != nil {
		t.Fatalf("CreateTemp: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write: %v", err)
	}
	f.Close()
	return f.Name()
}

// captureStdout redirects os.Stdout to a pipe for the duration of fn and
// returns everything written to it. runFmt/runVet print directly to
// os.Stdout, so this is required to assert on their actual output; it must
// only be used with inputs that are known not to hit an os.Exit call inside
// fn (an os.Exit here would kill the whole test process without restoring
// os.Stdout or running deferred cleanups).
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	origStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stdout = w
	defer func() { os.Stdout = origStdout }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	return <-done
}

func TestRunFmtFile(t *testing.T) {
	// Deliberately messy formatting; gofmt should expand it onto separate
	// lines. This exercises runFmt itself (not just interp.FormatSource),
	// asserting on what actually reaches stdout.
	path := writeTempFile(t, "package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"hello\")}\n")
	out := captureStdout(t, func() { runFmt(path) })
	if !strings.Contains(out, "func main() {") {
		t.Errorf("expected gofmt'd output containing 'func main() {', got %q", out)
	}
	if !strings.Contains(out, "fmt.Println(\"hello\")") {
		t.Errorf("expected formatted output to preserve the Println call, got %q", out)
	}
}

func TestRunVetFileClean(t *testing.T) {
	// runVet only calls os.Exit(1) when issues are found, so a clean program
	// is the only case safe to run in-process; the issues-found/exit-code
	// path is covered by the subprocess-based TestCLIBinaryFmtVetUsage below.
	path := writeTempFile(t, `package main
import "fmt"
func main() { fmt.Println("hello") }
`)
	out := captureStdout(t, func() { runVet(path) })
	if out != "" {
		t.Errorf("expected no vet output for clean code, got %q", out)
	}
}

func TestRunFileSubcommandRouting(t *testing.T) {
	// Verify the runFile/RunSafe path works for valid code.
	err := RunSafe("package main\nfunc main(){}\n", time.Second)
	if err != nil {
		// An empty main() is valid; no error expected.
		t.Fatalf("unexpected error for valid empty main: %v", err)
	}
}

// ---------- HostReadFile whitelist / path-traversal ----------

// newSafeNativeTestVM builds an interpreter wired exactly like
// RunSafeWithCapabilities (registerSafeNatives + RegisterBuiltinPackages),
// except that ConsoleLog is overridden to capture guest fmt.Println output
// into a buffer instead of printing to the real os.Stdout -- mirroring the
// newTestVM() helper pattern used by interp/interp_test.go.
func newSafeNativeTestVM(capabilities interp.Capabilities) (*interp.Interpreter, *strings.Builder) {
	vm := interp.NewInterpreter()
	vm.Capabilities = capabilities
	registerSafeNatives(vm)

	var buf strings.Builder
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) {
		if len(args) > 0 {
			buf.WriteString(interp.ToString(args[0]))
			buf.WriteByte('\n')
		}
		return nil, nil
	})

	interp.RegisterBuiltinPackages(vm)
	return vm, &buf
}

// chdirRepoRoot changes the process working directory to the repo root
// (two levels up from cmd/cli, where `go test` runs by default) for the
// duration of the test, restoring it afterwards. HostReadFile's whitelist
// (samples, web, README.md, LICENSE) is anchored to the repo root via
// os.Getwd(), so tests that expect a whitelisted read to actually succeed
// need to run from there.
func chdirRepoRoot(t *testing.T) {
	t.Helper()
	origWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	repoRoot, err := filepath.Abs(filepath.Join(origWd, "..", ".."))
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("Chdir(%s): %v", repoRoot, err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(origWd); err != nil {
			t.Fatalf("Chdir restore(%s): %v", origWd, err)
		}
	})
}

// runHostReadFile invokes HostReadFile (installed by registerSafeNatives in
// main.go) through fs.ReadFile(path) with a FileSystem.Read capability
// granted — the real, only-reachable route: HostReadFile is registered with
// RegisterInternalNative, so guest code can no longer call it directly by
// bare name (see TestHostReadFileDirectCallBypassesFilesystemCapability).
// fs.ReadFile resolves path through the VFS first (interp/packages.go ->
// vm.requireFileRead -> VFS.ResolvePath), producing an absolute VFS-style
// path such as "/home/user/README.md"; HostReadFile strips that default VFS
// home prefix before applying its own repo-relative whitelist/traversal
// logic (main.go:181-217) — exactly the logic this test suite covers.
func runHostReadFile(t *testing.T, path string) string {
	t.Helper()
	vm, buf := newSafeNativeTestVM(interp.Capabilities{FileSystem: interp.FileSystemCapabilities{Read: true}})
	src := fmt.Sprintf(`package main
import "fmt"
import "fs"
func main() {
	s, err := fs.ReadFile(%q)
	if err != nil {
		// Guest code cannot call err.Error() here (nanoGo's method dispatch
		// only supports methods on interpreter-native struct types, not on a
		// native-returned Go error value) -- passing err straight to
		// fmt.Println lets ToString's fmt.Sprintf("%%v", ...) fallback
		// stringify it via the standard Go error formatting instead.
		fmt.Println("ERR:", err)
		return
	}
	fmt.Println("OK:" + s)
}
`, path)
	if err := vm.Run(src); err != nil {
		t.Fatalf("guest program error for path %q: %v", path, err)
	}
	return buf.String()
}

func TestHostReadFileWhitelistAndTraversal(t *testing.T) {
	chdirRepoRoot(t)
	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}

	tests := []struct {
		name       string
		path       string
		wantOK     bool
		wantErrSub string
	}{
		{
			name:   "whitelisted top-level file succeeds",
			path:   "README.md",
			wantOK: true,
		},
		{
			name:       "absolute path is rejected",
			path:       filepath.Join(repoRoot, "README.md"),
			wantErrSub: "access denied",
		},
		{
			name:       "parent directory traversal is rejected",
			path:       "../../../etc/passwd",
			wantErrSub: "access denied",
		},
		{
			name:       "non-whitelisted top-level folder is rejected",
			path:       "secret/file.txt",
			wantErrSub: "access denied: path not in whitelist",
		},
		{
			name:       "sibling folder sharing the whitelist prefix is rejected",
			path:       "samples-evil/x",
			wantErrSub: "access denied: path not in whitelist",
		},
	}

	// Case 5 from the audit ("sibling-directory bypass against the final
	// wd-prefix check") is not independently reachable through this
	// interface under the current implementation: full is always built as
	// filepath.Join(wd, clean) (main.go:207), which inserts a path
	// separator between wd and clean, and clean has already been rejected
	// above if it starts with (or is adjacent to a separator next to) "..".
	// So no value of clean that passes the whitelist check above can make
	// Join(wd, clean) escape wd today. The cases above exercise every layer
	// that would need to weaken in combination for that check to become
	// reachable/exploitable, per the audit note.

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runHostReadFile(t, tc.path)
			if tc.wantOK {
				if !strings.HasPrefix(got, "OK:") {
					t.Fatalf("expected success reading %q, got %q", tc.path, got)
				}
				if !strings.Contains(got, "nanoGo") {
					t.Fatalf("expected README contents in output, got %q", got)
				}
				return
			}
			if !strings.HasPrefix(got, "ERR:") {
				t.Fatalf("expected denial reading %q, got %q", tc.path, got)
			}
			if tc.wantErrSub != "" && !strings.Contains(got, tc.wantErrSub) {
				t.Fatalf("expected error containing %q, got %q", tc.wantErrSub, got)
			}
		})
	}
}

// TestFSReadFileWhitelistedPathViaCapability proves the intended,
// capability-gated route actually works: fs.ReadFile resolves the guest path
// through the VFS first (interp/packages.go -> vm.requireFileRead ->
// VFS.ResolvePath), producing an absolute VFS-style path like
// "/home/user/README.md" — main.go's HostReadFile strips that default VFS
// home prefix before applying its repo-relative whitelist, so a whitelisted
// relative path succeeds end-to-end through the real capability check.
func TestFSReadFileWhitelistedPathViaCapability(t *testing.T) {
	chdirRepoRoot(t)

	got := runHostReadFile(t, "README.md")
	if !strings.HasPrefix(got, "OK:") || !strings.Contains(got, "nanoGo") {
		t.Fatalf("expected README contents via whitelisted fs.ReadFile, got %q", got)
	}
}

// runHostReadFileDirectly builds a VM with the given Capabilities and tries
// to call HostReadFile(...) directly as a bare guest identifier — the call
// shape TestHostReadFileDirectCallBypassesFilesystemCapability proves is no
// longer possible at all, regardless of Capabilities.
func runHostReadFileDirectly(t *testing.T, capabilities interp.Capabilities, path string) string {
	t.Helper()
	vm, buf := newSafeNativeTestVM(capabilities)
	src := fmt.Sprintf(`package main
import "fmt"
func main() {
	s, err := HostReadFile(%q)
	if err != nil {
		fmt.Println("ERR:", err)
		return
	}
	fmt.Println("OK:" + s)
}
`, path)
	if err := vm.Run(src); err != nil {
		t.Fatalf("guest program error for path %q: %v", path, err)
	}
	return buf.String()
}

// TestHostReadFileDirectCallBypassesFilesystemCapability proves
// HostReadFile is no longer reachable as a bare guest identifier: main.go
// registers it with RegisterInternalNative (interp/environment.go), which
// neither declares it as a guest identifier nor adds it to vm's
// guest-visible natives table that evalExpr's *ast.Ident case falls back to
// — so calling HostReadFile(...) directly from guest source now fails with
// "undefined: HostReadFile", the same as calling any other undeclared name,
// regardless of the host's configured Capabilities.
func TestHostReadFileDirectCallBypassesFilesystemCapability(t *testing.T) {
	chdirRepoRoot(t)

	got := runHostReadFileDirectly(t, interp.Capabilities{}, "README.md")
	if !strings.HasPrefix(got, "ERR:") {
		t.Fatalf("expected direct HostReadFile call to be denied like fs.ReadFile is under a zero Capabilities policy, got %q", got)
	}
	if !strings.Contains(got, "undefined: HostReadFile") {
		t.Errorf("expected the denial to be an \"undefined: HostReadFile\" identifier error (no longer guest-callable at all), got %q", got)
	}
}

// ---------- cmd/cli binary entrypoint (main/runFmt/runVet/printUsage) ----------

// asExitError unwraps err into an *exec.ExitError, if it is one.
func asExitError(err error) (*exec.ExitError, bool) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr, true
	}
	return nil, false
}

// TestCLIBinaryFmtVetUsage builds the real cmd/cli binary and exercises its
// os.Args dispatch (fmt/vet/default-run) end to end, including exit codes
// that cannot be observed from an in-process call to main()'s helpers since
// they call os.Exit directly. Skips (rather than fails) if a compatible `go`
// toolchain isn't available, matching the convention in
// interp/loader/test_test.go.
func TestCLIBinaryFmtVetUsage(t *testing.T) {
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no `go` on PATH, skipping cmd/cli binary integration test")
	}

	binPath := filepath.Join(t.TempDir(), "nanogo-cli-test")
	if runtime.GOOS == "windows" {
		binPath += ".exe"
	}
	if out, err := exec.Command(goBin, "build", "-o", binPath, ".").CombinedOutput(); err != nil {
		t.Skipf("go build cmd/cli unavailable/incompatible in this environment (%v); output:\n%s", err, out)
	}

	t.Run("fmt_formats_source_on_stdout", func(t *testing.T) {
		src := writeTempFile(t, "package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"hello\")}\n")
		out, err := exec.Command(binPath, "fmt", src).CombinedOutput()
		if err != nil {
			t.Fatalf("fmt subcommand failed: %v\noutput:\n%s", err, out)
		}
		if !strings.Contains(string(out), "func main() {") {
			t.Errorf("expected gofmt'd output containing 'func main() {', got %q", out)
		}
	})

	t.Run("vet_reports_unreachable_code_and_exits_1", func(t *testing.T) {
		src := writeTempFile(t, "package main\nfunc main() {\n\treturn\n\tprintln(\"unreachable\")\n}\n")
		out, err := exec.Command(binPath, "vet", src).CombinedOutput()
		exitErr, ok := asExitError(err)
		if !ok {
			t.Fatalf("expected vet to exit non-zero for unreachable code, err=%v output:\n%s", err, out)
		}
		if exitErr.ExitCode() != 1 {
			t.Errorf("expected exit code 1, got %d", exitErr.ExitCode())
		}
		if !strings.Contains(string(out), "unreachable code") {
			t.Errorf("expected vet output to mention unreachable code, got %q", out)
		}
	})

	t.Run("no_args_prints_usage_and_exits_1", func(t *testing.T) {
		out, err := exec.Command(binPath).CombinedOutput()
		exitErr, ok := asExitError(err)
		if !ok {
			t.Fatalf("expected non-zero exit with no args, err=%v output:\n%s", err, out)
		}
		if exitErr.ExitCode() != 1 {
			t.Errorf("expected exit code 1, got %d", exitErr.ExitCode())
		}
		if !strings.Contains(string(out), "usage:") {
			t.Errorf("expected usage message, got %q", out)
		}
	})
}
