package interp

import (
	"strings"
	"testing"
)

func TestFormatSource(t *testing.T) {
	src := `package main
import "fmt"
func main(){fmt.Println("hello")}
`
	formatted, err := FormatSource(src)
	if err != nil {
		t.Fatalf("FormatSource returned error: %v", err)
	}
	// gofmt expands the body onto separate lines.
	if !strings.Contains(formatted, "func main() {") {
		t.Errorf("expected formatted output, got %q", formatted)
	}
}

func TestFormatSourceInvalidCode(t *testing.T) {
	src := `this is not valid go code`
	_, err := FormatSource(src)
	if err == nil {
		t.Error("expected error for invalid source, got nil")
	}
}

func TestVetSourceClean(t *testing.T) {
	src := `package main
import "fmt"
func main() {
	fmt.Println("hello")
}
`
	issues, err := VetSource(src)
	if err != nil {
		t.Fatalf("VetSource error: %v", err)
	}
	if len(issues) != 0 {
		t.Errorf("expected no issues for clean code, got: %v", issues)
	}
}

func TestVetSourceUnreachableCode(t *testing.T) {
	src := `package main
func foo() {
	return
	println("unreachable")
}
func main() {}
`
	issues, err := VetSource(src)
	if err != nil {
		t.Fatalf("VetSource error: %v", err)
	}
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Message, "unreachable") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unreachable code issue, got: %v", issues)
	}
}

func TestVetSourceUnreachableAfterPanic(t *testing.T) {
	src := `package main
func foo() {
	panic("boom")
	println("unreachable")
}
func main() {}
`
	issues, err := VetSource(src)
	if err != nil {
		t.Fatalf("VetSource error: %v", err)
	}
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Message, "unreachable") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected unreachable code after panic, got: %v", issues)
	}
}

func TestVetSourcePrintfMismatch(t *testing.T) {
	src := `package main
import "fmt"
func main() {
	fmt.Printf("%d %s", 42)
}
`
	issues, err := VetSource(src)
	if err != nil {
		t.Fatalf("VetSource error: %v", err)
	}
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Message, "fmt.Printf") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected printf mismatch issue, got: %v", issues)
	}
}

func TestVetSourcePrintfCorrect(t *testing.T) {
	src := `package main
import "fmt"
func main() {
	fmt.Printf("%d %s", 42, "hello")
}
`
	issues, err := VetSource(src)
	if err != nil {
		t.Fatalf("VetSource error: %v", err)
	}
	for _, issue := range issues {
		if strings.Contains(issue.Message, "fmt.Printf") {
			t.Errorf("unexpected printf issue on correct call: %v", issue)
		}
	}
}

func TestVetSourceSelfAssignment(t *testing.T) {
	src := `package main
func main() {
	x := 5
	x = x
	_ = x
}
`
	issues, err := VetSource(src)
	if err != nil {
		t.Fatalf("VetSource error: %v", err)
	}
	found := false
	for _, issue := range issues {
		if strings.Contains(issue.Message, "self-assignment") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected self-assignment issue, got: %v", issues)
	}
}

func TestVetSourceParseError(t *testing.T) {
	src := `this is not valid go`
	_, err := VetSource(src)
	if err == nil {
		t.Error("expected parse error for invalid source")
	}
}

func TestVetIssueString(t *testing.T) {
	issue := VetIssue{Line: 5, Column: 10, Message: "something wrong"}
	got := issue.String()
	if !strings.Contains(got, "5") || !strings.Contains(got, "10") || !strings.Contains(got, "something wrong") {
		t.Errorf("VetIssue.String() = %q, unexpected format", got)
	}
}

func TestCountPrintfVerbs(t *testing.T) {
	cases := []struct {
		format string
		want   int
	}{
		{"%d", 1},
		{"%d %s", 2},
		{"%%", 0},
		{"%d%%", 1},
		{"hello", 0},
		{"%v %v %v", 3},
		// Width and precision specifiers must count as a single verb each.
		{"%5d", 1},
		{"%.2f", 1},
		{"%-10s", 1},
		{"%05.2f", 1},
		{"%5d %s", 2},
		{"% d", 1},  // space flag
		{"%+d", 1},  // plus flag
		{"%#v", 1},  // hash flag
	}
	for _, c := range cases {
		got := countPrintfVerbs(c.format)
		if got != c.want {
			t.Errorf("countPrintfVerbs(%q) = %d, want %d", c.format, got, c.want)
		}
	}
}
