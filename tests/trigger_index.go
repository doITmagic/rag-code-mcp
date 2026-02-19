package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func main() {
	// 1. Open the persistent SSE connection
	fmt.Println("Connecting to SSE...")
	resp, err := http.Get("http://localhost:3000/sse")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	var postURL string
	urlChan := make(chan string)
	done := make(chan bool)

	go func() {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				url := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
				if postURL == "" {
					urlChan <- "http://localhost:3000" + url
				} else {
					fmt.Printf("<<< RESPONSE: %s\n", line)
				}
				if strings.Contains(line, `"id":3`) {
					fmt.Println("\n--- SUCCESS: COMPLEX SEARCH COMPLETED ---")
					done <- true
					return
				}
			} else if line != "" {
				fmt.Printf("<<< %s\n", line)
			}
		}
	}()

	select {
	case postURL = <-urlChan:
		fmt.Println("Session URL:", postURL)
	case <-time.After(5 * time.Second):
		fmt.Println("Timeout waiting for endpoint")
		return
	}

	// 2. Send initialize
	send(postURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]interface{}{},
			"clientInfo":      map[string]interface{}{"name": "test-client", "version": "1.0.0"},
		},
	})

	// 3. Send initialized notification
	send(postURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"method":  "notifications/initialized",
	})

	// 4. Test COMPLEX DISCOVERY question
	fmt.Println("\n>>> Testing COMPLEX DISCOVERY question...")
	// This question requires understanding the interaction between Engine, Resolver, and Indexer
	send(postURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "rag_search_code",
			"arguments": map[string]interface{}{
				"query":     "Explain how the Engine orchestrates background re-indexing when it detects a Git branch change or a HEAD mismatch during a search request.",
				"file_path": "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/internal/service/engine/engine.go",
				"mode":      "discovery",
				"limit":     3,
			},
		},
	})

	time.Sleep(3 * time.Second)

	// 5. Test DEEP SYMBOL interaction in EXACT mode
	fmt.Println("\n>>> Testing DEEP SYMBOL interaction...")
	// Searching for a specific orchestration method that ties things together
	send(postURL, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name": "rag_search_code",
			"arguments": map[string]interface{}{
				"query":     "HybridSearchCode",
				"file_path": "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/internal/service/engine/engine.go",
				"mode":      "exact",
				"limit":     1,
			},
		},
	})

	select {
	case <-done:
		fmt.Println("Complex test finished successfully.")
	case <-time.After(20 * time.Second):
		fmt.Println("Timed out waiting for complex results.")
	}
}

func send(url string, payload interface{}) {
	body, _ := json.Marshal(payload)
	http.Post(url, "application/json", bytes.NewBuffer(body))
}
