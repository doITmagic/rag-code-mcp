package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// ── extractSSEPayload tests ──────────────────────────────────────────

func TestExtractSSEPayload_SingleEvent(t *testing.T) {
	body := []byte("data: {\"jsonrpc\":\"2.0\",\"id\":\"1\",\"result\":{\"tools\":[]}}\n\n")
	result := extractSSEPayload(body)
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if parsed["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc=2.0, got %v", parsed["jsonrpc"])
	}
}

func TestExtractSSEPayload_MultipleEvents_ReturnsLast(t *testing.T) {
	body := []byte(
		"data: {\"jsonrpc\":\"2.0\",\"id\":\"1\",\"result\":{\"first\":true}}\n\n" +
			"data: {\"jsonrpc\":\"2.0\",\"id\":\"1\",\"result\":{\"last\":true}}\n\n",
	)
	result := extractSSEPayload(body)
	if result == nil {
		t.Fatal("expected non-nil result")
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	res := parsed["result"].(map[string]any)
	if res["last"] != true {
		t.Errorf("expected last event, got %v", res)
	}
}

func TestExtractSSEPayload_DoneEvent_Ignored(t *testing.T) {
	body := []byte(
		"data: {\"jsonrpc\":\"2.0\",\"id\":\"1\",\"result\":{}}\n\n" +
			"data: [DONE]\n\n",
	)
	result := extractSSEPayload(body)
	if result == nil {
		t.Fatal("expected non-nil result (should skip [DONE])")
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
}

func TestExtractSSEPayload_InvalidJSON_ReturnsNil(t *testing.T) {
	body := []byte("data: this is not json\n\n")
	result := extractSSEPayload(body)
	if result != nil {
		t.Errorf("expected nil for invalid JSON, got %s", result)
	}
}

func TestExtractSSEPayload_EmptyBody_ReturnsNil(t *testing.T) {
	result := extractSSEPayload([]byte{})
	if result != nil {
		t.Errorf("expected nil for empty body, got %s", result)
	}
}

func TestExtractSSEPayload_UnterminatedEvent(t *testing.T) {
	// No trailing blank line — should still extract.
	body := []byte("data: {\"jsonrpc\":\"2.0\",\"id\":\"1\",\"result\":{}}")
	result := extractSSEPayload(body)
	if result == nil {
		t.Fatal("expected non-nil result for unterminated event")
	}
}

// ── forwardToMaster tests ────────────────────────────────────────────

func TestForwardToMaster_JSONResponse(t *testing.T) {
	expected := map[string]any{
		"jsonrpc": "2.0",
		"id":      "42",
		"result":  map[string]any{"status": "ok"},
	}
	expectedBytes, _ := json.Marshal(expected)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(expectedBytes)
	}))
	defer server.Close()

	client := &http.Client{}
	msg := []byte(`{"jsonrpc":"2.0","id":"42","method":"tools/list","params":{}}`)
	result, err := forwardToMaster(client, server.URL, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
	if parsed["id"] != "42" {
		t.Errorf("expected id=42, got %v", parsed["id"])
	}
}

func TestForwardToMaster_SSEResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"jsonrpc\":\"2.0\",\"id\":\"1\",\"result\":{\"tools\":[]}}\n\n")
	}))
	defer server.Close()

	client := &http.Client{}
	msg := []byte(`{"jsonrpc":"2.0","id":"1","method":"tools/list","params":{}}`)
	result, err := forwardToMaster(client, server.URL, msg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(result, &parsed); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}
}

func TestForwardToMaster_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer server.Close()

	client := &http.Client{}
	msg := []byte(`{"jsonrpc":"2.0","id":"1","method":"tools/list","params":{}}`)
	_, err := forwardToMaster(client, server.URL, msg)
	if err == nil {
		t.Fatal("expected error for 500 status")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("expected error to mention status 500, got: %v", err)
	}
}

func TestForwardToMaster_InvalidContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("this is not json or sse"))
	}))
	defer server.Close()

	client := &http.Client{}
	msg := []byte(`{"jsonrpc":"2.0","id":"1","method":"tools/list","params":{}}`)
	_, err := forwardToMaster(client, server.URL, msg)
	if err == nil {
		t.Fatal("expected error for non-JSON non-SSE response")
	}
	if !strings.Contains(err.Error(), "neither valid JSON nor SSE") {
		t.Errorf("expected descriptive error, got: %v", err)
	}
}

// ── writeErrorResponse tests ─────────────────────────────────────────

func TestWriteErrorResponse(t *testing.T) {
	var buf bytes.Buffer
	writeErrorResponse(&buf, `{"jsonrpc":"2.0","id":"test-123","method":"tools/call"}`, fmt.Errorf("connection refused"))

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("error response is not valid JSON: %v\nbody: %s", err, buf.String())
	}

	if parsed["id"] != "test-123" {
		t.Errorf("expected id=test-123, got %v", parsed["id"])
	}
	if parsed["jsonrpc"] != "2.0" {
		t.Errorf("expected jsonrpc=2.0, got %v", parsed["jsonrpc"])
	}
	errObj := parsed["error"].(map[string]any)
	msg := errObj["message"].(string)
	if !strings.Contains(msg, "connection refused") {
		t.Errorf("expected error message to contain original error, got: %s", msg)
	}
}

func TestWriteErrorResponse_InvalidJSON_NilID(t *testing.T) {
	var buf bytes.Buffer
	writeErrorResponse(&buf, "this is not json", fmt.Errorf("timeout"))

	var parsed map[string]any
	if err := json.Unmarshal(buf.Bytes(), &parsed); err != nil {
		t.Fatalf("error response is not valid JSON: %v", err)
	}
	if parsed["id"] != nil {
		t.Errorf("expected nil id for unparseable request, got %v", parsed["id"])
	}
}

// ── PortIsOccupied tests ─────────────────────────────────────────────

func TestPortIsOccupied_FreePort(t *testing.T) {
	if PortIsOccupied(59999) {
		t.Error("expected port 59999 to be free")
	}
}

func TestPortIsOccupied_OccupiedPort(t *testing.T) {
	// Start a test server to occupy a port.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer server.Close()

	// Extract port from server URL (e.g. "http://127.0.0.1:12345")
	parts := strings.Split(server.URL, ":")
	portStr := parts[len(parts)-1]
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	if !PortIsOccupied(port) {
		t.Errorf("expected port %d to be occupied", port)
	}
}

// ── QueryMasterVersion tests ─────────────────────────────────────────

func TestQueryMasterVersion_ValidResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read and validate the incoming request is an initialize call.
		body, _ := io.ReadAll(r.Body)
		var req map[string]any
		_ = json.Unmarshal(body, &req)

		if req["method"] != "initialize" {
			t.Errorf("expected initialize method, got %v", req["method"])
		}

		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result": map[string]any{
				"serverInfo": map[string]any{
					"name":    "ragcode",
					"version": "2.1.51",
				},
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	parts := strings.Split(server.URL, ":")
	portStr := parts[len(parts)-1]
	var port int
	_, _ = fmt.Sscanf(portStr, "%d", &port)

	version := QueryMasterVersion(port)
	if version != "2.1.51" {
		t.Errorf("expected version 2.1.51, got %q", version)
	}
}

func TestQueryMasterVersion_Unreachable(t *testing.T) {
	version := QueryMasterVersion(59998)
	if version != "" {
		t.Errorf("expected empty version for unreachable port, got %q", version)
	}
}
