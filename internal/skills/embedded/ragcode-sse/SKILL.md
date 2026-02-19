---
name: ragcode-sse
description: Teach agents how to call RagCode MCP over the SSE transport without extra MCP config
---

# ⚡ Skill: RagCode SSE Client

This skill shows any agent how to call RagCode MCP directly via HTTP + Server‑Sent Events. No MCP config files, no custom binary integration—just pure HTTP.

---

## 🔌 Endpoints

```
GET  /sse        # open the event stream (keep-alive)
POST /messages   # send JSON-RPC requests
```

Set the port with the `-http-port` flag (default `3000`). Example URLs:

```
SSE stream  : http://localhost:3000/sse
Send message: http://localhost:3000/messages
```

---

## 🧠 Protocol Basics

1. Keep a persistent SSE connection to `/sse` (subscribe to events).
2. Send JSON-RPC payloads to `/messages`.
3. Responses arrive asynchronously on the SSE stream (matching `id`).

### JSON-RPC Template

```json
{
  "jsonrpc": "2.0",
  "id": "request-001",
  "method": "call_tool",
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

Use other MCP methods such as `list_tools`, `ping`, etc.

---

## 🧾 curl Quick Start

Open a stream (terminal tab A):

```bash
curl -N http://localhost:3000/sse
```

Send a message (terminal tab B):

```bash
curl -X POST http://localhost:3000/messages \
     -H 'Content-Type: application/json' \
     -d '{
  "jsonrpc": "2.0",
  "id": "search-001",
  "method": "call_tool",
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
import json, requests, sseclient

SSE_URL = "http://localhost:3000/sse"
MSG_URL = "http://localhost:3000/messages"

payload = {
    "jsonrpc": "2.0",
    "id": "list-tools",
    "method": "list_tools",
    "params": {}
}

requests.post(MSG_URL, json=payload, timeout=10)
client = sseclient.SSEClient(SSE_URL)
for event in client.events():
    print(event.data)
```

Any SSE client works; just keep reading events and match IDs.

---

## 🧭 Discover Available Tools

1. Call `list_tools` via JSON-RPC to enumerate every MCP tool (rag_search_code, rag_index_workspace, etc.).
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
| No response | Ensure SSE connection is open; use unique `id`s per request. |
| 4xx on `/messages` | Check JSON validity and `Content-Type: application/json`. |
| Workspace errors | Always pass `arguments.file_path` for context detection. |

---

## ✅ Summary

- **Endpoints**: `GET /sse`, `POST /messages`.
- **Protocol**: JSON-RPC 2.0 (MCP spec).
- **Examples**: Provided for curl and Python.
- **Tool discovery**: `list_tools` response.

Install this skill to teach any agent how to drive RagCode MCP over SSE immediately.
