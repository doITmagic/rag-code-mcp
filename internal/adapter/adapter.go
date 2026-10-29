package adapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
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
// Sticky workspace: when a tool resolves a workspace, the daemon sets
// X-Resolved-Workspace in the response header. The adapter keeps the latest value
// and sends X-Workspace-Root on all subsequent requests, eliminating repeated
// workspace detection on the daemon side. The workspaceHint parameter from
// the IDE is intentionally ignored — only the daemon-resolved root is trusted.
//
// When the daemon cannot be dialed and is not healthy, restart (if non-nil)
// is called to start it again and the request is retried once.
//
// Returns nil on stdin EOF (normal IDE shutdown).
func RunBridge(ctx context.Context, port int, stdin io.Reader, stdout io.Writer, workspaceHint string, restart func() error) error {
	client := &http.Client{
		Timeout: 5 * time.Minute, // prevent indefinite hangs on stalled daemon
	}

	reader := bufio.NewReaderSize(stdin, 64*1024)
	// Allow up to 10MB per line; a longer line is drained and rejected.
	const maxLineSize = 10 * 1024 * 1024

	// Sticky workspace: learned from the daemon's X-Resolved-Workspace
	// response header. Once set, sent as X-Workspace-Root on every
	// subsequent request.
	var resolvedWorkspace string

	for {
		raw, tooLong, err := readLine(reader, maxLineSize)
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		if tooLong {
			writeJSONRPCError(stdout, "", fmt.Errorf("request exceeds %dMB limit", maxLineSize/(1024*1024)))
			continue
		}
		line := string(raw)
		if strings.TrimSpace(line) == "" {
			continue
		}

		post := func() (*http.Response, error) {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				fmt.Sprintf("http://127.0.0.1:%d/mcp", port), strings.NewReader(line))
			if err != nil {
				return nil, fmt.Errorf("create request: %w", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json, text/event-stream")

			// Send confirmed workspace root if we already learned it
			if resolvedWorkspace != "" {
				req.Header.Set("X-Workspace-Root", resolvedWorkspace)
			}
			return client.Do(req)
		}

		resp, err := post()
		// The daemon is gone: start it again and retry this request once.
		// Only a failed dial is retried; a request the daemon received may
		// be what killed it, or not be safe to run twice.
		var opErr *net.OpError
		if err != nil && restart != nil && ctx.Err() == nil && errors.As(err, &opErr) && opErr.Op == "dial" {
			if running, _ := IsDaemonRunning(port); !running {
				if rerr := restart(); rerr != nil {
					err = fmt.Errorf("%w; restart failed: %v", err, rerr)
				} else {
					resp, err = post()
				}
			}
		}
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

		// Follow explicit project switches confirmed by the daemon.
		if rw := resp.Header.Get("X-Resolved-Workspace"); rw != "" {
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
		// Notifications get 202 with no body: nothing to relay.
		if len(bytes.TrimSpace(body)) == 0 {
			continue
		}
		fmt.Fprintf(stdout, "%s\n", body)
	}
}

// readLine reads one line without its line ending. A line longer than limit is
// read to its end and discarded, and tooLong is reported instead.
func readLine(r *bufio.Reader, limit int) (line []byte, tooLong bool, err error) {
	for {
		chunk, isPrefix, err := r.ReadLine()
		if err != nil {
			return nil, false, err
		}
		if !tooLong {
			line = append(line, chunk...)
			if len(line) > limit {
				line, tooLong = nil, true
			}
		}
		if !isPrefix {
			return line, tooLong, nil
		}
	}
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
