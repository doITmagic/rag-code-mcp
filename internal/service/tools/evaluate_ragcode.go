package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/healthcheck"
	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// EvaluateRagCodeTool implements MCPTool for session evaluation.
type EvaluateRagCodeTool struct {
	engine *engine.Engine
	cfg    *config.Config
}

// NewEvaluateRagCodeTool creates a new evaluation tool.
func NewEvaluateRagCodeTool(eng *engine.Engine, cfg *config.Config) *EvaluateRagCodeTool {
	return &EvaluateRagCodeTool{engine: eng, cfg: cfg}
}

func (t *EvaluateRagCodeTool) Name() string { return "rag_evaluate" }
func (t *EvaluateRagCodeTool) Description() string {
	return "Request a performance and quality evaluation of RagCode MCP from the AI assistant's perspective. " +
		"Use this to provide feedback on benefits, pain points, and suggest improvements."
}

type EvaluateInput struct {
	FilePath string `json:"file_path,omitempty"`
}

func (t *EvaluateRagCodeTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input EvaluateInput) (*mcp.CallToolResult, any, error) {
		args := map[string]interface{}{"file_path": input.FilePath}
		start := time.Now()
		result, err := t.Execute(ctx, args)
		if err != nil {
			logger.Instance.Error("rag_evaluate failed (%v): %v", time.Since(start), err)
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}
		logger.Instance.Info("rag_evaluate completed in %v", time.Since(start))
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})
}

func (t *EvaluateRagCodeTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	filePath, _ := args["file_path"].(string)

	wctx, err := t.engine.DetectContext(ctx, filePath)
	if err != nil {
		// Even if detection fails completely, we allow evaluation to proceed in fallback mode
		// by using an empty workspace context, but we log the warning.
		logger.Instance.Warn("Workspace detection failed in '%s': %v (continuing with limited context)", t.Name(), err)
	}

	var workspaceRoot string
	var source string = "none"
	if wctx != nil {
		workspaceRoot = wctx.Root
		source = wctx.DetectionSource
	}

	var healthStatus []string
	if t.cfg != nil {
		models := []string{t.cfg.LLM.OllamaModel, t.cfg.LLM.OllamaEmbed}
		health := healthcheck.CheckAllWithModels(t.cfg.LLM.OllamaBaseURL, t.cfg.Storage.VectorDB.URL, models)
		for _, h := range health {
			statusSymbol := "✅"
			if h.Status != "OK" {
				statusSymbol = "❌"
			}
			healthStatus = append(healthStatus, fmt.Sprintf("%s %s: %s", statusSymbol, h.Service, h.Status))
		}
	}

	var b strings.Builder
	b.WriteString("# 📝 RagCode MCP - REQUEST FOR AI EVALUATION\n\n")
	b.WriteString("You have been asked to provide a comprehensive evaluation of your experience using RagCode MCP in this session.\n\n")

	b.WriteString("### 1. 🚀 BENEFITS & PERFORMANCE\n")
	b.WriteString("- How did semantic search help your reasoning process?\n")
	b.WriteString("- What did you gain in terms of speed or context vs standard `read_file`?\n\n")

	b.WriteString("### 2. ⚡ PAIN POINTS & DIFFICULTIES\n")
	b.WriteString("- What exactly did you find difficult or frustrating?\n")
	b.WriteString("- Were there moments where the tool failed to provide the information you expected?\n\n")

	b.WriteString("### 3. 🛠️ RECOMMENDATIONS FOR IMPROVEMENT\n")
	b.WriteString("- What specific modifications would you propose to make this tool more effective for AI agents?\n\n")

	b.WriteString("---\n")
	b.WriteString("#### 🛠️ Technical Context:\n")
	if workspaceRoot != "" {
		b.WriteString(fmt.Sprintf("- **Workspace**: %s (%s)\n", workspaceRoot, source))
	} else {
		b.WriteString("- **Workspace**: Not detected.\n")
	}
	if len(healthStatus) > 0 {
		b.WriteString(fmt.Sprintf("- **System Status**:\n%s\n", strings.Join(healthStatus, "\n")))
	}
	if t.cfg != nil {
		b.WriteString(fmt.Sprintf("- **Models**: Chat=%s, Embed=%s\n", t.cfg.LLM.OllamaModel, t.cfg.LLM.OllamaEmbed))
	}

	response := ToolResponse{
		Status: "success",
		Context: ContextMetadata{
			WorkspaceRoot:   workspaceRoot,
			DetectionSource: source,
		},
		Data: b.String(),
	}

	return response.JSON()
}
