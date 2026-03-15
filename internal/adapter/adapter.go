package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RunBridge reads JSON-RPC messages from stdin, forwards each as an HTTP POST
// to the daemon via local TCP port, and writes the JSON response to stdout.
//
// The adapter reads/writes single JSON payloads (no SSE framing).
// Accept header includes text/event-stream for StreamableHTTPHandler compatibility,
// but JSONResponse=true on the server forces pure JSON responses.
//
// Sticky workspace: after the first successful tool response, the daemon sets
// X-Resolved-Workspace in the response header. The adapter caches this value
// and sends X-Workspace-Root on all subsequent requests, eliminating repeated
// workspace detection on the daemon side. The workspaceHint parameter from
// the IDE is intentionally ignored — only the daemon-resolved root is trusted.
//
// Returns nil on stdin EOF (normal IDE shutdown).
func RunBridge(ctx context.Context, port int, stdin io.Reader, stdout io.Writer, workspaceHint string) error {
	client := &http.Client{
		Timeout: 5 * time.Minute, // prevent indefinite hangs on stalled daemon
	}

	scanner := bufio.NewScanner(stdin)
	// Allow up to 10MB per line (large MCP responses)
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	// Sticky workspace: learned from the daemon's X-Resolved-Workspace
	// response header. Once set, sent as X-Workspace-Root on every
	// subsequent request.
	var resolvedWorkspace string

	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			fmt.Sprintf("http://127.0.0.1:%d/mcp", port), bytes.NewReader([]byte(line)))
		if err != nil {
			writeJSONRPCError(stdout, line, fmt.Errorf("create request: %w", err))
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")

		// Send confirmed workspace root if we already learned it
		if resolvedWorkspace != "" {
			req.Header.Set("X-Workspace-Root", resolvedWorkspace)
		}

		resp, err := client.Do(req)
		if err != nil {
			writeJSONRPCError(stdout, line, fmt.Errorf("daemon unreachable: %w", err))
			continue
		}

		// Check for non-2xx status — daemon might return HTML errors
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			resp.Body.Close()
			writeJSONRPCError(stdout, line, fmt.Errorf("daemon returned HTTP %d", resp.StatusCode))
			continue
		}

		// Learn resolved workspace from daemon response header (first successful resolve)
		if rw := resp.Header.Get("X-Resolved-Workspace"); rw != "" && resolvedWorkspace == "" {
			resolvedWorkspace = rw
		}

		const maxResponseSize = 10 * 1024 * 1024 // 10MB
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize+1))
		resp.Body.Close()
		if err != nil {
			writeJSONRPCError(stdout, line, fmt.Errorf("read response: %w", err))
			continue
		}

		// Detect truncated response (exceeded max size)
		if len(body) > maxResponseSize {
			writeJSONRPCError(stdout, line, fmt.Errorf("response exceeds %dMB limit", maxResponseSize/(1024*1024)))
			continue
		}

		// Trim trailing newlines and write as single line
		body = bytes.TrimRight(body, "\n\r")
		fmt.Fprintf(stdout, "%s\n", body)
	}

	return scanner.Err()
}

// writeJSONRPCError writes a JSON-RPC error response to the writer.
// Extracts the ID from the original message so the client can match it.
func writeJSONRPCError(w io.Writer, originalMsg string, proxyErr error) {
	var parsed map[string]any
	var id any
	if json.Unmarshal([]byte(originalMsg), &parsed) == nil {
		id = parsed["id"]
	}
	errResp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error": map[string]any{
			"code":    -32000,
			"message": fmt.Sprintf("adapter: %v", proxyErr),
		},
	}
	data, _ := json.Marshal(errResp)
	fmt.Fprintf(w, "%s\n", data)
}
