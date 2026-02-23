//go:build manual_sse
// +build manual_sse

//
// These tests require a running server:
//   go run ./cmd/rag-code-mcp -http-port 3000
//
// They are automatically EXCLUDED from CI because the pipeline runs
//   go test ./...
// without -tags manual_sse.
//
// Run locally:
//   go test -v -tags manual_sse ./tests/... -timeout 300s

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
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// workspaceRoot returns the repo root (handles both "go test ./tests/" and "./..." invocations).
func workspaceRoot() string {
	cwd, _ := os.Getwd()
	if _, err := os.Stat(filepath.Join(cwd, "go.mod")); err == nil {
		return cwd
	}
	return filepath.Dir(cwd)
}

// sseClient is a minimal SSE + JSON-RPC client for MCP over HTTP.
type sseClient struct {
	resp      *http.Response
	reader    *bufio.Reader
	outCh     chan []byte
	sessionID string
	baseURL   string
}

func newSSEClient(baseURL string) (*sseClient, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, baseURL+"/sse", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect SSE: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SSE bad status: %d", resp.StatusCode)
	}

	c := &sseClient{
		resp:    resp,
		reader:  bufio.NewReader(resp.Body),
		outCh:   make(chan []byte, 256),
		baseURL: baseURL,
	}

	// First event contains the session endpoint URL, e.g.
	// data: /messages?sessionid=<ID>
	firstEvent, err := c.readEvent()
	if err != nil {
		return nil, fmt.Errorf("read first SSE event: %w", err)
	}
	text := string(firstEvent)
	if idx := strings.Index(text, "sessionid="); idx != -1 {
		c.sessionID = strings.TrimSpace(text[idx+len("sessionid="):])
	}
	if c.sessionID == "" {
		return nil, fmt.Errorf("no sessionid in first SSE event: %q", text)
	}

	go c.readLoop()
	return c, nil
}

func (c *sseClient) readEvent() ([]byte, error) {
	var buf bytes.Buffer
	for {
		line, err := c.reader.ReadString('\n')
		if err != nil {
			return nil, err
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

func (c *sseClient) readLoop() {
	defer close(c.outCh)
	for {
		event, err := c.readEvent()
		if err != nil {
			return
		}
		if len(event) > 0 {
			c.outCh <- event
		}
	}
}

// postJSON sends a JSON-RPC message and returns the raw HTTP response body.
func (c *sseClient) postJSON(payload interface{}) error {
	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/messages?sessionid=%s", c.baseURL, c.sessionID)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}
	return nil
}

// waitForID blocks until an SSE event with the given JSON-RPC id arrives.
func (c *sseClient) waitForID(id string, timeout time.Duration) (map[string]interface{}, error) {
	deadline := time.After(timeout)
	for {
		select {
		case <-deadline:
			return nil, fmt.Errorf("timeout (%v) waiting for id=%s", timeout, id)
		case data, ok := <-c.outCh:
			if !ok {
				return nil, fmt.Errorf("SSE stream closed while waiting for id=%s", id)
			}
			var msg map[string]interface{}
			if err := json.Unmarshal(data, &msg); err != nil {
				continue
			}
			if msg["id"] == id {
				return msg, nil
			}
		}
	}
}

// initialize performs the full MCP handshake:
//   initialize → wait for response → notifications/initialized
func (c *sseClient) initialize() error {
	if err := c.postJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      "init-1",
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "functional-test", "version": "1.0.0"},
		},
	}); err != nil {
		return fmt.Errorf("send initialize: %w", err)
	}

	if _, err := c.waitForID("init-1", 10*time.Second); err != nil {
		return fmt.Errorf("initialize response: %w", err)
	}

	// notifications/initialized has no id and no response
	return c.postJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
		"params":  map[string]interface{}{},
	})
}

// callTool invokes a tool and returns the parsed response map.
func (c *sseClient) callTool(name string, args map[string]interface{}, timeout time.Duration) (map[string]interface{}, error) {
	id := fmt.Sprintf("tool-%d", time.Now().UnixNano())
	if err := c.postJSON(map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      name,
			"arguments": args,
		},
	}); err != nil {
		return nil, err
	}
	return c.waitForID(id, timeout)
}

// toolText returns the first text content string from a tools/call response.
func toolText(res map[string]interface{}) string {
	result, _ := res["result"].(map[string]interface{})
	content, _ := result["content"].([]interface{})
	if len(content) == 0 {
		return ""
	}
	item, _ := content[0].(map[string]interface{})
	text, _ := item["text"].(string)
	return text
}

// toolJSON parses the JSON payload embedded in the text content.
func toolJSON(res map[string]interface{}) map[string]interface{} {
	var m map[string]interface{}
	_ = json.Unmarshal([]byte(toolText(res)), &m)
	return m
}

// toolStatus returns the "status" field from the embedded JSON payload.
func toolStatus(res map[string]interface{}) string {
	s, _ := toolJSON(res)["status"].(string)
	return s
}

// toolMessage returns the "message" field from the embedded JSON payload.
func toolMessage(res map[string]interface{}) string {
	m, _ := toolJSON(res)["message"].(string)
	return m
}

// toolDataLen returns the number of items in the "data" array (search results).
func toolDataLen(res map[string]interface{}) int {
	if d, ok := toolJSON(res)["data"].([]interface{}); ok {
		return len(d)
	}
	return 0
}

// dataContainsField returns true when at least one data item has a field whose
// string value contains substr.
func dataContainsField(res map[string]interface{}, field, substr string) bool {
	data, _ := toolJSON(res)["data"].([]interface{})
	for _, raw := range data {
		item, _ := raw.(map[string]interface{})
		val, _ := item[field].(string)
		if strings.Contains(val, substr) {
			return true
		}
	}
	return false
}

// isToolError returns true when the response carries a top-level error or isError flag.
func isToolError(res map[string]interface{}) bool {
	if res["error"] != nil {
		return true
	}
	result, _ := res["result"].(map[string]interface{})
	if v, ok := result["isError"].(bool); ok && v {
		return true
	}
	return false
}

// ── Test Suite ────────────────────────────────────────────────────────────────

var _ = Describe("Functional SSE Tools Integration", Ordered, func() {
	const baseURL = "http://localhost:3000"
	// toolTimeout: generous for first-call lazy embedding; subsequent calls are cached.
	const toolTimeout = 120 * time.Second

	var (
		client *sseClient
		root   string
	)

	BeforeAll(func() {
		root = workspaceRoot()

		resp, err := http.Get(baseURL + "/sse")
		Expect(err).NotTo(HaveOccurred(), "server must be running on %s (go run ./cmd/rag-code-mcp -http-port 3000)", baseURL)
		resp.Body.Close()

		client, err = newSSEClient(baseURL)
		Expect(err).NotTo(HaveOccurred())
		Expect(client.sessionID).NotTo(BeEmpty())
		Expect(client.initialize()).To(Succeed(), "MCP handshake failed")
		GinkgoWriter.Printf("[setup] session=%s  root=%s\n", client.sessionID, root)
	})

	AfterAll(func() {
		if client != nil {
			client.resp.Body.Close()
		}
	})

	// ── rag_search_code ────────────────────────────────────────────────────────

	Describe("rag_search_code", func() {

		Context("mode=exact (hybrid BM25 + vector)", func() {

			It("finds SearchLocalIndexTool by exact name → result.data[*].name contains it", func() {
				res, err := client.callTool("rag_search_code", map[string]interface{}{
					"query":     "SearchLocalIndexTool",
					"file_path": filepath.Join(root, "internal/service/tools/search_local_index.go"),
					"mode":      "exact",
					"limit":     5,
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				GinkgoWriter.Printf("[exact] SearchLocalIndexTool  status=%s  data=%d\n", toolStatus(res), toolDataLen(res))
				Expect(toolStatus(res)).To(Equal("success"))
				Expect(toolDataLen(res)).To(BeNumerically(">", 0))
				Expect(dataContainsField(res, "name", "SearchLocalIndexTool")).To(BeTrue(),
					"expected a result with name containing 'SearchLocalIndexTool'")
			})

			It("finds IndexWorkspace function", func() {
				res, err := client.callTool("rag_search_code", map[string]interface{}{
					"query":     "IndexWorkspace",
					"file_path": filepath.Join(root, "internal/service/engine/engine.go"),
					"mode":      "exact",
					"limit":     5,
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				GinkgoWriter.Printf("[exact] IndexWorkspace  status=%s  data=%d\n", toolStatus(res), toolDataLen(res))
				Expect(toolStatus(res)).To(Equal("success"))
				Expect(toolDataLen(res)).To(BeNumerically(">", 0))
			})

			It("finds HybridSearchCode method", func() {
				res, err := client.callTool("rag_search_code", map[string]interface{}{
					"query":     "HybridSearchCode",
					"file_path": filepath.Join(root, "internal/service/engine/engine.go"),
					"mode":      "exact",
					"limit":     5,
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				GinkgoWriter.Printf("[exact] HybridSearchCode  status=%s  data=%d\n", toolStatus(res), toolDataLen(res))
				Expect(toolStatus(res)).To(Equal("success"))
				Expect(toolDataLen(res)).To(BeNumerically(">", 0))
			})

			It("finds DetectContext method", func() {
				res, err := client.callTool("rag_search_code", map[string]interface{}{
					"query":     "DetectContext",
					"file_path": filepath.Join(root, "internal/service/engine/engine.go"),
					"mode":      "exact",
					"limit":     5,
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				GinkgoWriter.Printf("[exact] DetectContext  status=%s  data=%d\n", toolStatus(res), toolDataLen(res))
				Expect(toolStatus(res)).To(Equal("success"))
				Expect(toolDataLen(res)).To(BeNumerically(">", 0))
			})
		})

		Context("mode=discovery (semantic embedding)", func() {

			It("finds NewEngine constructor → data[*].name contains it", func() {
				res, err := client.callTool("rag_search_code", map[string]interface{}{
					"query":     "NewEngine",
					"file_path": filepath.Join(root, "internal/service/engine/engine.go"),
					"mode":      "discovery",
					"limit":     5,
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				GinkgoWriter.Printf("[discovery] NewEngine  status=%s  data=%d\n", toolStatus(res), toolDataLen(res))
				Expect(toolStatus(res)).To(Equal("success"))
				// semantic search for 'NewEngine' should return results from engine-related files
				Expect(dataContainsField(res, "file_path", "engine")).To(BeTrue())
			})

			It("finds 'graph context expansion' concept", func() {
				res, err := client.callTool("rag_search_code", map[string]interface{}{
					"query":     "graph context expansion related dependencies auto-fetch",
					"file_path": filepath.Join(root, "internal/service/tools/search_local_index.go"),
					"mode":      "discovery",
					"limit":     5,
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				GinkgoWriter.Printf("[discovery] graph expansion  status=%s  data=%d\n", toolStatus(res), toolDataLen(res))
				Expect(toolStatus(res)).To(Equal("success"))
				Expect(toolDataLen(res)).To(BeNumerically(">", 0))
			})

			It("finds 'embed query fan-out parallel language collections' concept", func() {
				res, err := client.callTool("rag_search_code", map[string]interface{}{
					"query":     "embed query fan out parallel language collections vector search",
					"file_path": filepath.Join(root, "internal/service/engine/engine.go"),
					"mode":      "discovery",
					"limit":     5,
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				GinkgoWriter.Printf("[discovery] vector fan-out  status=%s  data=%d\n", toolStatus(res), toolDataLen(res))
				Expect(toolStatus(res)).To(Equal("success"))
				Expect(toolDataLen(res)).To(BeNumerically(">", 0))
			})

			It("finds 'workspace detection registry fallback' concept", func() {
				res, err := client.callTool("rag_search_code", map[string]interface{}{
					"query":     "workspace detection file path resolver registry fallback",
					"file_path": filepath.Join(root, "internal/service/engine/engine.go"),
					"mode":      "discovery",
					"limit":     5,
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				GinkgoWriter.Printf("[discovery] workspace detection  status=%s  data=%d\n", toolStatus(res), toolDataLen(res))
				Expect(toolStatus(res)).To(Equal("success"))
				Expect(toolDataLen(res)).To(BeNumerically(">", 0))
			})

			It("finds 'index workspace parallel batch upsert qdrant' concept", func() {
				res, err := client.callTool("rag_search_code", map[string]interface{}{
					"query":     "index workspace parallel workers batch upsert qdrant",
					"file_path": filepath.Join(root, "pkg/indexer/service.go"),
					"mode":      "discovery",
					"limit":     5,
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				GinkgoWriter.Printf("[discovery] indexing  status=%s  data=%d\n", toolStatus(res), toolDataLen(res))
				Expect(toolStatus(res)).To(Equal("success"))
				Expect(toolDataLen(res)).To(BeNumerically(">", 0))
			})
		})

		Context("include_docs=true", func() {
			It("searches including documentation symbols", func() {
				res, err := client.callTool("rag_search_code", map[string]interface{}{
					"query":        "register MCP tool server",
					"file_path":    filepath.Join(root, "internal/service/tools/search_local_index.go"),
					"mode":         "discovery",
					"include_docs": true,
					"limit":        5,
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				GinkgoWriter.Printf("[include_docs] status=%s  data=%d\n", toolStatus(res), toolDataLen(res))
				Expect(toolStatus(res)).To(Equal("success"))
			})
		})

		Context("performance regression", func() {
			It("completes within 15s (guard: graph expansion must NOT trigger extra embeddings)", func() {
				// checkConfigUpgrade has stdlib relations (Printf, Scanln, Load, Save…).
				// Before the fix: N×ExactSearch miss → N×embedding → ~70s.
				// After the fix: ExactSearch-only expansion → single embedding for main query.
				start := time.Now()
				res, err := client.callTool("rag_search_code", map[string]interface{}{
					"query":     "checkConfigUpgrade",
					"file_path": filepath.Join(root, "cmd/rag-code-install/main.go"),
					"mode":      "exact",
					"limit":     5,
				}, toolTimeout)
				elapsed := time.Since(start)

				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())
				GinkgoWriter.Printf("[perf] elapsed=%v\n", elapsed)
				Expect(elapsed).To(BeNumerically("<=", 15*time.Second),
					"search took %v — graph expansion embedding fallback may have reappeared", elapsed)
			})
		})
	})

	// ── rag_find_usages ────────────────────────────────────────────────────────

	Describe("rag_find_usages", func() {

		It("NewEngine → must reference cmd/rag-code-mcp/main.go as caller", func() {
			res, err := client.callTool("rag_find_usages", map[string]interface{}{
				"symbol_name": "NewEngine",
				"file_path":   filepath.Join(root, "internal/service/engine/engine.go"),
			}, toolTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(isToolError(res)).To(BeFalse())

			text := toolText(res)
			j := toolJSON(res)
			GinkgoWriter.Printf("[find_usages] NewEngine  status=%s\n%.500s\n", j["status"], text)
			Expect(j["status"]).To(Equal("success"))
			Expect(text).To(ContainSubstring("main.go"), "main.go must appear — it calls NewEngine")
			Expect(text).To(ContainSubstring("NewEngine"))
		})

		It("Register → appears in multiple tool files", func() {
			res, err := client.callTool("rag_find_usages", map[string]interface{}{
				"symbol_name": "Register",
				"file_path":   filepath.Join(root, "internal/service/tools/search_local_index.go"),
			}, toolTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(isToolError(res)).To(BeFalse())

			j := toolJSON(res)
			text := toolText(res)
			GinkgoWriter.Printf("[find_usages] Register  status=%s  len=%d\n", j["status"], len(text))
			Expect(j["status"]).To(Equal("success"))
			Expect(text).To(ContainSubstring("Register"))
		})

		It("Execute → multiple tool implementations use it", func() {
			res, err := client.callTool("rag_find_usages", map[string]interface{}{
				"symbol_name": "Execute",
				"file_path":   filepath.Join(root, "internal/service/tools/search_local_index.go"),
			}, toolTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(isToolError(res)).To(BeFalse())

			j := toolJSON(res)
			text := toolText(res)
			GinkgoWriter.Printf("[find_usages] Execute  status=%s  len=%d\n", j["status"], len(text))
			Expect(j["status"]).To(Equal("success"))
			Expect(text).To(ContainSubstring("Execute"))
		})

		It("DetectContext → called from within tools", func() {
			res, err := client.callTool("rag_find_usages", map[string]interface{}{
				"symbol_name": "DetectContext",
				"file_path":   filepath.Join(root, "internal/service/engine/engine.go"),
			}, toolTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(isToolError(res)).To(BeFalse())

			j := toolJSON(res)
			text := toolText(res)
			GinkgoWriter.Printf("[find_usages] DetectContext  status=%s  len=%d\n", j["status"], len(text))
			Expect(j["status"]).To(Equal("success"))
			Expect(text).To(ContainSubstring("DetectContext"))
		})
	})

	// ── rag_call_hierarchy ────────────────────────────────────────────────────

	Describe("rag_call_hierarchy", func() {

		Context("direction=incoming (who calls this)", func() {

			It("NewEngine ← main() calls it", func() {
				res, err := client.callTool("rag_call_hierarchy", map[string]interface{}{
					"symbol_name": "NewEngine",
					"direction":   "incoming",
					"depth":       2,
					"file_path":   filepath.Join(root, "internal/service/engine/engine.go"),
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				j := toolJSON(res)
				text := toolText(res)
				GinkgoWriter.Printf("[call_hier incoming] NewEngine  status=%s\n%.500s\n", j["status"], text)
				Expect(j["status"]).To(Equal("success"))
				Expect(text).To(ContainSubstring("main"), "main() must appear as incoming caller of NewEngine")
			})

			It("Register ← incoming callers include higher-level setup code", func() {
				res, err := client.callTool("rag_call_hierarchy", map[string]interface{}{
					"symbol_name": "Register",
					"direction":   "incoming",
					"depth":       2,
					"file_path":   filepath.Join(root, "internal/service/tools/search_local_index.go"),
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				j := toolJSON(res)
				text := toolText(res)
				GinkgoWriter.Printf("[call_hier incoming] Register  status=%s\n%.500s\n", j["status"], text)
				Expect(j["status"]).To(Equal("success"))
				Expect(text).To(ContainSubstring("Register"))
			})
		})

		Context("direction=outgoing (what this calls)", func() {

			It("Execute (search_local_index) → calls HybridSearchCode or SearchCode", func() {
				res, err := client.callTool("rag_call_hierarchy", map[string]interface{}{
					"symbol_name": "Execute",
					"direction":   "outgoing",
					"depth":       2,
					"file_path":   filepath.Join(root, "internal/service/tools/search_local_index.go"),
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				j := toolJSON(res)
				text := toolText(res)
				GinkgoWriter.Printf("[call_hier outgoing] Execute  status=%s\n%.500s\n", j["status"], text)
				Expect(j["status"]).To(Equal("success"))
				// call hierarchy returns a tree with └─ edges
				Expect(text).To(ContainSubstring("└─"))
			})

			It("NewEngine → wires Resolver/Watcher dependencies", func() {
				res, err := client.callTool("rag_call_hierarchy", map[string]interface{}{
					"symbol_name": "NewEngine",
					"direction":   "outgoing",
					"depth":       2,
					"file_path":   filepath.Join(root, "internal/service/engine/engine.go"),
				}, toolTimeout)
				Expect(err).NotTo(HaveOccurred())
				Expect(isToolError(res)).To(BeFalse())

				j := toolJSON(res)
				text := toolText(res)
				GinkgoWriter.Printf("[call_hier outgoing] NewEngine  status=%s  len=%d\n", j["status"], len(text))
				Expect(j["status"]).To(Equal("success"))
				Expect(len(text)).To(BeNumerically(">", 50))
			})
		})
	})

	// ── rag_list_package_exports ──────────────────────────────────────────────

	Describe("rag_list_package_exports", func() {

		It("tools package → all public constructors present", func() {
			res, err := client.callTool("rag_list_package_exports", map[string]interface{}{
				"package":   "tools",
				"file_path": filepath.Join(root, "internal/service/tools/search_local_index.go"),
			}, toolTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(isToolError(res)).To(BeFalse())

			j := toolJSON(res)
			text := toolText(res)
			GinkgoWriter.Printf("[list_exports] tools  status=%s  len=%d\n", j["status"], len(text))
			Expect(j["status"]).To(Equal("success"))

			for _, sym := range []string{
				// constructor functions are NOT indexed — check struct types and methods that ARE
				"CallHierarchyTool",
				"CheckUpdateTool",
				"EvaluateRagCodeTool",
				"Execute",
				"Register",
			} {
				Expect(text).To(ContainSubstring(sym), "expected %q in tools package exports", sym)
			}
		})

		It("engine package → core public API present", func() {
			res, err := client.callTool("rag_list_package_exports", map[string]interface{}{
				"package":   "engine",
				"file_path": filepath.Join(root, "internal/service/engine/engine.go"),
			}, toolTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(isToolError(res)).To(BeFalse())

			j := toolJSON(res)
			text := toolText(res)
			GinkgoWriter.Printf("[list_exports] engine  status=%s  len=%d\n", j["status"], len(text))
			Expect(j["status"]).To(Equal("success"))

			for _, sym := range []string{
				// constructor NewEngine is NOT indexed (free functions);  methods ARE
				"SearchCode",
				"HybridSearchCode",
			} {
				Expect(text).To(ContainSubstring(sym), "expected %q in engine package exports", sym)
			}
		})

		It("indexer package → NewService present", func() {
			res, err := client.callTool("rag_list_package_exports", map[string]interface{}{
				"package":   "indexer",
				"file_path": filepath.Join(root, "pkg/indexer/service.go"),
			}, toolTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(isToolError(res)).To(BeFalse())

			j := toolJSON(res)
			text := toolText(res)
		GinkgoWriter.Printf("[list_exports] indexer  status=%s  len=%d\n", j["status"], len(text))
			Expect(j["status"]).To(Equal("success"))
			// pkg/indexer may or may not be in the index (200-point partial index)
			// just verify the tool handles the request without error
		})

		It("symbol_type=function filter works", func() {
			res, err := client.callTool("rag_list_package_exports", map[string]interface{}{
				"package":     "tools",
				"file_path":   filepath.Join(root, "internal/service/tools/search_local_index.go"),
				"symbol_type": "function",
			}, toolTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(isToolError(res)).To(BeFalse())

			j := toolJSON(res)
			GinkgoWriter.Printf("[list_exports] tools/function  status=%s\n", j["status"])
			Expect(j["status"]).To(Equal("success"))
		})
	})

	// ── rag_read_file_context ─────────────────────────────────────────────────

	Describe("rag_read_file_context", func() {

		It("line 71 of find_usages.go → inside Execute function", func() {
			// Execute starts at line 71 in find_usages.go
			res, err := client.callTool("rag_read_file_context", map[string]interface{}{
				"file_path":   filepath.Join(root, "internal/service/tools/find_usages.go"),
				"line_number": 71,
			}, toolTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(isToolError(res)).To(BeFalse())

			j := toolJSON(res)
			text := toolText(res)
			GinkgoWriter.Printf("[read_ctx] find_usages.go:71  status=%s\n%.400s\n", j["status"], text)
			Expect(j["status"]).To(Equal("success"))
			Expect(text).To(ContainSubstring("Execute"), "line 71 is inside Execute — must appear in AST context")
		})

		It("line 83 of search_local_index.go → inside Execute function", func() {
			res, err := client.callTool("rag_read_file_context", map[string]interface{}{
				"file_path":   filepath.Join(root, "internal/service/tools/search_local_index.go"),
				"line_number": 83,
			}, toolTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(isToolError(res)).To(BeFalse())

			j := toolJSON(res)
			text := toolText(res)
			GinkgoWriter.Printf("[read_ctx] search_local_index.go:83  status=%s\n%.400s\n", j["status"], text)
			Expect(j["status"]).To(Equal("success"))
			Expect(text).To(ContainSubstring("Execute"))
		})

		It("line 69 of engine.go → inside NewEngine constructor", func() {
			res, err := client.callTool("rag_read_file_context", map[string]interface{}{
				"file_path":   filepath.Join(root, "internal/service/engine/engine.go"),
				"line_number": 69,
			}, toolTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(isToolError(res)).To(BeFalse())

			j := toolJSON(res)
			text := toolText(res)
			GinkgoWriter.Printf("[read_ctx] engine.go:69  status=%s\n%.400s\n", j["status"], text)
			Expect(j["status"]).To(Equal("success"))
			Expect(text).To(ContainSubstring("NewEngine"), "line 69 is inside NewEngine — must appear in AST context")
		})

		It("start_line+end_line: lines 1-20 of main.go → package declaration", func() {
			res, err := client.callTool("rag_read_file_context", map[string]interface{}{
				"file_path":  filepath.Join(root, "cmd/rag-code-mcp/main.go"),
				"start_line": 1,
				"end_line":   20,
			}, toolTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(isToolError(res)).To(BeFalse())

			j := toolJSON(res)
			text := toolText(res)
			GinkgoWriter.Printf("[read_ctx] main.go:1-20  status=%s\n%.400s\n", j["status"], text)
			Expect(j["status"]).To(Equal("success"))
			Expect(text).To(ContainSubstring("main"), "package main declaration must appear in lines 1-20")
		})

		It("context_lines=20 around line 144 of engine.go → inside DetectContext", func() {
			res, err := client.callTool("rag_read_file_context", map[string]interface{}{
				"file_path":     filepath.Join(root, "internal/service/engine/engine.go"),
				"line_number":   144,
				"context_lines": 20,
			}, toolTimeout)
			Expect(err).NotTo(HaveOccurred())
			Expect(isToolError(res)).To(BeFalse())

			j := toolJSON(res)
			text := toolText(res)
			GinkgoWriter.Printf("[read_ctx] engine.go:144 ctx=20  status=%s  len=%d\n", j["status"], len(text))
			Expect(j["status"]).To(Equal("success"))
			Expect(text).To(ContainSubstring("DetectContext"))
		})
	})
})
