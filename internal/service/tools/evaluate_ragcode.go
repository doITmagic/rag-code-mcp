package tools

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/healthcheck"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
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

func (t *EvaluateRagCodeTool) Name() string {
	return "rag_evaluate"
}

func (t *EvaluateRagCodeTool) Description() string {
	return "Request a performance and quality evaluation of RagCode MCP from the AI assistant's perspective. " +
		"Use this to provide feedback on benefits, pain points, and suggest improvements. " +
		"RECOMMENDED: Providing the 'file_path' helps identifies the current project context for a more accurate evaluation, but it is not strictly mandatory."
}

func (t *EvaluateRagCodeTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	log.Printf("[INFO] 🛠️ Executing tool '%s'\n", t.Name())

	// Detect workspace for context, but continue gracefully if it fails.
	var workspaceRoot string
	if filePath, _ := args["file_path"].(string); filePath != "" && t.engine != nil {
		if wctx, err := t.engine.DetectContext(ctx, filePath); err != nil {
			log.Printf("[WARN] Workspace detection failed in '%s': %v (continuing without workspace context)\n", t.Name(), err)
		} else {
			workspaceRoot = wctx.Root
		}
	}

	// Gather minimal technical context.
	var healthStatus []string
	if t.cfg != nil {
		models := []string{t.cfg.LLM.OllamaModel, t.cfg.LLM.OllamaEmbed}
		health := healthcheck.CheckAllWithModels(t.cfg.LLM.OllamaBaseURL, t.cfg.Storage.VectorDB.URL, models)
		for _, h := range health {
			healthStatus = append(healthStatus, fmt.Sprintf("- %s: %s", h.Service, h.Status))
		}
	}

	var b strings.Builder
	b.WriteString("# 📝 RagCode MCP - REQUEST FOR AI EVALUATION\n\n")
	b.WriteString("You have been asked to provide a comprehensive evaluation of your experience using RagCode MCP in this session. Please analyze your interaction and provide a detailed response addressing the following points:\n\n")

	b.WriteString("### 1. 🚀 BENEFITS & PERFORMANCE\n")
	b.WriteString("- How did semantic search and specialized tools (like `rag_get_function_details`) help your reasoning process?\n")
	b.WriteString("- Compare this experience with standard file reading (`read_file`). What did you gain in terms of speed or context?\n\n")

	b.WriteString("### 2. ⚡ PAIN POINTS & DIFFICULTIES\n")
	b.WriteString("- What exactly did you find difficult or frustrating? (e.g., mandatory parameters, tool naming, connection issues, or context limits).\n")
	b.WriteString("- Were there moments where the tool failed to provide the information you expected?\n\n")

	b.WriteString("### 3. 🛠️ RECOMMENDATIONS FOR IMPROVEMENT\n")
	b.WriteString("- What specific modifications would you propose to make this tool more effective for AI agents like yourself?\n")
	b.WriteString("- Are there any new tools or features that would have made your task easier?\n\n")

	b.WriteString("### 4. 📢 RECOMMENDATION TO OTHER AGENTS\n")
	b.WriteString("- Would you recommend RagCode MCP to other AI agents? Why or why not?\n\n")

	b.WriteString("---\n")
	b.WriteString("#### 🛠️ Technical Context (for your reference):\n")
	if workspaceRoot != "" {
		b.WriteString(fmt.Sprintf("- **Workspace**: %s\n", workspaceRoot))
	} else {
		b.WriteString("- **Workspace**: Not detected (fallback mode). Provide a 'file_path' to enable workspace-specific context.\n")
	}
	if len(healthStatus) > 0 {
		b.WriteString(fmt.Sprintf("- **System Status**:\n%s\n", strings.Join(healthStatus, "\n")))
	}
	if t.cfg != nil {
		b.WriteString(fmt.Sprintf("- **Models**: Chat=%s, Embed=%s\n\n", t.cfg.LLM.OllamaModel, t.cfg.LLM.OllamaEmbed))
	}
	b.WriteString("**INSTRUCTION**: Do not just repeat the technical data. Provide a qualitative, 'human-like' analytical feedback based on your actual work in this session.")

	return b.String(), nil
}
