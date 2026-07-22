package main

import (
	"encoding/json"
	"strings"
	"testing"

	"simonwaldherr.de/go/nanogo/interp"
)

func TestInitializeNegotiatesProtocolAndAdvertisesGuide(t *testing.T) {
	result, rpcErr := handleInitialize(json.RawMessage(`{"protocolVersion":"2025-11-25","clientInfo":{"name":"test","version":"1"}}`))
	if rpcErr != nil {
		t.Fatalf("handleInitialize returned error: %+v", rpcErr)
	}
	response, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("initialize result type = %T, want map", result)
	}
	if got := response["protocolVersion"]; got != "2025-11-25" {
		t.Fatalf("protocolVersion = %v, want requested supported version", got)
	}
	capabilities := response["capabilities"].(map[string]any)
	if _, ok := capabilities["tools"]; !ok {
		t.Fatal("initialize response does not advertise tools")
	}
	if _, ok := capabilities["resources"]; !ok {
		t.Fatal("initialize response does not advertise resources")
	}

	result, rpcErr = handleInitialize(json.RawMessage(`{"protocolVersion":"2099-01-01"}`))
	if rpcErr != nil {
		t.Fatalf("handleInitialize fallback returned error: %+v", rpcErr)
	}
	response = result.(map[string]any)
	if got := response["protocolVersion"]; got != latestProtocolVersion {
		t.Fatalf("fallback protocolVersion = %v, want %s", got, latestProtocolVersion)
	}
}

func TestGuideResourceIsDiscoverableAndReadable(t *testing.T) {
	result, rpcErr := handleResourcesList()
	if rpcErr != nil {
		t.Fatalf("handleResourcesList returned error: %+v", rpcErr)
	}
	resources := result.(map[string]any)["resources"].([]mcpResource)
	if len(resources) != 1 || resources[0].URI != guideURI {
		t.Fatalf("resources = %+v, want guide resource", resources)
	}

	result, rpcErr = handleResourcesRead(json.RawMessage(`{"uri":"nanogo://guide"}`))
	if rpcErr != nil {
		t.Fatalf("handleResourcesRead returned error: %+v", rpcErr)
	}
	contents := result.(map[string]any)["contents"].([]mcpResourceContent)
	if len(contents) != 1 || !strings.Contains(contents[0].Text, "Recommended workflow") {
		t.Fatalf("guide contents = %+v, want workflow instructions", contents)
	}
}

func TestToolListIncludesProjectWorkflowTools(t *testing.T) {
	result, rpcErr := handleToolsList()
	if rpcErr != nil {
		t.Fatalf("handleToolsList returned error: %+v", rpcErr)
	}
	tools := result.(map[string]any)["tools"].([]mcpTool)
	names := make(map[string]bool, len(tools))
	for _, tool := range tools {
		names[tool.Name] = true
	}
	for _, expected := range []string{
		"inspect_code", "call_graph", "run_module", "test_module", "index_module",
		"vfs_tree", "vfs_stat", "vfs_chdir",
	} {
		if !names[expected] {
			t.Errorf("tools/list missing %q", expected)
		}
	}
}

func TestAnalysisToolsReturnStructuredJSON(t *testing.T) {
	source := `package main
import "fmt"
func helper() { fmt.Println("ok") }
func main() { helper() }
`

	inspection := callTool(t, "inspect_code", map[string]any{"code": source})
	if inspection.IsError {
		t.Fatalf("inspect_code returned error: %s", toolText(t, inspection))
	}
	var inspected struct {
		Funcs []string `json:"funcs"`
		Tree  any      `json:"tree"`
	}
	decodeToolJSON(t, inspection, &inspected)
	if got, want := strings.Join(inspected.Funcs, ","), "helper,main"; got != want {
		t.Fatalf("inspection funcs = %q, want %q", got, want)
	}
	if inspected.Tree != nil {
		t.Fatal("inspect_code returned AST despite include_tree=false")
	}

	graph := callTool(t, "call_graph", map[string]any{"code": source})
	if graph.IsError {
		t.Fatalf("call_graph returned error: %s", toolText(t, graph))
	}
	var callGraph struct {
		TotalFunctions int `json:"totalFunctions"`
	}
	decodeToolJSON(t, graph, &callGraph)
	if callGraph.TotalFunctions != 2 {
		t.Fatalf("call_graph totalFunctions = %d, want 2", callGraph.TotalFunctions)
	}
}

func TestModuleExecutionTestingAndVFSNavigation(t *testing.T) {
	resetSessionVFS(t)
	writeVFSFile(t, "/workspace/go.mod", "module example.com/demo\n")
	writeVFSFile(t, "/workspace/main.go", `package main

import "fmt"

func Add(a, b int) int { return a + b }

func main() { fmt.Println(Add(2, 5)) }
`)
	writeVFSFile(t, "/workspace/main_test.go", `package main

import "testing"

func TestAdd(t *testing.T) {
	if got := Add(2, 5); got != 7 {
		t.Errorf("Add() = %d, want 7", got)
	}
}
`)

	tree := callTool(t, "vfs_tree", map[string]any{"path": "/workspace", "max_depth": 2})
	if tree.IsError {
		t.Fatalf("vfs_tree returned error: %s", toolText(t, tree))
	}
	var workspaceTree struct {
		Entries []vfsEntry `json:"entries"`
	}
	decodeToolJSON(t, tree, &workspaceTree)
	if len(workspaceTree.Entries) != 3 {
		t.Fatalf("vfs_tree entry count = %d, want 3", len(workspaceTree.Entries))
	}

	run := callTool(t, "run_module", map[string]any{"root": "/workspace"})
	if run.IsError {
		t.Fatalf("run_module returned error: %s", toolText(t, run))
	}
	if got := toolText(t, run); got != "7\n" {
		t.Fatalf("run_module output = %q, want 7\\n", got)
	}

	tests := callTool(t, "test_module", map[string]any{"root": "/workspace"})
	if tests.IsError {
		t.Fatalf("test_module returned error: %s", toolText(t, tests))
	}
	var summary moduleTestSummary
	decodeToolJSON(t, tests, &summary)
	if !summary.Passed || summary.Total != 1 || len(summary.Results) != 1 || summary.Results[0].Name != "TestAdd" {
		t.Fatalf("test_module summary = %+v, want one passing named test", summary)
	}

	index := callTool(t, "index_module", map[string]any{"root": "/workspace"})
	if index.IsError {
		t.Fatalf("index_module returned error: %s", toolText(t, index))
	}
	var moduleIndex moduleIndexResult
	decodeToolJSON(t, index, &moduleIndex)
	if moduleIndex.TotalFunctions != 3 {
		t.Fatalf("index_module totalFunctions = %d, want 3", moduleIndex.TotalFunctions)
	}
}

func TestToolArgumentsAreValidated(t *testing.T) {
	resetSessionVFS(t)
	invalidType := callTool(t, "run_code", map[string]any{"code": 42})
	if !invalidType.IsError || !strings.Contains(toolText(t, invalidType), "must be a string") {
		t.Fatalf("run_code type error = %+v, want explicit string validation", invalidType)
	}

	invalidRange := callTool(t, "vfs_tree", map[string]any{"max_depth": 21})
	if !invalidRange.IsError || !strings.Contains(toolText(t, invalidRange), "between 0 and 20") {
		t.Fatalf("vfs_tree range error = %+v, want bounded argument error", invalidRange)
	}

	rootRemoval := callTool(t, "vfs_remove", map[string]any{"path": "/", "recursive": true})
	if !rootRemoval.IsError || !strings.Contains(toolText(t, rootRemoval), "refusing to remove the VFS root") {
		t.Fatalf("vfs_remove root result = %+v, want protected VFS root", rootRemoval)
	}

	_, rpcErr := handleToolCall(json.RawMessage(`{"name":"run_code","arguments":[]}`), defaultSession)
	if rpcErr == nil || rpcErr.Code != -32602 {
		t.Fatalf("non-object arguments rpc error = %+v, want -32602", rpcErr)
	}
}

func resetSessionVFS(t *testing.T) {
	t.Helper()
	previous := defaultSession.vfs
	defaultSession.vfs = interp.NewVFS()
	t.Cleanup(func() { defaultSession.vfs = previous })
}

func writeVFSFile(t *testing.T, path, content string) {
	t.Helper()
	result := callTool(t, "vfs_write", map[string]any{
		"path":           path,
		"content":        content,
		"create_parents": true,
	})
	if result.IsError {
		t.Fatalf("vfs_write %s returned error: %s", path, toolText(t, result))
	}
}

func callTool(t *testing.T, name string, arguments map[string]any) mcpToolResult {
	t.Helper()
	params, err := json.Marshal(map[string]any{"name": name, "arguments": arguments})
	if err != nil {
		t.Fatalf("marshal tool params: %v", err)
	}
	result, rpcErr := handleToolCall(params, defaultSession)
	if rpcErr != nil {
		t.Fatalf("tools/call %s rpc error: %+v", name, rpcErr)
	}
	toolResult, ok := result.(mcpToolResult)
	if !ok {
		t.Fatalf("tools/call %s result type = %T, want mcpToolResult", name, result)
	}
	return toolResult
}

func toolText(t *testing.T, result mcpToolResult) string {
	t.Helper()
	if len(result.Content) != 1 {
		t.Fatalf("tool result content = %+v, want exactly one text item", result.Content)
	}
	return result.Content[0].Text
}

func decodeToolJSON(t *testing.T, result mcpToolResult, output any) {
	t.Helper()
	if err := json.Unmarshal([]byte(toolText(t, result)), output); err != nil {
		t.Fatalf("unmarshal tool JSON %q: %v", toolText(t, result), err)
	}
}
