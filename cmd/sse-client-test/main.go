package main

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
	"time"
)

type sseClient struct {
	resp      *http.Response
	reader    *bufio.Reader
	outCh     chan []byte
	sessionID string
}

func newSSEClient(url string) (*sseClient, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create SSE request: %v", err)
	}
	req.Header.Set("Accept", "text/event-stream")

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("open SSE stream: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected SSE status %d: %s", resp.StatusCode, string(body))
	}

	client := &sseClient{
		resp:   resp,
		reader: bufio.NewReader(resp.Body),
		outCh:  make(chan []byte, 16),
	}
	endpoint, err := client.readEvent()
	if err != nil {
		return nil, err
	}
	client.sessionID = extractSessionID(endpoint)
	if client.sessionID == "" {
		return nil, fmt.Errorf("missing sessionid from SSE endpoint event: %s", string(endpoint))
	}
	go client.readLoop()
	return client, nil
}

func (c *sseClient) readEvent() ([]byte, error) {
	var buf bytes.Buffer
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("read SSE event: %v", err)
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if buf.Len() > 0 {
				return buf.Bytes(), nil
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

func sendJSONRPC(baseURL, sessionID, id, method string, params any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal jsonrpc: %v", err)
	}

	endpoint := baseURL + "/messages"
	if sessionID != "" {
		endpoint = endpoint + "?sessionid=" + sessionID
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create message request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post /messages: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected /messages status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func sendNotification(baseURL, sessionID, method string, params any) error {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal notification: %v", err)
	}

	endpoint := baseURL + "/messages"
	if sessionID != "" {
		endpoint = endpoint + "?sessionid=" + sessionID
	}
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create notification request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post notification: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("unexpected notification status %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}

func waitForResponse(ch <-chan []byte, id string, timeout time.Duration) (map[string]any, error) {
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("timeout waiting for response id=%s", id)
		case data, ok := <-ch:
			if !ok {
				return nil, fmt.Errorf("SSE stream closed before response id=%s", id)
			}
			if len(data) == 0 {
				continue
			}
			var msg map[string]any
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			if msgID, ok := msg["id"].(string); ok && msgID == id {
				return msg, nil
			}
		}
	}
}

func main() {
	baseURL := "http://localhost:3000"
	sse, err := newSSEClient(baseURL + "/sse")
	if err != nil {
		fmt.Printf("Error creating SSE client: %v\n", err)
		os.Exit(1)
	}
	defer sse.Close()

	fmt.Printf("Connected to SSE session: %s\n", sse.sessionID)

	// Initialize
	err = sendJSONRPC(baseURL, sse.sessionID, "init-1", "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"clientInfo": map[string]any{
			"name":    "ragcode-cli-test",
			"version": "0.0.1",
		},
		"capabilities": map[string]any{},
	})
	if err != nil {
		fmt.Printf("Init failed: %v\n", err)
		os.Exit(1)
	}
	_, err = waitForResponse(sse.outCh, "init-1", 10*time.Second)
	if err != nil {
		fmt.Printf("Wait init failed: %v\n", err)
		os.Exit(1)
	}
	_ = sendNotification(baseURL, sse.sessionID, "notifications/initialized", map[string]any{})

	// Use an absolute path for detection
	cwd, _ := os.Getwd()
	filePath := cwd + "/internal/service/engine/engine.go"

	// Force Re-Index
	fmt.Println("Triggering Workspace Re-Indexing...")
	err = sendJSONRPC(baseURL, sse.sessionID, "index-1", "tools/call", map[string]any{
		"name": "rag_index_workspace",
		"arguments": map[string]any{
			"file_path": filePath,
			"recreate":  true,
		},
	})
	if err != nil {
		fmt.Printf("Indexing request failed: %v\n", err)
		os.Exit(1)
	}
	// Wait for response, ignore msgId
	_, err = waitForResponse(sse.outCh, "index-1", 30*time.Second)
	if err != nil {
		fmt.Printf("Wait index failed: %v\n", err)
		os.Exit(1)
	}

	// Wait for indexing to complete (since it runs async)
	// We check for "status": "indexing_started".
	// The client test has logic for polling, let's simplify and just wait a bit or poll search
	fmt.Println("Indexing started... Waiting/Polling for completion...")

	// Ideally, poll search until results come back for "IndexWorkspace"
	deadline := time.Now().Add(2 * time.Minute)
	for {
		if time.Now().After(deadline) {
			fmt.Println("Indexing timed out")
			os.Exit(1)
		}

		fmt.Println("Polling search status...")
		// Use a specific query to probe if indexing is done
		err = sendJSONRPC(baseURL, sse.sessionID, "poll-search", "tools/call", map[string]any{
			"name": "rag_search_code",
			"arguments": map[string]any{
				"query":     "IndexingStatus",
				"file_path": filePath,
				"limit":     1,
			},
		})
		if err != nil {
			fmt.Println("Poll search failed, retrying...")
			time.Sleep(2 * time.Second)
			continue
		}

		pollMsg, err := waitForResponse(sse.outCh, "poll-search", 10*time.Second)
		if err != nil {
			fmt.Println("Wait poll failed, retrying...")
			time.Sleep(2 * time.Second)
			continue
		}

		// check response content for "indexing_started" or actual results
		// parse logic simplified from test
		if result, ok := pollMsg["result"].(map[string]any); ok {
			if content, ok := result["content"].([]any); ok && len(content) > 0 {
				if first, ok := content[0].(map[string]any); ok {
					if text, ok := first["text"].(string); ok {
						if strings.Contains(text, "indexing_started") || strings.Contains(text, "indexing_in_progress") {
							fmt.Println("Still indexing...")
							time.Sleep(2 * time.Second)
							continue
						}
						// If we get here and it's not indexing status, assume done
						fmt.Println("Indexing complete or search successful!")
						break
					}
				}
			}
		}
		time.Sleep(2 * time.Second)
	}

	// Search Code Query
	query := "IndexWorkspace implementation"
	// Use an absolute path for detection to work correctly based on the current context strategy
	// cwd, _ := os.Getwd() // filePath is already defined above
	// filePath := cwd + "/internal/service/engine/engine.go" // Just as a reference point

	fmt.Printf("Sending search query via SSE: '%s'...\n", query)
	err = sendJSONRPC(baseURL, sse.sessionID, "search-1", "tools/call", map[string]any{
		"name": "rag_search_code",
		"arguments": map[string]any{
			"query":     query,
			"file_path": filePath,
			"limit":     3,
		},
	})
	if err != nil {
		fmt.Printf("Search request failed: %v\n", err)
		os.Exit(1)
	}

	msg, err := waitForResponse(sse.outCh, "search-1", 30*time.Second)
	if err != nil {
		fmt.Printf("Wait search failed: %v\n", err)
		os.Exit(1)
	}

	// Parse result
	result, ok := msg["result"].(map[string]any)
	if !ok {
		fmt.Printf("Missing result in response\n")
		os.Exit(1)
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		fmt.Printf("Missing content in result\n")
		os.Exit(1)
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		fmt.Printf("Invalid content item\n")
		os.Exit(1)
	}
	text, ok := first["text"].(string)
	if !ok {
		fmt.Printf("Missing text in content\n")
		os.Exit(1)
	}

	// Try to pretty print the JSON response from the tool
	var toolResp map[string]any
	if err := json.Unmarshal([]byte(text), &toolResp); err == nil {
		pretty, _ := json.MarshalIndent(toolResp, "", "  ")
		fmt.Printf("Tool Response:\n%s\n", string(pretty))
	} else {
		fmt.Printf("Tool Response (raw):\n%s\n", text)
	}
}
