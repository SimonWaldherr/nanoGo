package interp

import (
    "strings"
    "testing"
)

func TestFSReadFileAndHTTPGetText(t *testing.T) {
    vm, buf := newTestVM()

    // Host native for file read — return a known string when README.md requested.
    vm.RegisterNative("HostReadFile", func(args []any) (any, error) {
        if len(args) == 0 { return "", nil }
        p := ToString(args[0])
        if p == "README.md" { return "TEST_README_CONTENT", nil }
        return "", nil
    })

    // Host native for HTTPGetText — echo back a marker plus URL.
    vm.RegisterNative("HTTPGetText", func(args []any) (any, error) {
        if len(args) == 0 { return "", nil }
        return "HTTP_OK:" + ToString(args[0]), nil
    })

    src := `package main
import "fmt"
import "fs"
import "http"
func main() {
    s := fs.ReadFile("README.md")
    fmt.Println(s)
    t := http.GetText("http://example")
    fmt.Println(t)
}
`

    if err := vm.Run(src); err != nil {
        t.Fatalf("Run failed: %v", err)
    }

    out := buf.String()
    if !strings.Contains(out, "TEST_README_CONTENT") {
        t.Fatalf("expected fs.ReadFile output, got %q", out)
    }
    if !strings.Contains(out, "HTTP_OK:http://example") {
        t.Fatalf("expected HTTPGetText output, got %q", out)
    }
}
