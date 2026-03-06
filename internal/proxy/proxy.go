package proxy

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
)

// RunProxyMode runs a lightweight Stdio-to-HTTP proxy.
// It reads newline-delimited JSON-RPC messages from stdin, forwards them
// as HTTP POST requests to the master instance on the given port,
// and writes the responses back to stdout.
//
// This function blocks until stdin is closed (EOF) or the context is cancelled.
// It never returns under normal operation — the process exits when the IDE
// closes the stdin pipe.
func RunProxyMode(port int) {
	logger.Instance.Info("Entering PROXY mode → forwarding Stdio to http://127.0.0.1:%d/mcp", port)

	targetURL := fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
	client := &http.Client{Timeout: 5 * time.Minute} // long timeout for indexing operations

	scanner := bufio.NewScanner(os.Stdin)
	// MCP messages can be large (e.g. search results with full file contents).
	// Default scanner buffer is 64KB; expand to 10MB.
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		resp, err := forwardToMaster(client, targetURL, []byte(line))
		if err != nil {
			// If master is unreachable, send a JSON-RPC error back to the IDE.
			logger.Instance.Error("Proxy forward failed: %v", err)
			writeErrorResponse(os.Stdout, line, err)
			continue
		}

		// Write response as a single line to stdout (newline-delimited JSON).
		resp = bytes.TrimRight(resp, "\n\r")
		fmt.Fprintf(os.Stdout, "%s\n", resp)
	}

	if err := scanner.Err(); err != nil {
		logger.Instance.Error("Proxy stdin read error: %v", err)
	}

	logger.Instance.Info("Proxy mode: stdin closed (EOF), shutting down")
}

// forwardToMaster sends a JSON-RPC message to the master's HTTP endpoint
// and returns the response body.
func forwardToMaster(client *http.Client, url string, message []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(message))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusAccepted {
		return nil, fmt.Errorf("unexpected status %d: %s", resp.StatusCode, string(body))
	}

	// Validate response is well-formed JSON before forwarding to IDE.
	// Strategy: try json.Unmarshal first (handles application/json).
	// If it fails and Content-Type is SSE, extract JSON from data: lines.
	var validated json.RawMessage
	if json.Unmarshal(body, &validated) == nil {
		// Body is already valid JSON — use it directly.
		return validated, nil
	}

	// Not direct JSON — try SSE extraction if Content-Type matches.
	contentType := resp.Header.Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/event-stream") {
		return nil, fmt.Errorf("response is neither valid JSON nor SSE (content-type=%s, %d bytes)", contentType, len(body))
	}

	extracted := extractSSEPayload(body)
	if extracted == nil {
		return nil, fmt.Errorf("failed to extract valid JSON from SSE response (%d bytes)", len(body))
	}
	return extracted, nil
}

// extractSSEPayload parses SSE-formatted response body and returns
// the last valid JSON-RPC payload. Returns nil if no valid JSON is found.
// All extracted data is validated through json.Unmarshal to ensure
// only well-formed JSON is ever forwarded to the IDE.
func extractSSEPayload(body []byte) json.RawMessage {
	scanner := bufio.NewScanner(bytes.NewReader(body))
	var lastValid json.RawMessage
	var dataLines []string

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			// End of SSE event — flush accumulated data lines.
			if len(dataLines) > 0 {
				combined := strings.Join(dataLines, "\n")
				if combined != "[DONE]" {
					var msg json.RawMessage
					if json.Unmarshal([]byte(combined), &msg) == nil {
						lastValid = msg
					}
				}
				dataLines = dataLines[:0]
			}
			continue
		}

		if strings.HasPrefix(trimmed, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "data:"))
			dataLines = append(dataLines, payload)
		}
	}

	// Handle unterminated last event.
	if len(dataLines) > 0 {
		combined := strings.Join(dataLines, "\n")
		if combined != "[DONE]" {
			var msg json.RawMessage
			if json.Unmarshal([]byte(combined), &msg) == nil {
				lastValid = msg
			}
		}
	}

	return lastValid
}

// writeErrorResponse writes a JSON-RPC error response to the writer,
// extracting the request ID from the original message if possible.
func writeErrorResponse(w io.Writer, originalMsg string, proxyErr error) {
	id := extractRequestID(originalMsg)
	errResp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    -32000,
			"message": fmt.Sprintf("proxy: master unreachable: %v", proxyErr),
		},
	}
	data, _ := json.Marshal(errResp)
	fmt.Fprintf(w, "%s\n", data)
}

// extractRequestID attempts to extract the "id" field from a JSON-RPC message.
func extractRequestID(msg string) any {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(msg), &parsed); err != nil {
		return nil
	}
	return parsed["id"]
}
