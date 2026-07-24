package interp

import (
	"os"
	"strings"
	"testing"
)

func readmeGoExamples(t *testing.T) []string {
	t.Helper()
	contents, err := os.ReadFile("../README.md")
	if err != nil {
		t.Fatalf("read README: %v", err)
	}
	// Normalize line endings so the fence matching below works regardless of
	// how the README was checked out. On Windows (git core.autocrlf=true) the
	// working tree uses CRLF, which would otherwise leave a trailing "\r" on
	// every line and stop "```go" fences from matching.
	normalized := strings.ReplaceAll(string(contents), "\r\n", "\n")

	var examples []string
	var source strings.Builder
	inGoBlock := false
	for _, line := range strings.Split(normalized, "\n") {
		switch {
		case line == "```go":
			if inGoBlock {
				t.Fatal("nested Go code block in README")
			}
			inGoBlock = true
			source.Reset()
		case line == "```" && inGoBlock:
			// README also contains host-integration snippets. Only complete
			// nanoGo programs (which begin with package main) can be executed by
			// this interpreter-level regression test.
			if strings.HasPrefix(strings.TrimSpace(source.String()), "package main") {
				examples = append(examples, source.String())
			}
			inGoBlock = false
		case inGoBlock:
			source.WriteString(line)
			source.WriteByte('\n')
		}
	}
	if inGoBlock {
		t.Fatal("unterminated Go code block in README")
	}
	return examples
}

func TestReadmeGoExamples(t *testing.T) {
	examples := readmeGoExamples(t)
	if len(examples) != 7 {
		t.Fatalf("README has %d Go examples; add expectations for each one", len(examples))
	}

	for index, source := range examples {
		t.Run("example_"+string(rune('1'+index)), func(t *testing.T) {
			vm, output := newTestVM()
			var browserCalls []string
			for _, native := range []string{"SetInnerHTML", "CanvasSize", "CanvasSet", "CanvasFlush"} {
				native := native
				vm.RegisterNative(native, func(args []any) (any, error) {
					browserCalls = append(browserCalls, native)
					return nil, nil
				})
			}

			if err := vm.Run(source); err != nil {
				t.Fatalf("README example failed: %v", err)
			}

			got := output.String()
			switch index {
			case 0:
				if got != "Hello from Go in the browser!\n" {
					t.Errorf("unexpected output: %q", got)
				}
			case 1:
				want := "Received: 0\nReceived: 2\nReceived: 4\nReceived: 6\nReceived: 8\nDone!\n"
				if got != want {
					t.Errorf("unexpected output: %q", got)
				}
			case 2:
				if got != "Canvas updated\n" {
					t.Errorf("unexpected output: %q", got)
				}
				wantCalls := []string{"SetInnerHTML", "CanvasSize", "CanvasSet", "CanvasSet", "CanvasFlush"}
				if strings.Join(browserCalls, ",") != strings.Join(wantCalls, ",") {
					t.Errorf("browser calls: got %v, want %v", browserCalls, wantCalls)
				}
			case 3:
				if !strings.HasPrefix(got, "Starting timer...\nElapsed: ") {
					t.Errorf("unexpected output: %q", got)
				}
			case 4:
				if !strings.Contains(got, "JSON: {\"features\":[\"wasm\",\"browser\",\"lightweight\"],\"name\":\"nanoGo\",\"version\":\"1.0\"}") {
					t.Errorf("JSON output did not contain the documented data: %q", got)
				}
				if !strings.Contains(got, "Parsed: map[features:[wasm browser lightweight] name:nanoGo version:1.0]") {
					t.Errorf("unexpected parsed JSON output: %q", got)
				}
			case 5:
				if got != "square: 4\nsquare: 9\n" {
					t.Errorf("unexpected output: %q", got)
				}
			case 6:
				if got != "work\ncleanup 2\ncleanup 1\n" {
					t.Errorf("unexpected output: %q", got)
				}
			}
		})
	}
}
