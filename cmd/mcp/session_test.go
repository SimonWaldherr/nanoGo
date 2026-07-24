package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// postMCP sends one JSON-RPC request to the /mcp handler and returns the
// recorder. sessionID, when non-empty, is sent as the Mcp-Session-Id header.
func postMCP(t *testing.T, store *sessionStore, sessionID, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	if sessionID != "" {
		req.Header.Set(sessionIDHeader, sessionID)
	}
	rec := httptest.NewRecorder()
	handleHTTPMCP(store, rec, req)
	return rec
}

// decodeRPC unmarshals a JSON-RPC response body from the recorder.
func decodeRPC(t *testing.T, rec *httptest.ResponseRecorder) jsonRPCResponse {
	t.Helper()
	var resp jsonRPCResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode JSON-RPC response %q: %v", rec.Body.String(), err)
	}
	return resp
}

func TestHTTPInitializeAllocatesSession(t *testing.T) {
	store := newSessionStore()

	rec := postMCP(t, store, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	sessionID := rec.Header().Get(sessionIDHeader)
	if sessionID == "" {
		t.Fatal("initialize did not return a Mcp-Session-Id header")
	}
	if _, ok := store.get(sessionID); !ok {
		t.Fatalf("session %q not registered in store", sessionID)
	}
	resp := decodeRPC(t, rec)
	if resp.Error != nil {
		t.Fatalf("initialize returned RPC error: %+v", resp.Error)
	}
}

func TestHTTPRequiresSessionForNonInitialize(t *testing.T) {
	store := newSessionStore()

	// A tool call without a session header must be rejected.
	rec := postMCP(t, store, "", `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing-session status = %d, want 404", rec.Code)
	}
	// An unknown session id is likewise rejected.
	rec = postMCP(t, store, "does-not-exist", `{"jsonrpc":"2.0","id":3,"method":"tools/list"}`)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown-session status = %d, want 404", rec.Code)
	}
}

func TestHTTPSessionVFSIsolation(t *testing.T) {
	store := newSessionStore()

	// Two independent clients each initialize their own session.
	first := postMCP(t, store, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`)
	second := postMCP(t, store, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`)
	firstID := first.Header().Get(sessionIDHeader)
	secondID := second.Header().Get(sessionIDHeader)
	if firstID == "" || secondID == "" || firstID == secondID {
		t.Fatalf("expected two distinct session ids, got %q and %q", firstID, secondID)
	}

	// Write a file in the first session's VFS.
	writeBody := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"vfs_write","arguments":{"path":"/note.txt","content":"hello"}}}`
	if rec := postMCP(t, store, firstID, writeBody); rec.Code != http.StatusOK {
		t.Fatalf("vfs_write status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	// It is readable in the same session.
	readBody := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"vfs_read","arguments":{"path":"/note.txt"}}}`
	rec := postMCP(t, store, firstID, readBody)
	result := toolResultFromRPC(t, decodeRPC(t, rec))
	if result.IsError || toolText(t, result) != "hello" {
		t.Fatalf("vfs_read in first session = %+v, want \"hello\"", result)
	}

	// It is NOT visible in the second session's VFS.
	rec = postMCP(t, store, secondID, readBody)
	result = toolResultFromRPC(t, decodeRPC(t, rec))
	if !result.IsError {
		t.Fatalf("vfs_read in second session unexpectedly succeeded: %+v", result)
	}
}

func TestHTTPDeleteEndsSession(t *testing.T) {
	store := newSessionStore()
	rec := postMCP(t, store, "", `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-11-25"}}`)
	sessionID := rec.Header().Get(sessionIDHeader)

	req := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Header.Set(sessionIDHeader, sessionID)
	del := httptest.NewRecorder()
	handleHTTPMCP(store, del, req)
	if del.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204", del.Code)
	}
	if _, ok := store.get(sessionID); ok {
		t.Fatal("session still present after DELETE")
	}

	// DELETE without a session id is a bad request.
	req = httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	bad := httptest.NewRecorder()
	handleHTTPMCP(store, bad, req)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("DELETE without header status = %d, want 400", bad.Code)
	}
}

func TestHTTPGetMcpIsMethodNotAllowed(t *testing.T) {
	store := newSessionStore()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rec := httptest.NewRecorder()
	handleHTTPMCP(store, rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("GET /mcp status = %d, want 405", rec.Code)
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("GET /mcp missing open CORS header")
	}
}

func TestHTTPHealthAndRootProbes(t *testing.T) {
	health := httptest.NewRecorder()
	handleHealth(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", health.Code)
	}
	var status struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(health.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode /healthz: %v", err)
	}
	if status.Status != "ok" || status.Version != serverVersion {
		t.Fatalf("/healthz body = %+v, want ok/%s", status, serverVersion)
	}

	root := httptest.NewRecorder()
	handleRoot(root, httptest.NewRequest(http.MethodGet, "/", nil))
	if root.Code != http.StatusOK || !strings.Contains(root.Body.String(), "MCP server") {
		t.Fatalf("/ probe = %d %q, want 200 with description", root.Code, root.Body.String())
	}
}

func TestSessionReapRemovesIdle(t *testing.T) {
	store := newSessionStore()
	sess := store.create()
	// Make the session look old, then reap relative to now.
	sess.mu.Lock()
	sess.lastSeen = sess.lastSeen.Add(-sessionIdleTimeout - time.Minute)
	old := sess.lastSeen
	sess.mu.Unlock()

	if removed := store.reap(old.Add(sessionIdleTimeout + 2*time.Minute)); removed != 1 {
		t.Fatalf("reap removed %d sessions, want 1", removed)
	}
	if _, ok := store.get(sess.id); ok {
		t.Fatal("idle session survived reaping")
	}
}

// toolResultFromRPC extracts an mcpToolResult from a JSON-RPC response's result
// field (which arrives as generic JSON after a round-trip).
func toolResultFromRPC(t *testing.T, resp jsonRPCResponse) mcpToolResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("unexpected JSON-RPC error: %+v", resp.Error)
	}
	raw, err := json.Marshal(resp.Result)
	if err != nil {
		t.Fatalf("re-marshal result: %v", err)
	}
	var result mcpToolResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("decode tool result %q: %v", string(raw), err)
	}
	return result
}
