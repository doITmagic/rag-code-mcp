package tools

import (
"context"
"encoding/json"
"fmt"

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
return "Request a performance and quality evaluation of RagCode MCP from the AI assistant's perspective. " +
"This tool provides technical stats and prompts the AI to provide feedback on benefits and pain points."
}

func (t *EvaluateRagCodeTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
// Detect workspace
info, err := t.wm.DetectWorkspace(args)
if err != nil {
return "", fmt.Errorf("failed to detect workspace: %w", err)
}

result := make(map[string]interface{})
result["workspace_root"] = info.Root
result["status"] = "Active"

// 1. Check Dependencies
cfg := t.wm.GetConfig()
models := []string{cfg.LLM.OllamaModel, cfg.LLM.OllamaEmbed}
health := healthcheck.CheckAllWithModels(cfg.LLM.OllamaBaseURL, cfg.Storage.VectorDB.URL, models)
result["dependency_health"] = health

// Check if any dependency is failing
for _, h := range health {
if h.Status != "ok" {
result["status"] = "Degraded"
break
}
}

// 2. Get Indexing Info
languages := t.wm.GetDetectedLanguages(info)
result["detected_languages"] = languages

collectionStats := make(map[string]interface{})
for _, lang := range languages {
collectionName := info.CollectionNameForLanguage(lang)
collectionStats[lang] = map[string]string{
"collection": collectionName,
}
}
result["collections"] = collectionStats

// 3. The Prompt for the AI
result["evaluation_prompt"] = "PROMPT FOR AI ASSISTANT: Based on the technical status above and your experience in this session, provide your honest evaluation of RagCode MCP. Do not use pre-calculated templates. Address the following: 1. BENEFITS: How did semantic search and specialized tools (like get_function_details) help your reasoning? 2. PAIN POINTS: What exactly did you find difficult? (e.g., limits, mandatory parameters, error messages). 3. RECOMMENDATION: Would you recommend this tool to other AI agents? Why? 4. COMPARISON: How does it compare to standard file reading (read_file)?"

out, _ := json.MarshalIndent(result, "", "  ")
return string(out), nil
}
