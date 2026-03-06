// cmd/sse-client-test is a manual HTTP MCP client for the RagCode MCP server.
// It primarily tests the Streamable HTTP (stateless) transport via POST /mcp
// and does best-effort parsing of SSE-style `data:` lines in responses.
//
// NOTE: The directory/binary name is legacy; a more accurate name would be `mcp-http-client-test`.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// callMCP sends a single JSON-RPC request to the /mcp endpoint and returns the response.
// The server uses Streamable HTTP Stateless — no session, no handshake required.
func callMCP(baseURL, id, method string, params any, timeout time.Duration) (map[string]any, error) {
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	// Accept both JSON and SSE — server picks JSON for single-message responses (stateless)
	req.Header.Set("Accept", "application/json, text/event-stream")

	client := &http.Client{Timeout: timeout}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST /mcp: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(b))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// If response is SSE, extract the data field
	contentType := resp.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "text/event-stream") {
		respBody = extractSSEData(respBody)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w\nBody: %s", err, string(respBody))
	}
	return result, nil
}

// extractSSEData parses `data: {...}` lines from an SSE response body.
func extractSSEData(body []byte) []byte {
	lines := strings.Split(string(body), "\n")
	var dataLines []string

	flushEvent := func() []byte {
		if len(dataLines) == 0 {
			return nil
		}
		joined := strings.Join(dataLines, "\n")
		trimmed := strings.TrimSpace(joined)
		if trimmed == "" || trimmed == "[DONE]" {
			return nil
		}
		return []byte(trimmed)
	}

	for _, rawLine := range lines {
		// Preserve internal spaces but normalize line endings.
		line := strings.TrimRight(rawLine, "\r")
		trimmed := strings.TrimSpace(line)

		// Blank line = end of current event.
		if trimmed == "" {
			if data := flushEvent(); data != nil {
				return data
			}
			dataLines = dataLines[:0]
			continue
		}

		if strings.HasPrefix(trimmed, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			dataLines = append(dataLines, payload)
		}
	}

	// Handle case where the last event is not terminated by a blank line.
	if data := flushEvent(); data != nil {
		return data
	}

	// Fallback: return the original body if we couldn't extract a valid SSE data payload.
	return body
}

func main() {
	serverURL := flag.String("url", "http://localhost:3000", "Base URL of the MCP server")
	workspacePath := flag.String("path", "", "Absolute path to a file in the workspace to index and search")
	query := flag.String("query", "IndexWorkspace implementation", "Search query to run")
	mode := flag.String("mode", "discovery", "rag_search_code mode: discovery or exact")
	limit := flag.Int("limit", 3, "Number of results to return")
	outPath := flag.String("out", "", "If set, write the final tool JSON response to this file")
	indexTimeout := flag.Duration("index-timeout", 2*time.Minute, "Timeout for index request")
	searchTimeout := flag.Duration("search-timeout", 30*time.Second, "Timeout for search request")
	skipIndex := flag.Bool("skip-index", false, "Skip indexing, just run the search query")
	flag.Parse()

	if *workspacePath == "" {
		cwd, _ := os.Getwd()
		*workspacePath = cwd + "/internal/service/engine/engine.go"
	}

	fmt.Printf("🔗 Server: %s/mcp\n", *serverURL)
	fmt.Printf("📁 Workspace: %s\n", *workspacePath)

	// Step 1: Index workspace (optional)
	if !*skipIndex {
		fmt.Println("\n📦 Triggering workspace indexing...")
		resp, err := callMCP(*serverURL, "index-1", "tools/call", map[string]any{
			"name": "rag_index_workspace",
			"arguments": map[string]any{
				"file_path": *workspacePath,
				"recreate":  false,
			},
		}, *indexTimeout)
		if err != nil {
			fmt.Printf("❌ Indexing failed: %v\n", err)
			os.Exit(1)
		}
		printResult("Index", resp)
	}

	// Step 2: Search
	fmt.Printf("\n🔍 Searching: %q (mode=%s, limit=%d)...\n", *query, *mode, *limit)
	resp, err := callMCP(*serverURL, "search-1", "tools/call", map[string]any{
		"name": "rag_search_code",
		"arguments": map[string]any{
			"query":     *query,
			"file_path": *workspacePath,
			"mode":      *mode,
			"limit":     *limit,
		},
	}, *searchTimeout)
	if err != nil {
		fmt.Printf("❌ Search failed: %v\n", err)
		os.Exit(1)
	}

	// Extract and display text content
	text := extractToolText(resp)
	if text == "" {
		fmt.Println("⚠️  No text content in response")
		pretty, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(pretty))
		os.Exit(1)
	}

	// Try to pretty-print if JSON
	var toolResp any
	if err := json.Unmarshal([]byte(text), &toolResp); err == nil {
		pretty, _ := json.MarshalIndent(toolResp, "", "  ")
		fmt.Printf("\n✅ Search Result:\n%s\n", string(pretty))
		if *outPath != "" {
			_ = os.WriteFile(*outPath, pretty, 0o644)
			fmt.Printf("💾 Saved to %s\n", *outPath)
		}
	} else {
		fmt.Printf("\n✅ Search Result:\n%s\n", text)
		if *outPath != "" {
			_ = os.WriteFile(*outPath, []byte(text), 0o644)
		}
	}
}

// printResult prints a brief summary of a tool response.
func printResult(label string, resp map[string]any) {
	text := extractToolText(resp)
	if text != "" {
		// Truncate for display
		if len(text) > 200 {
			text = text[:200] + "..."
		}
		fmt.Printf("  %s response: %s\n", label, text)
	} else {
		pretty, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Printf("  %s raw: %s\n", label, string(pretty))
	}
}

// extractToolText extracts the first text content from a tools/call MCP response.
func extractToolText(resp map[string]any) string {
	result, ok := resp["result"].(map[string]any)
	if !ok {
		return ""
	}
	content, ok := result["content"].([]any)
	if !ok || len(content) == 0 {
		return ""
	}
	first, ok := content[0].(map[string]any)
	if !ok {
		return ""
	}
	text, _ := first["text"].(string)
	return text
}
