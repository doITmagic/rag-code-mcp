---
name: ragcode-sse
description: Teach agents how to call RagCode MCP over the SSE transport without extra MCP config
---

# ⚡ Skill: RagCode SSE Client

This skill shows any agent how to call RagCode MCP directly via HTTP + Server‑Sent Events. No MCP config files, no custom binary integration—just pure HTTP.

---

## 🔌 Endpoints

```
GET  /sse                        # Opens the persistent stream
POST /sse?sessionid=ID           # Send JSON-RPC (exact URL from the endpoint event)
```

### 🔑 Session Handshake
1. Connect to `GET /sse` and keep the connection open.
2. The first SSE event is `event: endpoint`.
3. Its data is the full POST URL, e.g.: `data: /sse?sessionid=H3QCVDP32TP3RBZQ5WLMEJSRF`
4. **Required**: send `initialize` + `notifications/initialized` before any `tools/call`.
5. All responses are delivered back on the open SSE stream.

### 🧾 Bash Script (Tested and working)
```bash
#!/bin/bash
SSE_FILE=$(mktemp)
curl -s -N -H 'Accept: text/event-stream' http://localhost:3000/sse >> "$SSE_FILE" &
SSE_PID=$!
sleep 1

# Extract the POST URL from the endpoint event
ENDPOINT=$(grep 'data:' "$SSE_FILE" | head -1 | sed 's/^data: //' | tr -d '[:space:]')
POST_URL="http://localhost:3000${ENDPOINT}"

# 1. MCP handshake - initialize
curl -s -X POST "$POST_URL" -H 'Content-Type: application/json' -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": {"protocolVersion": "2024-11-05", "capabilities": {},
              "clientInfo": {"name": "my-agent", "version": "1.0"}}
}' && sleep 1

# 2. MCP handshake - initialized notification
curl -s -X POST "$POST_URL" -H 'Content-Type: application/json' -d '{
  "jsonrpc": "2.0", "method": "notifications/initialized"
}' && sleep 1

# 3. Tool call
curl -s -X POST "$POST_URL" -H 'Content-Type: application/json' -d '{
  "jsonrpc": "2.0", "id": 2, "method": "tools/call",
  "params": {
    "name": "rag_search_code",
    "arguments": {
      "query": "automatic update implementation",
      "file_path": "/your/project/file.go"
    }
  }
}'

# 4. Read the response from the SSE stream
sleep 12
kill $SSE_PID 2>/dev/null
cat "$SSE_FILE"
rm "$SSE_FILE"
```

---

## 🧠 Protocol Basics

1. Keep a persistent SSE connection via `GET /sse`.
2. Read the first `event: endpoint` to get the POST URL (`/sse?sessionid=ID`).
3. Send `initialize` + `notifications/initialized` (MCP handshake) before any tool call.
4. Send JSON-RPC payloads via `POST /sse?sessionid=ID`.
5. Responses arrive asynchronously on the SSE stream (match by `id`).

### JSON-RPC Template

```json
{
  "jsonrpc": "2.0",
  "id": "request-001",
  "method": "tools/call",
  "params": {
    "name": "rag_search_code",
    "arguments": {
      "query": "workspace registry",
      "file_path": "/home/user/project/main.go",
      "limit": 5
    }
  }
}
```

Use other MCP methods such as `tools/list`, `ping`, etc.

---

## 🧾 curl Quick Start

Tab A — open the stream and note the POST URL:

```bash
curl -N -H 'Accept: text/event-stream' http://localhost:3000/sse
# First output: data: /sse?sessionid=XXXXXX  <-- this is your POST_URL
```

Tab B — full sequence (replace `XXXXXX` with the real sessionid):

```bash
POST_URL="http://localhost:3000/sse?sessionid=XXXXXX"

# Step 1: initialize
curl -X POST "$POST_URL" -H 'Content-Type: application/json' -d '{
  "jsonrpc": "2.0", "id": 1, "method": "initialize",
  "params": {"protocolVersion": "2024-11-05", "capabilities": {},
              "clientInfo": {"name": "curl-client", "version": "1.0"}}
}' && sleep 1

# Step 2: notifications/initialized
curl -X POST "$POST_URL" -H 'Content-Type: application/json' -d '{
  "jsonrpc": "2.0", "method": "notifications/initialized"
}' && sleep 1

# Step 3: tools/call
curl -X POST "$POST_URL" \
     -H 'Content-Type: application/json' \
     -d '{
  "jsonrpc": "2.0",
  "id": "search-001",
  "method": "tools/call",
  "params": {
    "name": "rag_search_code",
    "arguments": {
      "query": "workspace registry",
      "file_path": "/home/user/project/main.go"
    }
  }
}'
```

Watch the SSE tab for the response.

---

## 🐍 Python Example

```python
import json, threading, requests, sseclient

BASE_URL = "http://localhost:3000"

# 1. Open SSE stream and extract POST URL
resp = requests.get(f"{BASE_URL}/sse", stream=True, headers={"Accept": "text/event-stream"})
client = sseclient.SSEClient(resp)

post_url = None
for event in client.events():
    if event.event == "endpoint":
        post_url = BASE_URL + event.data.strip()
        break

def post(payload):
    requests.post(post_url, json=payload, timeout=10)

# 2. MCP Handshake
post({"jsonrpc": "2.0", "id": 1, "method": "initialize",
      "params": {"protocolVersion": "2024-11-05", "capabilities": {},
                 "clientInfo": {"name": "py-agent", "version": "1.0"}}})
post({"jsonrpc": "2.0", "method": "notifications/initialized"})

# 3. Tool call
post({"jsonrpc": "2.0", "id": 2, "method": "tools/call",
      "params": {"name": "rag_search_code",
                 "arguments": {"query": "workspace registry",
                               "file_path": "/your/project/main.py"}}})

# 4. Read responses
for event in client.events():
    if event.data:
        print(json.loads(event.data))
```

Any SSE client works; keep the stream open and match responses by `id`.

---

## 🧭 Discover Available Tools

1. Call `tools/list` via JSON-RPC to enumerate every MCP tool (rag_search_code, rag_index_workspace, etc.).
2. Inspect each tool's `input_schema` to learn required arguments.

---

## 🛡️ Recommended Settings

- Add header `Accept: text/event-stream` when connecting to `/sse`.
- Maintain a single SSE connection and reuse it for all requests.
- Implement reconnection logic in case the stream drops.

---

## 🧱 Troubleshooting

| Symptom | Fix |
| --- | --- |
| No response | Ensure SSE is open and MCP handshake was done; use unique `id` per request. |
| 4xx on POST | Check JSON and `Content-Type: application/json`; POST goes to `/sse?sessionid=ID`, not `/messages`. |
| `sessionid must be provided` | Extract sessionid using `sed 's/^data: //'` on the `data:` line from the SSE stream. |
| Workspace errors | Always pass an absolute path in `arguments.file_path` for workspace detection. |

---

## ✅ Summary

- **Endpoints**: `GET /sse` (stream), `POST /sse?sessionid=ID` (messages).
- **Required handshake**: `initialize` → `notifications/initialized` → `tools/call`.
- **Protocol**: JSON-RPC 2.0 (MCP spec).
- **Examples**: Provided for curl (bash) and Python.
- **Tool discovery**: call `tools/list` after handshake.

Install this skill to teach any agent how to drive RagCode MCP over SSE immediately.
