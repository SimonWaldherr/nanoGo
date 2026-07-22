// cmd/mcp/main.go — nanoGo MCP (Model Context Protocol) server.
//
// The server speaks two interchangeable transports over the identical
// JSON-RPC handler (handleMethod), so any MCP client can reach it regardless
// of whether it can spawn a local subprocess:
//
//   - stdio (default): newline-delimited JSON-RPC on stdin/stdout, log
//     output on stderr. This is what Claude Desktop, Claude Code, and most
//     editor integrations expect to launch as a private subprocess.
//   - Streamable HTTP (-http addr, see http.go): a single POST/GET/DELETE
//     endpoint per the MCP spec's HTTP transport, for clients that reach
//     the server over a network instead of spawning it — web-based
//     clients, remote/hosted agents, or one server shared by several
//     clients at once.
//
// Exposed MCP tools include safe single-file and multi-file execution,
// package tests, static source/module analysis, and a session virtual
// filesystem. See the nanogo://guide resource for a suggested workflow.
//
// Each client gets its own isolated VFS (see session.go): the stdio
// transport serves one implicit client for the process's lifetime, while
// the HTTP transport allocates one session per Mcp-Session-Id so concurrent
// clients never see each other's files.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"simonwaldherr.de/go/nanogo/interp"
	"simonwaldherr.de/go/nanogo/interp/index"
	"simonwaldherr.de/go/nanogo/interp/loader"
)

const (
	serverName    = "nanoGo"
	serverVersion = "0.2.0"

	latestProtocolVersion = "2025-11-25"
)

var supportedProtocolVersions = map[string]bool{
	"2024-11-05": true,
	"2025-03-26": true,
	"2025-06-18": true,
	"2025-11-25": true,
}

// ── JSON-RPC 2.0 types ──────────────────────────────────────────────────────

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

// ── MCP protocol types ───────────────────────────────────────────────────────

type mcpInitializeParams struct {
	ProtocolVersion string `json:"protocolVersion"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

type mcpToolCallParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type mcpContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type mcpToolResult struct {
	Content []mcpContent `json:"content"`
	IsError bool         `json:"isError"`
}

type mcpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

type mcpResource struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mimeType,omitempty"`
}

type mcpResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mimeType,omitempty"`
	Text     string `json:"text"`
}

// ── entry point ──────────────────────────────────────────────────────────────

func main() {
	httpAddr := flag.String("http", os.Getenv("NANOGO_MCP_HTTP_ADDR"), "serve MCP over Streamable HTTP at this address (e.g. :8080) instead of stdio")
	flag.Parse()

	if *httpAddr != "" {
		runHTTPServer(*httpAddr)
		return
	}
	runStdioServer()
}

// runStdioServer implements the MCP stdio transport: newline-delimited
// JSON-RPC on stdin/stdout, diagnostics on stderr. It serves exactly one
// implicit client (defaultSession) for the life of the process, matching
// how every existing stdio-only MCP client (Claude Desktop, Claude Code,
// most IDE integrations) expects to launch and own a private subprocess.
func runStdioServer() {
	scanner := bufio.NewScanner(os.Stdin)
	// Increase scanner buffer for large code payloads (up to 4 MiB).
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 4*1024*1024)

	encoder := json.NewEncoder(os.Stdout)

	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "[nanoGo-mcp] "+format+"\n", args...)
	}

	logf("nanoGo MCP server starting (stdio transport)")

	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			logf("parse error: %v", err)
			_ = encoder.Encode(jsonRPCResponse{
				JSONRPC: "2.0",
				Error:   &jsonRPCError{Code: -32700, Message: "Parse error"},
			})
			continue
		}

		if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" || !validRequestID(req.ID) {
			logf("invalid request")
			_ = encoder.Encode(jsonRPCResponse{
				JSONRPC: "2.0",
				Error:   &jsonRPCError{Code: -32600, Message: "Invalid Request"},
			})
			continue
		}

		// Notifications (requests without an ID) are fire-and-forget.
		isNotification := req.ID == nil

		result, rpcErr := handleMethod(req.Method, req.Params, defaultSession)

		if isNotification {
			// Do not send a response for notifications.
			logf("notification %s handled", req.Method)
			continue
		}

		resp := jsonRPCResponse{JSONRPC: "2.0", ID: req.ID}
		if rpcErr != nil {
			resp.Error = rpcErr
		} else {
			resp.Result = result
		}
		if err := encoder.Encode(resp); err != nil {
			logf("encode error: %v", err)
		}
	}
	if err := scanner.Err(); err != nil {
		logf("scanner error: %v", err)
		os.Exit(1)
	}
}

// handleMethod dispatches a JSON-RPC method to the appropriate handler. sess
// is the calling client's session (see session.go); only tools/call uses it,
// but every transport (stdio, HTTP) threads one through uniformly so a tool
// added later can rely on it being present regardless of transport.
func handleMethod(method string, rawParams json.RawMessage, sess *session) (any, *jsonRPCError) {
	switch method {
	case "initialize":
		return handleInitialize(rawParams)
	case "notifications/initialized":
		return nil, nil
	case "tools/list":
		return handleToolsList()
	case "tools/call":
		return handleToolCall(rawParams, sess)
	case "resources/list":
		return handleResourcesList()
	case "resources/read":
		return handleResourcesRead(rawParams)
	case "ping":
		return map[string]any{}, nil
	default:
		return nil, &jsonRPCError{Code: -32601, Message: "Method not found: " + method}
	}
}

// validRequestID accepts the MCP/JSON-RPC request-ID types. A nil ID denotes a
// notification; a literal JSON null is not a valid MCP request ID.
func validRequestID(raw json.RawMessage) bool {
	if raw == nil {
		return true
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	switch value.(type) {
	case string, float64:
		return true
	default:
		return false
	}
}

// ── initialize ────────────────────────────────────────────────────────────────

func handleInitialize(rawParams json.RawMessage) (any, *jsonRPCError) {
	var params mcpInitializeParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "Invalid initialize params: " + err.Error()}
	}
	if params.ProtocolVersion == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "Invalid initialize params: protocolVersion is required"}
	}
	protocolVersion := params.ProtocolVersion
	if !supportedProtocolVersions[protocolVersion] {
		protocolVersion = latestProtocolVersion
	}
	return map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    serverName,
			"version": serverVersion,
		},
		"instructions": "nanoGo provides a sandboxed Go workspace. Read nanogo://guide before working with a multi-file project; use static analysis and tests before execution.",
	}, nil
}

// ── resources ───────────────────────────────────────────────────────────────

const guideURI = "nanogo://guide"

const guideText = `# nanoGo MCP workspace

nanoGo lets an MCP client create, analyse, test, and run small Go programs in
an in-memory virtual filesystem (VFS). It is intended for quick experiments,
teaching, code review, regression reproduction, and agent-assisted iteration
without giving guest Go code access to the host filesystem or network.

## Recommended workflow

1. Use vfs_write with create_parents=true to create go.mod and source files.
2. Use vfs_tree or index_module to understand the workspace.
3. Use inspect_code, call_graph, fmt_code, and vet_code for non-executing
   feedback.
4. Use test_module for supported TestXxx(t *testing.T) tests.
5. Use run_module for a multi-file module, or run_code for a single snippet.

The VFS belongs to this server process only; it is not the host disk and is
discarded when the process exits. Guest code may read and write that VFS, but
network access remains denied. Execution has a caller-configurable timeout of
1–60 seconds (10 seconds by default), plus nanoGo's resource limits.

Static analysis is intentionally syntactic and best-effort: it does not
replace the Go compiler or type checker. test_module implements nanoGo's
supported subset of Go's testing package.
`

func handleResourcesList() (any, *jsonRPCError) {
	return map[string]any{"resources": []mcpResource{{
		URI:         guideURI,
		Name:        "nanoGo MCP guide",
		Description: "Purpose, safety model, supported workflows, and tool ordering for the nanoGo MCP server.",
		MimeType:    "text/markdown",
	}}}, nil
}

func handleResourcesRead(rawParams json.RawMessage) (any, *jsonRPCError) {
	var params struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "Invalid resource parameters: " + err.Error()}
	}
	if params.URI != guideURI {
		return nil, &jsonRPCError{Code: -32602, Message: "Unknown resource: " + params.URI}
	}
	return map[string]any{"contents": []mcpResourceContent{{
		URI:      guideURI,
		MimeType: "text/markdown",
		Text:     guideText,
	}}}, nil
}

// ── tools/list ────────────────────────────────────────────────────────────────

func handleToolsList() (any, *jsonRPCError) {
	tools := []mcpTool{
		{
			Name:        "run_code",
			Description: "Execute one self-contained Go source file in nanoGo's sandbox. The source must declare package main and main(). Captures program output; use run_module for a VFS-backed multi-file project.",
			InputSchema: schema(map[string]any{
				"code":    prop("string", "Complete Go source code to execute (must include package main)"),
				"timeout": prop("integer", "Execution timeout in seconds (default 10, range 1-60)"),
			}, "code"),
		},
		{
			Name:        "fmt_code",
			Description: "Format Go source code using gofmt rules. Returns the formatted source or an error message.",
			InputSchema: schema(map[string]any{
				"code": prop("string", "Go source code to format"),
			}, "code"),
		},
		{
			Name:        "vet_code",
			Description: "Run nanoGo's fast static checks for unreachable code, printf argument mismatches, and self-assignments. It does not execute the source and is not a replacement for go vet.",
			InputSchema: schema(map[string]any{
				"code": prop("string", "Go source code to analyse"),
			}, "code"),
		},
		{
			Name:        "inspect_code",
			Description: "Parse Go source without executing it and return imports, declared functions, AST size/depth, and parse time. Request the tree only when source-level structure is needed, because it can be large.",
			InputSchema: schema(map[string]any{
				"code":         prop("string", "Go source code to inspect"),
				"include_tree": prop("boolean", "Include the complete syntax tree (default false)"),
			}, "code"),
		},
		{
			Name:        "call_graph",
			Description: "Build a best-effort static call graph for one Go source file without executing it. Local calls are resolved where unambiguous; package and interface calls remain visible but unresolved.",
			InputSchema: schema(map[string]any{
				"code":          prop("string", "Go source code to analyse"),
				"max_functions": prop("integer", "Maximum function records to return (default 100, range 1-1000)"),
			}, "code"),
		},
		{
			Name:        "run_module",
			Description: "Build and run a multi-file nanoGo module already stored in the session VFS. The root directory must contain go.mod; local module imports are resolved inside the VFS. Captures program output.",
			InputSchema: schema(map[string]any{
				"root":    prop("string", "Module-root directory in the VFS; must contain go.mod"),
				"entry":   prop("string", "Entry function in the root package (default main)"),
				"timeout": prop("integer", "Execution timeout in seconds (default 10, range 1-60)"),
			}, "root"),
		},
		{
			Name:        "test_module",
			Description: "Run supported TestXxx(t *testing.T) tests for a package in a VFS-backed module. Results are named and categorized; failed assertions are returned as a tool error so an agent can fix them.",
			InputSchema: schema(map[string]any{
				"root":    prop("string", "Module-root directory in the VFS; must contain go.mod"),
				"package": prop("string", "Go package name to test (default: root package)"),
				"timeout": prop("integer", "Total timeout in seconds (default 10, range 1-60)"),
			}, "root"),
		},
		{
			Name:        "index_module",
			Description: "Statically index every Go package below a VFS directory. Returns compact function metadata, callers/callees, tests, and complexity metrics without returning source bodies or executing code.",
			InputSchema: schema(map[string]any{
				"root":          prop("string", "Directory in the VFS to index"),
				"max_functions": prop("integer", "Maximum function records to return (default 200, range 1-1000)"),
			}, "root"),
		},
		{
			Name:        "vfs_read",
			Description: "Read a file from the session virtual filesystem. Paths follow Unix conventions (e.g. /tmp/data.txt).",
			InputSchema: schema(map[string]any{
				"path": prop("string", "Absolute or relative path of the file to read"),
			}, "path"),
		},
		{
			Name:        "vfs_write",
			Description: "Write (create or overwrite) a text file in the session virtual filesystem. Set create_parents=true to create missing parent directories in the same call.",
			InputSchema: schema(map[string]any{
				"path":           prop("string", "Absolute or relative path of the file to write"),
				"content":        prop("string", "Content to write"),
				"create_parents": prop("boolean", "Create any missing parent directories (default false)"),
			}, "path", "content"),
		},
		{
			Name:        "vfs_list",
			Description: "List the contents of a directory in the session virtual filesystem.",
			InputSchema: schema(map[string]any{
				"path": prop("string", "Directory path to list (default '/')"),
			}),
		},
		{
			Name:        "vfs_mkdir",
			Description: "Create a directory (and all parents) in the session virtual filesystem.",
			InputSchema: schema(map[string]any{
				"path": prop("string", "Directory path to create"),
			}, "path"),
		},
		{
			Name:        "vfs_tree",
			Description: "Return a bounded recursive, metadata-rich directory tree for the session VFS. Prefer it over repeated vfs_list calls when orienting in a project.",
			InputSchema: schema(map[string]any{
				"path":        prop("string", "Directory path to inspect (default current directory)"),
				"max_depth":   prop("integer", "Maximum traversal depth (default 4, range 0-20)"),
				"max_entries": prop("integer", "Maximum entries to return (default 200, range 1-1000)"),
			}),
		},
		{
			Name:        "vfs_stat",
			Description: "Return metadata for one VFS file or directory, including its resolved path, type, size, mode, and modification time.",
			InputSchema: schema(map[string]any{
				"path": prop("string", "Path to inspect"),
			}, "path"),
		},
		{
			Name:        "vfs_chdir",
			Description: "Change the VFS working directory used to resolve later relative paths, then return the new absolute directory.",
			InputSchema: schema(map[string]any{
				"path": prop("string", "Existing directory to make current"),
			}, "path"),
		},
		{
			Name:        "vfs_remove",
			Description: "Remove a file or directory from the session virtual filesystem. Directories must be empty unless recursive is true.",
			InputSchema: schema(map[string]any{
				"path":      prop("string", "Path to remove"),
				"recursive": prop("boolean", "Remove directory and all contents (default false)"),
			}, "path"),
		},
	}
	return map[string]any{"tools": tools}, nil
}

// prop builds a simple JSON-Schema property descriptor.
func prop(typ, description string) map[string]any {
	return map[string]any{"type": typ, "description": description}
}

// schema builds a minimal JSON-Schema object descriptor for tool input.
func schema(properties map[string]any, required ...string) map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": properties,
		"required":   required,
	}
}

// ── tools/call ────────────────────────────────────────────────────────────────

func handleToolCall(rawParams json.RawMessage, sess *session) (any, *jsonRPCError) {
	var params mcpToolCallParams
	if err := json.Unmarshal(rawParams, &params); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "Invalid params: " + err.Error()}
	}
	if params.Name == "" {
		return nil, &jsonRPCError{Code: -32602, Message: "Invalid params: tool name is required"}
	}
	if sess == nil {
		return nil, &jsonRPCError{Code: -32603, Message: "Internal error: no active session"}
	}
	sess.touch()
	args, rpcErr := decodeToolArgs(params.Arguments)
	if rpcErr != nil {
		return nil, rpcErr
	}

	result := func(text string, isError bool) (any, *jsonRPCError) {
		return mcpToolResult{Content: []mcpContent{{Type: "text", Text: text}}, IsError: isError}, nil
	}
	failure := func(err error) (any, *jsonRPCError) {
		return result("error: "+err.Error(), true)
	}
	requiredString := func(key string) (string, error) {
		value, err := args.string(key, "")
		if err != nil {
			return "", err
		}
		if value == "" {
			return "", fmt.Errorf("'%s' argument is required", key)
		}
		return value, nil
	}
	timeout := func() (time.Duration, error) {
		seconds, err := args.boundedInt("timeout", 10, 1, 60)
		return time.Duration(seconds) * time.Second, err
	}

	switch params.Name {
	case "run_code":
		code, err := requiredString("code")
		if err != nil {
			return failure(err)
		}
		duration, err := timeout()
		if err != nil {
			return failure(err)
		}
		output, err := runCode(code, sess.vfs, duration)
		return executionResult(result, output, err)

	case "fmt_code":
		code, err := requiredString("code")
		if err != nil {
			return failure(err)
		}
		formatted, err := interp.FormatSource(code)
		if err != nil {
			return failure(err)
		}
		return result(formatted, false)

	case "vet_code":
		code, err := requiredString("code")
		if err != nil {
			return failure(err)
		}
		issues, err := interp.VetSource(code)
		if err != nil {
			return failure(err)
		}
		if len(issues) == 0 {
			return result("ok", false)
		}
		var output strings.Builder
		for _, issue := range issues {
			output.WriteString(issue.String())
			output.WriteByte('\n')
		}
		return result(output.String(), false)

	case "inspect_code":
		code, err := requiredString("code")
		if err != nil {
			return failure(err)
		}
		includeTree, err := args.bool("include_tree", false)
		if err != nil {
			return failure(err)
		}
		inspection, err := interp.InspectSource(code)
		if err != nil {
			return failure(err)
		}
		response := map[string]any{
			"nodeCount": inspection.NodeCount,
			"maxDepth":  inspection.MaxDepth,
			"funcs":     inspection.Funcs,
			"imports":   inspection.Imports,
			"parseUs":   inspection.ParseUs,
		}
		if includeTree {
			response["tree"] = inspection.Tree
		}
		return jsonResult(result, response, false)

	case "call_graph":
		code, err := requiredString("code")
		if err != nil {
			return failure(err)
		}
		maxFunctions, err := args.boundedInt("max_functions", 100, 1, 1000)
		if err != nil {
			return failure(err)
		}
		graph, err := interp.AnalyzeCallGraph(code)
		if err != nil {
			return failure(err)
		}
		funcs := graph.Funcs
		truncated := len(funcs) > maxFunctions
		if truncated {
			funcs = funcs[:maxFunctions]
		}
		return jsonResult(result, map[string]any{
			"totalFunctions": len(graph.Funcs),
			"truncated":      truncated,
			"funcs":          funcs,
		}, false)

	case "run_module":
		root, err := requiredString("root")
		if err != nil {
			return failure(err)
		}
		entry, err := args.string("entry", "main")
		if err != nil {
			return failure(err)
		}
		duration, err := timeout()
		if err != nil {
			return failure(err)
		}
		output, err := runModule(root, entry, sess.vfs, duration)
		return executionResult(result, output, err)

	case "test_module":
		root, err := requiredString("root")
		if err != nil {
			return failure(err)
		}
		packageName, err := args.string("package", "")
		if err != nil {
			return failure(err)
		}
		duration, err := timeout()
		if err != nil {
			return failure(err)
		}
		summary, err := testModule(root, packageName, sess.vfs, duration)
		if err != nil {
			return failure(err)
		}
		return jsonResult(result, summary, !summary.Passed)

	case "index_module":
		root, err := requiredString("root")
		if err != nil {
			return failure(err)
		}
		maxFunctions, err := args.boundedInt("max_functions", 200, 1, 1000)
		if err != nil {
			return failure(err)
		}
		indexResult, err := indexModule(root, sess.vfs, maxFunctions)
		if err != nil {
			return failure(err)
		}
		return jsonResult(result, indexResult, false)

	case "vfs_read":
		filePath, err := requiredString("path")
		if err != nil {
			return failure(err)
		}
		data, err := sess.vfs.ReadFile(filePath)
		if err != nil {
			return failure(err)
		}
		return result(string(data), false)

	case "vfs_write":
		filePath, err := requiredString("path")
		if err != nil {
			return failure(err)
		}
		content, err := args.string("content", "")
		if err != nil {
			return failure(err)
		}
		createParents, err := args.bool("create_parents", false)
		if err != nil {
			return failure(err)
		}
		if createParents {
			if err := sess.vfs.MkdirAll(path.Dir(sess.vfs.ResolvePath(filePath)), 0755); err != nil {
				return failure(err)
			}
		}
		if err := sess.vfs.WriteFile(filePath, []byte(content), 0644); err != nil {
			return failure(err)
		}
		return result(fmt.Sprintf("wrote %d bytes to %s", len(content), sess.vfs.ResolvePath(filePath)), false)

	case "vfs_list":
		dirPath, err := args.string("path", sess.vfs.Getwd())
		if err != nil {
			return failure(err)
		}
		entries, err := sess.vfs.ReadDir(dirPath)
		if err != nil {
			return failure(err)
		}
		if len(entries) == 0 {
			return result("(empty directory)", false)
		}
		var output strings.Builder
		for _, entry := range entries {
			kind := "-"
			if entry.IsDir {
				kind = "d"
			}
			output.WriteString(fmt.Sprintf("%s  %s\n", kind, entry.Name))
		}
		return result(output.String(), false)

	case "vfs_mkdir":
		dirPath, err := requiredString("path")
		if err != nil {
			return failure(err)
		}
		if err := sess.vfs.MkdirAll(dirPath, 0755); err != nil {
			return failure(err)
		}
		return result("created "+sess.vfs.ResolvePath(dirPath), false)

	case "vfs_tree":
		dirPath, err := args.string("path", sess.vfs.Getwd())
		if err != nil {
			return failure(err)
		}
		maxDepth, err := args.boundedInt("max_depth", 4, 0, 20)
		if err != nil {
			return failure(err)
		}
		maxEntries, err := args.boundedInt("max_entries", 200, 1, 1000)
		if err != nil {
			return failure(err)
		}
		tree, err := vfsTree(sess.vfs, dirPath, maxDepth, maxEntries)
		if err != nil {
			return failure(err)
		}
		return jsonResult(result, tree, false)

	case "vfs_stat":
		filePath, err := requiredString("path")
		if err != nil {
			return failure(err)
		}
		info, err := sess.vfs.Stat(filePath)
		if err != nil {
			return failure(err)
		}
		return jsonResult(result, vfsMetadata(sess.vfs.ResolvePath(filePath), info), false)

	case "vfs_chdir":
		dirPath, err := requiredString("path")
		if err != nil {
			return failure(err)
		}
		if err := sess.vfs.Chdir(dirPath); err != nil {
			return failure(err)
		}
		return result(sess.vfs.Getwd(), false)

	case "vfs_remove":
		filePath, err := requiredString("path")
		if err != nil {
			return failure(err)
		}
		if sess.vfs.ResolvePath(filePath) == "/" {
			return failure(errors.New("refusing to remove the VFS root"))
		}
		recursive, err := args.bool("recursive", false)
		if err != nil {
			return failure(err)
		}
		if recursive {
			err = sess.vfs.RemoveAll(filePath)
		} else {
			err = sess.vfs.Remove(filePath)
		}
		if err != nil {
			return failure(err)
		}
		return result("removed "+sess.vfs.ResolvePath(filePath), false)

	default:
		return nil, &jsonRPCError{Code: -32602, Message: "Unknown tool: " + params.Name}
	}
}

type toolArgs map[string]any

func decodeToolArgs(raw json.RawMessage) (toolArgs, *jsonRPCError) {
	if len(raw) == 0 {
		return toolArgs{}, nil
	}
	var args toolArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, &jsonRPCError{Code: -32602, Message: "Invalid tool arguments: " + err.Error()}
	}
	if args == nil {
		return toolArgs{}, nil
	}
	return args, nil
}

func (args toolArgs) string(key, fallback string) (string, error) {
	value, ok := args[key]
	if !ok {
		return fallback, nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("'%s' must be a string", key)
	}
	return text, nil
}

func (args toolArgs) bool(key string, fallback bool) (bool, error) {
	value, ok := args[key]
	if !ok {
		return fallback, nil
	}
	flag, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("'%s' must be a boolean", key)
	}
	return flag, nil
}

func (args toolArgs) boundedInt(key string, fallback, min, max int) (int, error) {
	value, ok := args[key]
	if !ok {
		return fallback, nil
	}
	number, ok := value.(float64)
	if !ok || number != float64(int(number)) {
		return 0, fmt.Errorf("'%s' must be an integer", key)
	}
	converted := int(number)
	if converted < min || converted > max {
		return 0, fmt.Errorf("'%s' must be between %d and %d", key, min, max)
	}
	return converted, nil
}

func executionResult(result func(string, bool) (any, *jsonRPCError), output string, err error) (any, *jsonRPCError) {
	if err == nil {
		return result(output, false)
	}
	if output != "" {
		output += "\n"
	}
	return result(output+"error: "+err.Error(), true)
}

func jsonResult(result func(string, bool) (any, *jsonRPCError), value any, isError bool) (any, *jsonRPCError) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return result("error: could not encode tool result: "+err.Error(), true)
	}
	return result(string(data), isError)
}

// runCode executes Go source code in a fresh Interpreter that shares the
// session VFS. Output from guest code is captured, while the guest receives no
// host filesystem or network capability.
func runCode(source string, vfs *interp.VFS, timeout time.Duration) (output string, retErr error) {
	var buffer strings.Builder
	var bufferMu sync.Mutex
	defer func() {
		output = capturedOutput(&buffer, &bufferMu)
		if recovered := recover(); recovered != nil {
			retErr = fmt.Errorf("panic recovered: %v", recovered)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	vm := newMCPInterpreter(vfs, &buffer, &bufferMu)
	retErr = vm.RunContext(ctx, source)
	if errors.Is(retErr, context.DeadlineExceeded) {
		retErr = fmt.Errorf("execution timed out after %s: %w", timeout, retErr)
	}
	return output, retErr
}

// runModule is the multi-file companion to runCode. It loads only VFS files
// under root, resolving module-local imports without consulting the host disk.
func runModule(root, entry string, vfs *interp.VFS, timeout time.Duration) (output string, retErr error) {
	var buffer strings.Builder
	var bufferMu sync.Mutex
	defer func() {
		output = capturedOutput(&buffer, &bufferMu)
		if recovered := recover(); recovered != nil {
			retErr = fmt.Errorf("panic recovered: %v", recovered)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	program, err := loader.LoadModule(vfs, vfs.ResolvePath(root), loader.Options{})
	if err != nil {
		return "", err
	}
	vm := newMCPInterpreter(vfs, &buffer, &bufferMu)
	retErr = loader.RunProgram(ctx, vm, program, entry)
	if errors.Is(retErr, context.DeadlineExceeded) {
		retErr = fmt.Errorf("execution timed out after %s: %w", timeout, retErr)
	}
	return output, retErr
}

// newMCPInterpreter configures the exact sandbox used for both single-file
// and module execution. In particular, it grants guest code only the VFS
// capability; network capability remains at its deny-by-default zero value.
func newMCPInterpreter(vfs *interp.VFS, output *strings.Builder, outputMu *sync.Mutex) *interp.Interpreter {
	vm := interp.NewInterpreterWithVFS(vfs)
	vm.Capabilities.FileSystem = interp.FileSystemCapabilities{Read: true, Write: true}
	write := func(prefix string, args []any) {
		if len(args) == 0 {
			return
		}
		outputMu.Lock()
		output.WriteString(prefix)
		output.WriteString(interp.ToString(args[0]))
		output.WriteByte('\n')
		outputMu.Unlock()
	}
	vm.RegisterNative("ConsoleLog", func(args []any) (any, error) { write("", args); return nil, nil })
	vm.RegisterNative("ConsoleWarn", func(args []any) (any, error) { write("[warn] ", args); return nil, nil })
	vm.RegisterNative("ConsoleError", func(args []any) (any, error) { write("[error] ", args); return nil, nil })
	vm.RegisterNative("__hostSprintf", func(args []any) (any, error) {
		if len(args) == 0 {
			return "", nil
		}
		format := interp.ToString(args[0])
		formatArgs := make([]any, 0, len(args)-1)
		for _, arg := range args[1:] {
			formatArgs = append(formatArgs, arg)
		}
		return fmt.Sprintf(format, formatArgs...), nil
	})
	interp.RegisterBuiltinPackages(vm)
	return vm
}

func capturedOutput(buffer *strings.Builder, bufferMu *sync.Mutex) string {
	bufferMu.Lock()
	defer bufferMu.Unlock()
	return buffer.String()
}

type vfsEntry struct {
	Path    string `json:"path"`
	Type    string `json:"type"`
	Size    int    `json:"size"`
	Mode    string `json:"mode"`
	ModTime string `json:"modTime"`
}

type vfsTreeResult struct {
	Root       string     `json:"root"`
	MaxDepth   int        `json:"maxDepth"`
	MaxEntries int        `json:"maxEntries"`
	Truncated  bool       `json:"truncated"`
	Entries    []vfsEntry `json:"entries"`
}

func vfsMetadata(resolvedPath string, info *interp.VFSFileInfo) vfsEntry {
	kind := "file"
	if info.IsDir {
		kind = "directory"
	}
	return vfsEntry{
		Path:    resolvedPath,
		Type:    kind,
		Size:    info.Size,
		Mode:    fmt.Sprintf("%#o", info.Mode),
		ModTime: info.ModTime.UTC().Format(time.RFC3339Nano),
	}
}

func vfsTree(vfs *interp.VFS, dirPath string, maxDepth, maxEntries int) (vfsTreeResult, error) {
	root := vfs.ResolvePath(dirPath)
	info, err := vfs.Stat(root)
	if err != nil {
		return vfsTreeResult{}, err
	}
	if !info.IsDir {
		return vfsTreeResult{}, fmt.Errorf("tree %s: not a directory", dirPath)
	}
	result := vfsTreeResult{
		Root:       root,
		MaxDepth:   maxDepth,
		MaxEntries: maxEntries,
		Entries:    make([]vfsEntry, 0),
	}
	var walk func(current string, depth int) error
	walk = func(current string, depth int) error {
		entries, err := vfs.ReadDir(current)
		if err != nil {
			return err
		}
		if depth >= maxDepth {
			result.Truncated = result.Truncated || len(entries) > 0
			return nil
		}
		for _, entry := range entries {
			if result.Truncated {
				return nil
			}
			if len(result.Entries) >= maxEntries {
				result.Truncated = true
				return nil
			}
			entryPath := path.Join(current, entry.Name)
			result.Entries = append(result.Entries, vfsMetadata(entryPath, entry))
			if entry.IsDir {
				if err := walk(entryPath, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return vfsTreeResult{}, err
	}
	return result, nil
}

type moduleIndexResult struct {
	Root           string                 `json:"root"`
	TotalFunctions int                    `json:"totalFunctions"`
	Truncated      bool                   `json:"truncated"`
	Functions      []moduleFunctionRecord `json:"functions"`
}

type moduleFunctionRecord struct {
	ID        string               `json:"id"`
	Name      string               `json:"name"`
	Package   string               `json:"package"`
	File      string               `json:"file"`
	LineStart int                  `json:"lineStart"`
	LineEnd   int                  `json:"lineEnd"`
	Signature string               `json:"signature"`
	Doc       string               `json:"doc,omitempty"`
	Receiver  string               `json:"receiver,omitempty"`
	Calls     []string             `json:"calls,omitempty"`
	CalledBy  []string             `json:"calledBy,omitempty"`
	Tests     []string             `json:"tests,omitempty"`
	Metrics   moduleFunctionMetric `json:"metrics"`
}

type moduleFunctionMetric struct {
	CyclomaticComplexity int `json:"cyclomaticComplexity"`
	MaxNestingDepth      int `json:"maxNestingDepth"`
	LOC                  int `json:"loc"`
}

func indexModule(root string, vfs *interp.VFS, maxFunctions int) (moduleIndexResult, error) {
	resolvedRoot := vfs.ResolvePath(root)
	entries, err := index.Scan(vfs, resolvedRoot, index.Options{})
	if err != nil {
		return moduleIndexResult{}, err
	}
	result := moduleIndexResult{
		Root:           resolvedRoot,
		TotalFunctions: len(entries),
		Truncated:      len(entries) > maxFunctions,
		Functions:      make([]moduleFunctionRecord, 0, min(len(entries), maxFunctions)),
	}
	for _, entry := range entries[:min(len(entries), maxFunctions)] {
		result.Functions = append(result.Functions, moduleFunctionRecord{
			ID:        entry.ID,
			Name:      entry.Name,
			Package:   entry.Package,
			File:      entry.File,
			LineStart: entry.LineStart,
			LineEnd:   entry.LineEnd,
			Signature: entry.Signature,
			Doc:       truncateText(entry.Doc, 500),
			Receiver:  entry.Receiver,
			Calls:     entry.Calls,
			CalledBy:  entry.CalledBy,
			Tests:     entry.Tests,
			Metrics: moduleFunctionMetric{
				CyclomaticComplexity: entry.Metrics.CyclomaticComplexity,
				MaxNestingDepth:      entry.Metrics.MaxNestingDepth,
				LOC:                  entry.Metrics.LOC,
			},
		})
	}
	return result, nil
}

func truncateText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type moduleTestSummary struct {
	Root    string              `json:"root"`
	Package string              `json:"package"`
	Passed  bool                `json:"passed"`
	Total   int                 `json:"total"`
	Failed  int                 `json:"failed"`
	Results []moduleTestOutcome `json:"results"`
	Output  string              `json:"output,omitempty"`
}

type moduleTestOutcome struct {
	Name     string `json:"name"`
	Passed   bool   `json:"passed"`
	Category string `json:"category"`
	Line     int    `json:"line"`
	Column   int    `json:"column"`
	Panicked bool   `json:"panicked"`
}

func testModule(root, packageName string, vfs *interp.VFS, timeout time.Duration) (summary moduleTestSummary, retErr error) {
	resolvedRoot := vfs.ResolvePath(root)
	program, err := loader.LoadModule(vfs, resolvedRoot, loader.Options{})
	if err != nil {
		return moduleTestSummary{}, err
	}
	if packageName == "" {
		packageName = program.Packages[program.Entry].Name
	}
	names, err := packageTestNames(program, packageName)
	if err != nil {
		return moduleTestSummary{}, err
	}

	var buffer strings.Builder
	var bufferMu sync.Mutex
	defer func() {
		summary.Output = capturedOutput(&buffer, &bufferMu)
		if recovered := recover(); recovered != nil {
			retErr = fmt.Errorf("panic recovered: %v", recovered)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	vm := newMCPInterpreter(vfs, &buffer, &bufferMu)
	results, err := loader.RunPackageTests(ctx, vm, program, packageName)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return moduleTestSummary{}, fmt.Errorf("test execution timed out after %s: %w", timeout, err)
		}
		return moduleTestSummary{}, err
	}
	summary = moduleTestSummary{
		Root:    resolvedRoot,
		Package: packageName,
		Passed:  true,
		Total:   len(results),
		Results: make([]moduleTestOutcome, 0, len(results)),
	}
	for i, testResult := range results {
		name := fmt.Sprintf("Test #%d", i+1)
		if i < len(names) {
			name = names[i]
		}
		outcome := moduleTestOutcome{
			Name:     name,
			Passed:   testResult.Pass,
			Category: testResult.Category,
			Line:     testResult.Line,
			Column:   testResult.Column,
			Panicked: testResult.Panic,
		}
		if !outcome.Passed {
			summary.Passed = false
			summary.Failed++
		}
		summary.Results = append(summary.Results, outcome)
	}
	return summary, nil
}

func packageTestNames(program *loader.Program, packageName string) ([]string, error) {
	var parsedPackage *loader.ParsedPackage
	for _, candidate := range program.Packages {
		if candidate.Name != packageName {
			continue
		}
		if parsedPackage != nil {
			return nil, fmt.Errorf("package name %q is ambiguous in module", packageName)
		}
		parsedPackage = candidate
	}
	if parsedPackage == nil {
		return nil, fmt.Errorf("package %q was not found in module", packageName)
	}
	var names []string
	for _, file := range parsedPackage.TestFiles {
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if ok && function.Recv == nil && strings.HasPrefix(function.Name.Name, "Test") {
				names = append(names, function.Name.Name)
			}
		}
	}
	return names, nil
}
