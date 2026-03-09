package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
	"github.com/doITmagic/rag-code-mcp/pkg/telemetry"
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
	return "Explore recursive call relationships (caller/callee) for a symbol to understand the execution flow. Supports discovering who calls a function ('incoming') or what a function calls ('outgoing'), up to a specified depth. Provide 'file_path' for better context resolution; if omitted, falls back to the last active workspace."
}

type CallHierarchyInput struct {
	SymbolName string `json:"symbol_name"`
	Direction  string `json:"direction"` // "incoming" or "outgoing"
	Depth      int    `json:"depth,omitempty"`
	FilePath   string `json:"file_path,omitempty"`
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
	depthVal := args["depth"]
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

	idx := t.engine.GetIndexStatus(wctx.Root)

	visited := make(map[string]bool)

	rootNode := &CallNode{Name: symbolName}

	// Try to find root symbol info
	rootRes := t.findSymbolInfo(ctx, wctx.ID, symbolName)
	if rootRes != nil {
		rootNode.Type, _ = rootRes.Point.Payload["type"].(string)
		rootNode.FilePath, _ = rootRes.Point.Payload["file_path"].(string)
		rootNode.Package, _ = rootRes.Point.Payload["package"].(string)
	} else {
		// If nothing is indexed yet, ExactSearchPolyglot will return ErrNoCollectionsFound.
		// Signal indexing status instead of returning an empty hierarchy.
		_, sErr := t.engine.ExactSearchPolyglot(ctx, wctx.ID, map[string]interface{}{"name": symbolName}, 1)
		var noCollections *engine.ErrNoCollectionsFound
		if errors.As(sErr, &noCollections) {
			resp := ToolResponse{
				Status:  "indexing_required",
				Message: fmt.Sprintf("⏳ Workspace '%s' is not indexed yet. Indexing is required for complete call hierarchy results.", wctx.Root),
				Context: ContextFromWorkspaceWithStatus(wctx, t.engine),
			}
			if idx != nil {
				resp.Status = "indexing_in_progress"
				resp.Data = map[string]any{"indexing": idx}
			}
			return resp.JSON()
		}
	}

	if direction == "incoming" {
		t.resolveIncoming(ctx, wctx.ID, rootNode, depth, visited)
	} else {
		t.resolveOutgoing(ctx, wctx.ID, rootNode, depth, visited)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Call Hierarchy: %s (%s)\n\n", symbolName, direction))
	t.formatTree(&sb, rootNode, 0)

	// Collect unique file paths from the hierarchy for telemetry
	seenFiles := make(map[string]bool)
	baselineBytes := int64(0)
	collectFiles(rootNode, seenFiles)
	for fp := range seenFiles {
		if info, err := os.Stat(fp); err == nil {
			baselineBytes += info.Size()
		}
	}
	// actualBytes = just the text of the hierarchy message (what we actually send)
	actualBytes := int64(sb.Len())

	resp := ToolResponse{
		Status:  "success",
		Message: sb.String(),
		Data:    rootNode,
		Context: ContextMetadata{
			WorkspaceRoot:    wctx.Root,
			DetectionSource:  wctx.DetectionSource,
			Telemetry:        telemetry.CalculateSavings(baselineBytes, actualBytes),
			IndexingStatus:   idx,
		},
	}
	return resp.JSON()
}

func (t *CallHierarchyTool) findSymbolInfo(ctx context.Context, wsID, name string) *storage.SearchResult {
	res, err := t.engine.SearchByName(ctx, wsID, name, 1)
	if err == nil && len(res) > 0 {
		return &res[0]
	}
	return nil
}

// collectFiles traverses the CallNode tree and adds unique non-empty FilePath values to seen.
func collectFiles(node *CallNode, seen map[string]bool) {
	if node == nil {
		return
	}
	if node.FilePath != "" {
		seen[node.FilePath] = true
	}
	for _, child := range node.Children {
		collectFiles(child, seen)
	}
}

func (t *CallHierarchyTool) resolveIncoming(ctx context.Context, wsID string, node *CallNode, depth int, visited map[string]bool) {
	if depth <= 0 || visited[node.Name] {
		if visited[node.Name] {
			node.Recursive = true
		}
		return
	}
	visited[node.Name] = true

	// ExactSearchPolyglot fans out to all language collections in parallel
	res, err := t.engine.ExactSearchPolyglot(ctx, wsID, map[string]interface{}{
		"relations[].target_name": node.Name,
		"relations[].type":        "calls",
	}, 20)
	if err != nil {
		return
	}

	for _, r := range res {
		// Verify in-memory (nested filter may not be fully precise in Qdrant Scroll)
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
		t.resolveIncoming(ctx, wsID, child, depth-1, visited)
	}
}

func (t *CallHierarchyTool) resolveOutgoing(ctx context.Context, wsID string, node *CallNode, depth int, visited map[string]bool) {
	if depth <= 0 || visited[node.Name] {
		if visited[node.Name] {
			node.Recursive = true
		}
		return
	}
	visited[node.Name] = true

	res := t.findSymbolInfo(ctx, wsID, node.Name)
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

			// Skip qualified names — parser emits both qualified and short form
			if strings.Contains(target, ".") {
				continue
			}

			child := &CallNode{Name: target}
			childInfo := t.findSymbolInfo(ctx, wsID, target)
			if childInfo != nil {
				child.Type, _ = childInfo.Point.Payload["type"].(string)
				child.FilePath, _ = childInfo.Point.Payload["file_path"].(string)
				child.Package, _ = childInfo.Point.Payload["package"].(string)
				node.Children = append(node.Children, child)
				t.resolveOutgoing(ctx, wsID, child, depth-1, visited)
			} else {
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
