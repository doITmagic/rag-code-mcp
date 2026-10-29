package tests

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/internal/transport"
)

// The adapter follows every X-Resolved-Workspace header, so only a confirmed
// rag_index_workspace may send it: a preview or a rejected token for another
// project must not move the window's later pathless calls there.
func TestIndexWorkspaceConfirmsWorkspaceOnlyAfterToken(t *testing.T) {
	root := t.TempDir()
	eng := setupTestEngine(&mockVectorStore{})
	t.Cleanup(eng.Close)
	tool := tools.NewIndexWorkspaceTool(eng)
	call := func(params map[string]interface{}) (map[string]interface{}, string) {
		t.Helper()
		writer := httptest.NewRecorder()
		out, err := tool.Execute(transport.WithResponseWriter(context.Background(), writer), params)
		if err != nil {
			t.Fatal(err)
		}
		var resp map[string]interface{}
		if err := json.Unmarshal([]byte(out), &resp); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		return resp, writer.Header().Get("X-Resolved-Workspace")
	}

	preview, header := call(map[string]interface{}{"workspace_root": root})
	if header != "" {
		t.Fatalf("unconfirmed preview set sticky workspace %q", header)
	}
	token, _ := preview["data"].(map[string]interface{})["confirmation_token"].(string)
	if token == "" {
		t.Fatalf("no confirmation token: %v", preview)
	}
	if resp, header := call(map[string]interface{}{"workspace_root": root, "confirm": true, "confirmation_token": "bogus"}); header != "" {
		t.Fatalf("rejected token (%v) set sticky workspace %q", resp["status"], header)
	}
	resp, header := call(map[string]interface{}{"workspace_root": root, "confirm": true, "confirmation_token": token})
	if resp["status"] != "indexing_started" || header != root {
		t.Fatalf("confirmed index: status %v, sticky workspace %q, want %q", resp["status"], header, root)
	}
}
