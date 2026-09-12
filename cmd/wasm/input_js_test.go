package main

import (
	"encoding/json"
	"fmt"
	"reflect"
	"syscall/js"
	"testing"
)

func TestWorkspaceJSONInput(t *testing.T) {
	raw := `[{"path":"lib/../main.go","source":"package main // Grüße 🌍"}]`
	a, err := workspaceFilesFromJS(js.ValueOf(raw))
	if err != nil {
		t.Fatal(err)
	}
	b, err := workspaceFilesFromJS(js.Global().Get("JSON").Call("parse", raw))
	if err != nil || !reflect.DeepEqual(a, b) {
		t.Fatalf("%v %v %v", a, b, err)
	}
	for _, invalid := range []string{`[]`, `null`, `[`, `[{"path":"../main.go"}]`, `[{"path":"a.go"},{"path":"./a.go"}]`} {
		if _, err := workspaceFilesFromJS(js.ValueOf(invalid)); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
}

func BenchmarkWorkspaceInput(b *testing.B) {
	files := make([]workspaceFile, 100)
	for i := range files {
		files[i] = workspaceFile{Path: fmt.Sprintf("pkg/file%d.go", i), Source: "package pkg\nfunc Hello() string { return \"Grüße 🌍\" }"}
	}
	raw, _ := json.Marshal(files)
	// The legacy array API uses lower-case property names.
	legacy := make([]any, len(files))
	for i, f := range files {
		legacy[i] = map[string]any{"path": f.Path, "source": f.Source}
	}
	for name, value := range map[string]js.Value{"array": js.ValueOf(legacy), "json": js.ValueOf(string(raw))} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := workspaceFilesFromJS(value); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
