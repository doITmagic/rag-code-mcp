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
fmt.Println(">>> Connecting to SSE...")
resp, err := http.Get("http://localhost:3000/sse")
if err != nil {
fmt.Printf("Connection error: %v\n", err)
return
}
defer resp.Body.Close()

reader := bufio.NewReader(resp.Body)
var postURL string

for {
line, err := reader.ReadString('\n')
if err != nil {
return
}
if strings.HasPrefix(line, "data: ") {
endpoint := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
postURL = "http://localhost:3000" + endpoint
fmt.Printf(">>> Endpoint found: %s\n", postURL)
break
}
}

go func() {
for {
line, err := reader.ReadString('\n')
if err != nil {
return
}
data := strings.TrimPrefix(line, "data: ")
if strings.TrimSpace(data) != "" {
fmt.Printf("\n[SSE RECEIVED] %s\n", data)
}
}
}()

send := func(payload interface{}) {
body, _ := json.Marshal(payload)
r, err := http.Post(postURL, "application/json", bytes.NewBuffer(body))
if err != nil {
return
}
r.Body.Close()
fmt.Printf(">>> Sent %v, Status: %d\n", payload.(map[string]interface{})["method"], r.StatusCode)
}

send(map[string]interface{}{
"jsonrpc": "2.0",
"id":      1,
"method":  "initialize",
"params": map[string]interface{}{
"protocolVersion": "2024-11-05",
"capabilities":    map[string]interface{}{},
"clientInfo":      map[string]string{"name": "agent-tester", "version": "1.0.0"},
},
})
time.Sleep(1 * time.Second)

send(map[string]interface{}{
"jsonrpc": "2.0",
"method":  "notifications/initialized",
})
time.Sleep(1 * time.Second)

send(map[string]interface{}{
"jsonrpc": "2.0",
"id":      2,
"method":  "tools/call",
"params": map[string]interface{}{
"name": "rag_search_code",
"arguments": map[string]interface{}{
"query":     "automatic update implementation details",
"file_path": "/home/razvan/go/src/github.com/doITmagic/rag-code-mcp/internal/updater/updater.go",
},
},
})

send(map[string]interface{}{
"jsonrpc": "2.0",
"method":  "notifications/tools/list_changed",
"params":  map[string]interface{}{},
})

fmt.Println(">>> Waiting 10s for SSE results...")
time.Sleep(10 * time.Second)
}
