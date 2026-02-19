package tests

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type sseClient struct {
	resp      *http.Response
	reader    *bufio.Reader
	outCh     chan []byte
	sessionID string
}

func newSSEClient(t *testing.T, url string) *sseClient {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("create SSE request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("open SSE stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected SSE status %d: %s", resp.StatusCode, string(body))
	}

	client := &sseClient{
		resp:   resp,
		reader: bufio.NewReader(resp.Body),
		outCh:  make(chan []byte, 16),
	}
	endpoint := client.readEvent(t)
	client.sessionID = extractSessionID(endpoint)
	if client.sessionID == "" {
		t.Fatalf("missing sessionid from SSE endpoint event: %s", string(endpoint))
	}
	go client.readLoop()
	return client
}

func (c *sseClient) readEvent(t *testing.T) []byte {
	var buf bytes.Buffer
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			t.Fatalf("read SSE event: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if buf.Len() > 0 {
				return buf.Bytes()
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			buf.WriteString(data)
		}
	}
}

func extractSessionID(endpoint []byte) string {
	text := string(endpoint)
	idx := strings.Index(text, "sessionid=")
	if idx == -1 {
		return ""
	}
	return strings.TrimSpace(text[idx+len("sessionid="):])
}

func (c *sseClient) readLoop() {
	defer close(c.outCh)
	var buf bytes.Buffer
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if buf.Len() > 0 {
				payload := append([]byte(nil), buf.Bytes()...)
				c.outCh <- payload
				buf.Reset()
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			buf.WriteString(data)
		}
	}
}

func (c *sseClient) Close() {
	if c.resp != nil {
		_ = c.resp.Body.Close()
	}
}

func sendJSONRPC(t *testing.T, baseURL, sessionID, id, method string, params any) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal jsonrpc: %v", err)
	}

	endpoint := baseURL + "/messages"
	if sessionID != "" {
		endpoint = endpoint + "?sessionid=" + sessionID
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create message request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post /messages: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected /messages status %d: %s", resp.StatusCode, string(respBody))
	}
}

func sendNotification(t *testing.T, baseURL, sessionID, method string, params any) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}

	endpoint := baseURL + "/messages"
	if sessionID != "" {
		endpoint = endpoint + "?sessionid=" + sessionID
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create notification request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post notification: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected notification status %d: %s", resp.StatusCode, string(respBody))
	}
}

func waitForResponse(t *testing.T, ch <-chan []byte, id string, timeout time.Duration) map[string]any {
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			t.Fatalf("timeout waiting for response id=%s", id)
		case data, ok := <-ch:
			if !ok {
				t.Fatalf("SSE stream closed before response id=%s", id)
			}
			if len(data) == 0 {
				continue
			}
			var msg map[string]any
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			if msgID, ok := msg["id"].(string); ok && msgID == id {
				return msg
			}
		}
	}
}

func parseToolText(t *testing.T, msg map[string]any) map[string]any {
	result, ok := msg["result"].(map[string]any)
	if !ok {
		t.Fatalf("missing result in response: %#v", msg)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatalf("missing content in response: %#v", msg)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		t.Fatalf("invalid content item: %#v", content[0])
	}
	text, ok := first["text"].(string)
	if !ok {
		t.Fatalf("missing text in content: %#v", first)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("parse tool text json: %v (text=%s)", err, text)
	}
	return parsed
}

func TestFunctionalSSETools(t *testing.T) {
	baseURL := os.Getenv("RAGCODE_SSE_URL")
	if baseURL == "" {
		baseURL = "http://localhost:3000"
	}

	sse := newSSEClient(t, baseURL+"/sse")
	defer sse.Close()

	// tools/list
	sendJSONRPC(t, baseURL, sse.sessionID, "init-1", "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo": map[string]any{
			"name":    "ragcode-functional-test",
			"version": "0.0.1",
		},
		"capabilities": map[string]any{},
	})
	_ = waitForResponse(t, sse.outCh, "init-1", 20*time.Second)
	sendNotification(t, baseURL, sse.sessionID, "notifications/initialized", map[string]any{})

	// tools/list
	sendJSONRPC(t, baseURL, sse.sessionID, "list-1", "tools/list", map[string]any{})
	msg := waitForResponse(t, sse.outCh, "list-1", 20*time.Second)
	if _, ok := msg["result"]; !ok {
		t.Fatalf("list_tools missing result: %#v", msg)
	}

	filePath := "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/internal/service/engine/engine.go"

	// rag_list_skills
	sendJSONRPC(t, baseURL, sse.sessionID, "skills-1", "tools/call", map[string]any{
		"name": "rag_list_skills",
		"arguments": map[string]any{
			"file_path": filePath,
		},
	})
	msg = waitForResponse(t, sse.outCh, "skills-1", 20*time.Second)
	_ = parseToolText(t, msg)

	// rag_install_skill (uninstall, install, uninstall)
	sendJSONRPC(t, baseURL, sse.sessionID, "skill-uninstall", "tools/call", map[string]any{
		"name": "rag_install_skill",
		"arguments": map[string]any{
			"skill_id":  "ragcode-sse",
			"action":    "uninstall",
			"file_path": filePath,
		},
	})
	_ = waitForResponse(t, sse.outCh, "skill-uninstall", 20*time.Second)

	sendJSONRPC(t, baseURL, sse.sessionID, "skill-install", "tools/call", map[string]any{
		"name": "rag_install_skill",
		"arguments": map[string]any{
			"skill_id":  "ragcode-sse",
			"action":    "install",
			"file_path": filePath,
		},
	})
	msg = waitForResponse(t, sse.outCh, "skill-install", 20*time.Second)
	_ = parseToolText(t, msg)

	sendJSONRPC(t, baseURL, sse.sessionID, "skill-uninstall-2", "tools/call", map[string]any{
		"name": "rag_install_skill",
		"arguments": map[string]any{
			"skill_id":  "ragcode-sse",
			"action":    "uninstall",
			"file_path": filePath,
		},
	})
	_ = waitForResponse(t, sse.outCh, "skill-uninstall-2", 20*time.Second)

	// rag_evaluate
	sendJSONRPC(t, baseURL, sse.sessionID, "eval-1", "tools/call", map[string]any{
		"name": "rag_evaluate",
		"arguments": map[string]any{
			"file_path": filePath,
		},
	})
	msg = waitForResponse(t, sse.outCh, "eval-1", 20*time.Second)
	_ = parseToolText(t, msg)

	// rag_search_code with indexing wait
	deadline := time.Now().Add(2 * time.Minute)
	for {
		sendJSONRPC(t, baseURL, sse.sessionID, "search-1", "tools/call", map[string]any{
			"name": "rag_search_code",
			"arguments": map[string]any{
				"query":     "SearchCode",
				"file_path": filePath,
				"limit":     3,
			},
		})
		msg = waitForResponse(t, sse.outCh, "search-1", 30*time.Second)
		resp := parseToolText(t, msg)
		status, _ := resp["status"].(string)
		if status == "indexing_started" || status == "indexing_in_progress" {
			if time.Now().After(deadline) {
				t.Fatalf("indexing did not complete before timeout")
			}
			time.Sleep(5 * time.Second)
			continue
		}
		if status != "success" && status != "no_results" {
			t.Fatalf("unexpected search status: %s", status)
		}
		break
	}

	// rag_search_code again after indexing
	fmt.Println("functional SSE tools test completed")
}
