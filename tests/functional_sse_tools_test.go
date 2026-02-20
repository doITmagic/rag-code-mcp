package tests

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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Simplified SSE client for functional testing
type sseClient struct {
	resp      *http.Response
	reader    *bufio.Reader
	outCh     chan []byte
	sessionID string
}

func newSSEClient(url string) (*sseClient, error) {
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "text/event-stream")

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("bad status: %d", resp.StatusCode)
	}

	client := &sseClient{
		resp:   resp,
		reader: bufio.NewReader(resp.Body),
		outCh:  make(chan []byte, 100),
	}

	// Read first event to get session ID
	event, err := client.readEvent()
	if err != nil {
		return nil, err
	}

	text := string(event)
	idx := strings.Index(text, "sessionid=")
	if idx != -1 {
		client.sessionID = strings.TrimSpace(text[idx+len("sessionid="):])
	}

	go client.readLoop()
	return client, nil
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
		c.outCh <- event
	}
}

func (c *sseClient) callTool(baseURL, name string, args map[string]interface{}) (map[string]interface{}, error) {
	id := fmt.Sprintf("call-%d", time.Now().UnixNano())
	payload := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      name,
			"arguments": args,
		},
	}

	body, _ := json.Marshal(payload)
	url := fmt.Sprintf("%s/messages?sessionid=%s", baseURL, c.sessionID)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("tool call failed: %d - %s", resp.StatusCode, string(b))
	}

	// Wait for response in SSE stream
	timeout := time.After(30 * time.Second)
	for {
		select {
		case <-timeout:
			return nil, fmt.Errorf("timeout waiting for %s", id)
		case data := <-c.outCh:
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

var _ = Describe("Functional SSE Tools Integration", Ordered, func() {
	var (
		client  *sseClient
		baseURL = "http://localhost:3000"
		root    = "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp"
	)

	BeforeAll(func() {
		var err error
		client, err = newSSEClient(baseURL + "/sse")
		Expect(err).NotTo(HaveOccurred())

		// Initialize
		id := "init"
		initPayload := map[string]interface{}{
			"jsonrpc": "2.0",
			"id":      id,
			"method":  "initialize",
			"params": map[string]interface{}{
				"protocolVersion": "2024-11-05",
				"capabilities":    map[string]interface{}{},
				"clientInfo":      map[string]interface{}{"name": "functional-test", "version": "1.0.0"},
			},
		}
		body, _ := json.Marshal(initPayload)
		http.Post(baseURL+"/messages?sessionid="+client.sessionID, "application/json", bytes.NewReader(body))

		Eventually(client.outCh, "5s").Should(Receive())
	})

	AfterAll(func() {
		if client != nil {
			client.resp.Body.Close()
		}
	})

	It("should search for code using rag_search_code", func() {
		res, err := client.callTool(baseURL, "rag_search_code", map[string]interface{}{
			"query":     "ListPackageExports implementation",
			"file_path": root + "/internal/service/tools/list_package_exports.go",
		})
		Expect(err).NotTo(HaveOccurred())

		result := res["result"].(map[string]interface{})
		content := result["content"].([]interface{})
		text := content[0].(map[string]interface{})["text"].(string)

		fmt.Printf("\n--- SEARCH RESULTS ---\n%s\n", text)
		Expect(text).To(ContainSubstring("success"))
	})

	It("should find usages using rag_find_usages", func() {
		res, err := client.callTool(baseURL, "rag_find_usages", map[string]interface{}{
			"symbol_name": "NewEngine",
			"file_path":   root + "/cmd/rag-code-mcp/main.go",
		})
		Expect(err).NotTo(HaveOccurred())

		result := res["result"].(map[string]interface{})
		content := result["content"].([]interface{})
		text := content[0].(map[string]interface{})["text"].(string)

		fmt.Printf("\n--- FIND USAGES (NewEngine) ---\n%s\n", text)
		Expect(text).To(ContainSubstring("success"))
	})

	It("should explore call hierarchy using rag_call_hierarchy", func() {
		res, err := client.callTool(baseURL, "rag_call_hierarchy", map[string]interface{}{
			"symbol_name": "Execute",
			"direction":   "outgoing",
			"file_path":   root + "/internal/service/tools/find_usages.go",
		})
		Expect(err).NotTo(HaveOccurred())

		result := res["result"].(map[string]interface{})
		content := result["content"].([]interface{})
		text := content[0].(map[string]interface{})["text"].(string)

		fmt.Printf("\n--- CALL HIERARCHY (Execute OUTGOING) ---\n%s\n", text)
		Expect(text).To(ContainSubstring("success"))
	})

	It("should list package exports using rag_list_package_exports", func() {
		res, err := client.callTool(baseURL, "rag_list_package_exports", map[string]interface{}{
			"package":   "tools",
			"file_path": root + "/internal/service/tools/response.go",
		})
		Expect(err).NotTo(HaveOccurred())

		result := res["result"].(map[string]interface{})
		content := result["content"].([]interface{})
		text := content[0].(map[string]interface{})["text"].(string)

		fmt.Printf("\n--- PACKAGE EXPORTS (tools) ---\n%s\n", text)
		Expect(text).To(ContainSubstring("success"))
	})

	It("should read file with AST context using rag_read_file_context", func() {
		res, err := client.callTool(baseURL, "rag_read_file_context", map[string]interface{}{
			"file_path":   root + "/internal/service/tools/find_usages.go",
			"line_number": 50,
		})
		Expect(err).NotTo(HaveOccurred())

		result := res["result"].(map[string]interface{})
		content := result["content"].([]interface{})
		text := content[0].(map[string]interface{})["text"].(string)

		fmt.Printf("\n--- READ FILE CONTEXT (find_usages.go:50) ---\n%s\n", text)
		Expect(text).To(ContainSubstring("success"))
	})
})
