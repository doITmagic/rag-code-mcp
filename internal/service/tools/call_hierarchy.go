package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type CallHierarchyTool struct {
	engine *engine.Engine
}

func NewCallHierarchyTool(eng *engine.Engine) *CallHierarchyTool {
	return &CallHierarchyTool{
		engine: eng,
	}
}

func (t *CallHierarchyTool) Name() string { return "rag_call_hierarchy" }
func (t *CallHierarchyTool) Description() string {
	return "Explore recursive call relationships (caller/callee) for a symbol to understand the execution flow. Supports discovering who calls a function ('incoming') or what a function calls ('outgoing'), up to a specified depth. MANDATORY: provide 'file_path' for context resolution."
}

type CallHierarchyInput struct {
	SymbolName string `json:"symbol_name"`
	Direction  string `json:"direction"` // "incoming" or "outgoing"
	Depth      int    `json:"depth,omitempty"`
	FilePath   string `json:"file_path"`
}

type CallNode struct {
	Name      string      `json:"name"`
	Type      string      `json:"type"`
	FilePath  string      `json:"file_path"`
	Package   string      `json:"package"`
	Children  []*CallNode `json:"children,omitempty"`
	Recursive bool        `json:"recursive,omitempty"`
}

func (t *CallHierarchyTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CallHierarchyInput) (*mcp.CallToolResult, any, error) {
		if input.Depth <= 0 {
			input.Depth = 2
		}
		if input.Depth > 5 {
			input.Depth = 5
		}
		if input.Direction == "" {
			input.Direction = "incoming"
		}

		args := map[string]interface{}{
			"symbol_name": input.SymbolName,
			"direction":   input.Direction,
			"depth":       input.Depth,
			"file_path":   input.FilePath,
		}

		result, err := t.Execute(ctx, args)
		if err != nil {
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})
}

func (t *CallHierarchyTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	symbolName, ok := args["symbol_name"].(string)
	if !ok || symbolName == "" {
		resp := ToolResponse{Status: "error", Error: "symbol_name parameter is required"}
		return resp.JSON()
	}

	direction, _ := args["direction"].(string)
	depthVal, _ := args["depth"]
	var depth int
	switch v := depthVal.(type) {
	case float64:
		depth = int(v)
	case int:
		depth = v
	}
	if depth <= 0 {
		depth = 2
	}

	filePath, _ := args["file_path"].(string)

	wctx, err := t.engine.DetectContext(ctx, filePath)
	if err != nil {
		resp := ToolResponse{Status: "error", Error: fmt.Sprintf("failed to detect workspace: %v", err)}
		return resp.JSON()
	}

	searchSvc := t.engine.GetSearchService()
	if searchSvc == nil {
		resp := ToolResponse{Status: "error", Error: "search service unavailable"}
		return resp.JSON()
	}

	langs := parser.SupportedLanguages()
	visited := make(map[string]bool)

	rootNode := &CallNode{Name: symbolName}

	// Try to find root symbol info
	rootRes := t.findSymbolInfo(ctx, searchSvc, wctx.ID, langs, symbolName)
	if rootRes != nil {
		rootNode.Type, _ = rootRes.Point.Payload["type"].(string)
		rootNode.FilePath, _ = rootRes.Point.Payload["file_path"].(string)
		rootNode.Package, _ = rootRes.Point.Payload["package"].(string)
	}

	if direction == "incoming" {
		t.resolveIncoming(ctx, searchSvc, wctx.ID, langs, rootNode, depth, visited)
	} else {
		t.resolveOutgoing(ctx, searchSvc, wctx.ID, langs, rootNode, depth, visited)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Call Hierarchy: %s (%s)\n\n", symbolName, direction))
	t.formatTree(&sb, rootNode, 0)

	resp := ToolResponse{
		Status:  "success",
		Message: sb.String(),
		Data:    rootNode,
		Context: ContextMetadata{WorkspaceRoot: wctx.Root, DetectionSource: wctx.DetectionSource},
	}
	return resp.JSON()
}

func (t *CallHierarchyTool) findSymbolInfo(ctx context.Context, srv *search.Service, wsID string, langs []string, name string) *storage.SearchResult {
	for _, lang := range langs {
		col := fmt.Sprintf("ragcode-%s-%s", wsID, lang)
		exists, _ := srv.CollectionExists(ctx, col)
		if !exists {
			continue
		}

		res, err := srv.ExactSearch(ctx, col, map[string]interface{}{"name": name}, 1)
		if err == nil && len(res) > 0 {
			return &res[0]
		}
	}
	return nil
}

func (t *CallHierarchyTool) resolveIncoming(ctx context.Context, srv *search.Service, wsID string, langs []string, node *CallNode, depth int, visited map[string]bool) {
	if depth <= 0 || visited[node.Name] {
		if visited[node.Name] {
			node.Recursive = true
		}
		return
	}
	visited[node.Name] = true

	for _, lang := range langs {
		col := fmt.Sprintf("ragcode-%s-%s", wsID, lang)
		exists, _ := srv.CollectionExists(ctx, col)
		if !exists {
			continue
		}

		// Find who has a Relation target_name == node.Name AND type == "calls"
		res, err := srv.ExactSearch(ctx, col, map[string]interface{}{
			"relations[].target_name": node.Name,
			"relations[].type":        "calls",
		}, 20)
		if err == nil {
			for _, r := range res {
				// Verify the relation matches in-memory (to handle lack of robust nested filtering in ExactSearch)
				hasCall := false
				if rels, ok := r.Point.Payload["relations"].([]interface{}); ok {
					for _, relRaw := range rels {
						if rel, ok := relRaw.(map[string]interface{}); ok {
							if rel["target_name"] == node.Name && rel["type"] == "calls" {
								hasCall = true
								break
							}
						}
					}
				}
				if !hasCall {
					continue
				}

				name, _ := r.Point.Payload["name"].(string)
				if name == "" || name == node.Name {
					continue
				}

				child := &CallNode{
					Name:     name,
					Type:     fmt.Sprintf("%v", r.Point.Payload["type"]),
					FilePath: fmt.Sprintf("%v", r.Point.Payload["file_path"]),
					Package:  fmt.Sprintf("%v", r.Point.Payload["package"]),
				}
				node.Children = append(node.Children, child)
				t.resolveIncoming(ctx, srv, wsID, langs, child, depth-1, visited)
			}
		}
	}
}

func (t *CallHierarchyTool) resolveOutgoing(ctx context.Context, srv *search.Service, wsID string, langs []string, node *CallNode, depth int, visited map[string]bool) {
	if depth <= 0 || visited[node.Name] {
		if visited[node.Name] {
			node.Recursive = true
		}
		return
	}
	visited[node.Name] = true

	// Find the node itself to get its Relations
	res := t.findSymbolInfo(ctx, srv, wsID, langs, node.Name)
	if res == nil {
		return
	}

	if relationsRaw, ok := res.Point.Payload["relations"].([]interface{}); ok {
		for _, relRaw := range relationsRaw {
			relMap, ok := relRaw.(map[string]interface{})
			if !ok {
				continue
			}

			target, _ := relMap["target_name"].(string)
			relType, _ := relMap["type"].(string)
			if target == "" || target == node.Name || relType != "calls" {
				continue
			}

			// Skip qualified names (e.g. "t.engine.DetectContext", "json.MarshalIndent")
			// The parser always emits both qualified and short form; the short form
			// is either already in the list (local) or will be handled as external.
			if strings.Contains(target, ".") {
				continue
			}

			child := &CallNode{Name: target}

			// Try to fill child info from local index
			childInfo := t.findSymbolInfo(ctx, srv, wsID, langs, target)
			if childInfo != nil {
				child.Type, _ = childInfo.Point.Payload["type"].(string)
				child.FilePath, _ = childInfo.Point.Payload["file_path"].(string)
				child.Package, _ = childInfo.Point.Payload["package"].(string)
				node.Children = append(node.Children, child)
				t.resolveOutgoing(ctx, srv, wsID, langs, child, depth-1, visited)
			} else {
				// External / stdlib symbol — show once, no recursion
				child.Type = "external"
				node.Children = append(node.Children, child)
			}
		}
	}
}

func (t *CallHierarchyTool) formatTree(sb *strings.Builder, node *CallNode, indent int) {
	prefix := strings.Repeat("  ", indent)
	marker := "└─"
	if indent == 0 {
		marker = ""
	}

	var line string
	if node.Type == "external" {
		line = fmt.Sprintf("%s%s **%s** _(external)_", prefix, marker, node.Name)
	} else {
		line = fmt.Sprintf("%s%s **%s** (%s) `%s:%s`", prefix, marker, node.Name, node.Type, node.Package, node.FilePath)
	}
	if node.Recursive {
		line += " 🔄 [circular]"
	}
	sb.WriteString(line + "\n")

	for _, child := range node.Children {
		t.formatTree(sb, child, indent+1)
	}
}
