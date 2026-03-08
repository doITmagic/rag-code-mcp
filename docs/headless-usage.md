# Headless Usage (Streamable HTTP)

RagCode MCP is primarily an MCP Server designed to be invoked via `stdio` by conforming clients (like Cursor, Windsurf, Claude Desktop). However, the architecture is **Protocol Agnostic**, allowing it to be used "headlessly" without IDE integration.

This is especially useful for purely autonomous AI agents (like Antigravity scripts, custom Python bots, or CI/CD integrations) that need to issue JSON-RPC queries to the engine over TCP instead of standard I/O pipes.

## Streamable HTTP Mode

RagCode features a subpackage `internal/daemon/` which spawns a stateless Streamable HTTP handler that conforms to the MCP standard.

To start the daemon:

```bash
rag-code-mcp --http-port 8080
```

When the server starts successfully, it will print:
`[INFO] HTTP MCP server listening on :8080`

### Example Integration (Using cURL or HTTP Agents)

Once the daemon is active, your AI Script can send standard MCP JSON-RPC requests directly to the `/mcp` endpoint:

**Send an MCP Command (POST)**
```bash
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "rag_search",
      "arguments": {
        "query": "authentication code",
        "file_path": "/var/www/my_project/index.php"
      }
    }
  }'
```

The response will be returned as a standard JSON-RPC 2.0 response in the HTTP body.

## "The `ragcode-http` Skill" Integration

For agents that do not inherently support MCP natively in their prompt cycle, developers can push the officially provided `ragcode-http` agent skill. 

This skill injects bash commands that wrap `curl` payloads, allowing the agent to perform raw POST queries seamlessly, extracting context from large workspaces directly into standard markdown without installing a Desktop IDE.

1. Agent discovers skill via `rag_list_skills`.
2. Agent runs `rag_install_skill {"skill_id": "ragcode-http", "target": "agent"}`.
3. The agent reads `SKILL.md` to learn how to frame HTTP POST queries toward port `8080`.

**Note on Workspace Context:** Always ensure the script explicitly sends `"file_path": "/absolute/path/to/project/file.ext"` inside the arguments payload. Without an IDE, RagCode completely relies on `file_path` to map the workspace identity correctly.
