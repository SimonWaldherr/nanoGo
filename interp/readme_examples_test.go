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
	expected := []string{"{\"answer\":42}\n"}
	if len(examples) != len(expected) {
		t.Fatalf("README has %d Go examples; add expectations for each one", len(examples))
	}
	for index, source := range examples {
		t.Run("example_"+string(rune('1'+index)), func(t *testing.T) {
			vm, output := newTestVM()
			if err := vm.Run(source); err != nil {
				t.Fatalf("README example failed: %v", err)
			}
			if got := output.String(); got != expected[index] {
				t.Errorf("output: got %q, want %q", got, expected[index])
			}
		})
	}
}
