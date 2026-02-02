package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/healthcheck"
	"github.com/doITmagic/rag-code-mcp/internal/workspace"
)

// EvaluateRagCodeTool implements MCPTool for session evaluation
type EvaluateRagCodeTool struct {
	wm *workspace.Manager
}

func NewEvaluateRagCodeTool(wm *workspace.Manager) *EvaluateRagCodeTool {
	return &EvaluateRagCodeTool{wm: wm}
}

func (t *EvaluateRagCodeTool) Name() string {
	return "rag_evaluate"
}

func (t *EvaluateRagCodeTool) Description() string {
	return "Request a performance and quality evaluation of RagCode MCP from the AI assistant's perspective. Use this to provide feedback on benefits, pain points, and suggest improvements."
}

func (t *EvaluateRagCodeTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	fmt.Fprintf(os.Stderr, "[INFO] 🛠️ Executing tool '%s'\n", t.Name())

	// Detect workspace for context
	info, err := t.wm.DetectWorkspace(args)
	if err != nil {
		return "", fmt.Errorf("failed to detect workspace: %w", err)
	}

	// Gather minimal technical context
	cfg := t.wm.GetConfig()
	models := []string{cfg.LLM.OllamaModel, cfg.LLM.OllamaEmbed}
	health := healthcheck.CheckAllWithModels(cfg.LLM.OllamaBaseURL, cfg.Storage.VectorDB.URL, models)
	languages := t.wm.GetDetectedLanguages(info)

	var healthStatus []string
	for _, h := range health {
		healthStatus = append(healthStatus, fmt.Sprintf("- %s: %s", h.Service, h.Status))
	}

	// Create the "Call to Action" for the AI
	var builder strings.Builder
	builder.WriteString("# 📝 RagCode MCP - REQUEST FOR AI EVALUATION\n\n")
	builder.WriteString("You have been asked to provide a comprehensive evaluation of your experience using RagCode MCP in this session. Please analyze your interaction and provide a detailed response addressing the following points:\n\n")

	builder.WriteString("### 1. 🚀 BENEFITS & PERFORMANCE\n")
	builder.WriteString("- How did semantic search and specialized tools (like `get_function_details`) help your reasoning process?\n")
	builder.WriteString("- Compare this experience with standard file reading (`read_file`). What did you gain in terms of speed or context?\n\n")

	builder.WriteString("### 2. ⚡ PAIN POINTS & DIFFICULTIES\n")
	builder.WriteString("- What exactly did you find difficult or frustrating? (e.g., mandatory parameters, tool naming, connection issues, or context limits).\n")
	builder.WriteString("- Were there moments where the tool failed to provide the information you expected?\n\n")

	builder.WriteString("### 3. 🛠️ RECOMMENDATIONS FOR IMPROVEMENT\n")
	builder.WriteString("- What specific modifications would you propose to make this tool more effective for AI agents like yourself?\n")
	builder.WriteString("- Are there any new tools or features that would have made your task easier?\n\n")

	builder.WriteString("### 4. 📢 RECOMMENDATION TO OTHER AGENTS\n")
	builder.WriteString("- Would you recommend RagCode MCP to other AI agents? Why or why not?\n\n")

	builder.WriteString("--- \n")
	builder.WriteString("#### 🛠️ Technical Context (for your reference):\n")
	builder.WriteString(fmt.Sprintf("- **Workspace**: %s\n", info.Root))
	builder.WriteString(fmt.Sprintf("- **Languages**: %s\n", strings.Join(languages, ", ")))
	builder.WriteString(fmt.Sprintf("- **System Status**:\n%s\n", strings.Join(healthStatus, "\n")))
	builder.WriteString(fmt.Sprintf("- **Models**: Chat=%s, Embed=%s\n\n", cfg.LLM.OllamaModel, cfg.LLM.OllamaEmbed))

	builder.WriteString("**INSTRUCTION**: Do not just repeat the technical data. Provide a qualitative, 'human-like' analytical feedback based on your actual work in this session.")

	return builder.String(), nil
}
