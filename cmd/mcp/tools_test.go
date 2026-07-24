package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFmtCodeFormatsSource(t *testing.T) {
	// Deliberately mis-indented, valid Go.
	messy := "package main\n\nimport \"fmt\"\n\nfunc main() {\nfmt.Println(\"hi\")\n}\n"
	result := callTool(t, "fmt_code", map[string]any{"code": messy})
	if result.IsError {
		t.Fatalf("fmt_code returned error: %s", toolText(t, result))
	}
	got := toolText(t, result)
	if !strings.Contains(got, "\tfmt.Println(\"hi\")") {
		t.Fatalf("fmt_code did not gofmt the body; got:\n%s", got)
	}
}

func TestVetCodeReportsAndClears(t *testing.T) {
	// A printf verb/argument mismatch is a deterministic VetSource finding.
	mismatch := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Printf(\"%d %d\\n\", 1)\n}\n"
	result := callTool(t, "vet_code", map[string]any{"code": mismatch})
	if result.IsError {
		t.Fatalf("vet_code returned rpc-level error: %s", toolText(t, result))
	}
	if got := toolText(t, result); !strings.Contains(got, "verb") {
		t.Fatalf("vet_code output = %q, want a printf verb/argument finding", got)
	}

	clean := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Printf(\"%d\\n\", 1)\n}\n"
	result = callTool(t, "vet_code", map[string]any{"code": clean})
	if got := toolText(t, result); got != "ok" {
		t.Fatalf("vet_code clean output = %q, want \"ok\"", got)
	}
}

func TestVFSNavigationTools(t *testing.T) {
	resetSessionVFS(t)

	// mkdir then list the parent and see the new directory.
	if r := callTool(t, "vfs_mkdir", map[string]any{"path": "/proj"}); r.IsError {
		t.Fatalf("vfs_mkdir error: %s", toolText(t, r))
	}
	writeVFSFile(t, "/proj/a.txt", "abcde")

	list := callTool(t, "vfs_list", map[string]any{"path": "/proj"})
	if list.IsError || !strings.Contains(toolText(t, list), "a.txt") {
		t.Fatalf("vfs_list /proj = %+v, want to contain a.txt", list)
	}

	// chdir returns the new absolute working directory.
	cd := callTool(t, "vfs_chdir", map[string]any{"path": "/proj"})
	if cd.IsError || toolText(t, cd) != "/proj" {
		t.Fatalf("vfs_chdir result = %q, want /proj", toolText(t, cd))
	}

	// stat reports type and size for the file.
	stat := callTool(t, "vfs_stat", map[string]any{"path": "/proj/a.txt"})
	if stat.IsError {
		t.Fatalf("vfs_stat error: %s", toolText(t, stat))
	}
	var meta vfsEntry
	decodeToolJSON(t, stat, &meta)
	if meta.Type != "file" || meta.Size != 5 {
		t.Fatalf("vfs_stat = %+v, want file of size 5", meta)
	}
}

func TestVFSRemoveTool(t *testing.T) {
	resetSessionVFS(t)

	// Non-recursive removal of a single file.
	writeVFSFile(t, "/del.txt", "x")
	if r := callTool(t, "vfs_remove", map[string]any{"path": "/del.txt"}); r.IsError {
		t.Fatalf("vfs_remove file error: %s", toolText(t, r))
	}
	if r := callTool(t, "vfs_read", map[string]any{"path": "/del.txt"}); !r.IsError {
		t.Fatal("vfs_read after remove unexpectedly succeeded")
	}

	// A non-empty directory needs recursive=true.
	writeVFSFile(t, "/tree/sub/b.txt", "y")
	if r := callTool(t, "vfs_remove", map[string]any{"path": "/tree"}); !r.IsError {
		t.Fatal("vfs_remove of non-empty dir without recursive should fail")
	}
	if r := callTool(t, "vfs_remove", map[string]any{"path": "/tree", "recursive": true}); r.IsError {
		t.Fatalf("recursive vfs_remove error: %s", toolText(t, r))
	}
}

func TestToolCallErrorPaths(t *testing.T) {
	resetSessionVFS(t)

	// A missing required argument is surfaced as a tool error, not a crash.
	missing := callTool(t, "vfs_read", map[string]any{})
	if !missing.IsError || !strings.Contains(toolText(t, missing), "is required") {
		t.Fatalf("vfs_read without path = %+v, want required-arg error", missing)
	}

	// An unknown tool name is a JSON-RPC error (-32602), not a tool result.
	_, rpcErr := handleToolCall(json.RawMessage(`{"name":"does_not_exist","arguments":{}}`), defaultSession)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("unknown tool rpc error = %+v, want -32602", rpcErr)
	}
}
