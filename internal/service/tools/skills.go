package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/service/engine"
	"github.com/doITmagic/rag-code-mcp/internal/skills"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListSkillsTool implements MCPTool to list available skills.
type ListSkillsTool struct {
	engine *engine.Engine
}

func NewListSkillsTool(eng *engine.Engine) *ListSkillsTool {
	return &ListSkillsTool{engine: eng}
}

func (t *ListSkillsTool) Name() string { return "rag_list_skills" }
func (t *ListSkillsTool) Description() string {
	return "Lists all available embedded skills and their installation status in the current workspace. " +
		"Use this to see what capabilities (automation, templates, etc.) you can install into a project."
}

type ListSkillsInput struct {
	FilePath string `json:"file_path,omitempty"`
}

func (t *ListSkillsTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ListSkillsInput) (*mcp.CallToolResult, any, error) {
		args := map[string]interface{}{"file_path": input.FilePath}
		start := time.Now()
		result, err := t.Execute(ctx, args)
		if err != nil {
			logger.Instance.Error("rag_list_skills failed (%v): %v", time.Since(start), err)
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}
		logger.Instance.Info("rag_list_skills completed in %v", time.Since(start))
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})
}

func (t *ListSkillsTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	filePath, _ := args["file_path"].(string)

	wctx, err := t.engine.DetectContext(ctx, filePath)
	if err != nil {
		response := ToolResponse{
			Status: "error",
			Error:  err.Error(),
		}
		return response.JSON()
	}

	workspaceRoot := wctx.Root
	source := wctx.DetectionSource

	available, err := skills.ListAvailableSkills()
	if err != nil {
		return "", fmt.Errorf("failed to list available skills: %w", err)
	}

	response := ToolResponse{
		Status: "success",
		Context: ContextMetadata{
			WorkspaceRoot:    workspaceRoot,
			DetectionSource:  source,
			IndexingProgress: BuildIndexingProgress(t.engine, wctx.ID, wctx.Root),
		},
	}
	response.SetFallbackWarning(source == "registry_fallback")

	type skillStatus struct {
		skills.SkillInfo
		Installed bool `json:"installed"`
	}

	results := make([]skillStatus, 0, len(available))
	for _, s := range available {
		installed := false
		if workspaceRoot != "" {
			installed = skills.IsSkillInstalled(s.ID, workspaceRoot)
		}
		results = append(results, skillStatus{
			SkillInfo: s,
			Installed: installed,
		})
	}

	response.Data = results
	return response.JSON()
}

// InstallSkillTool implements MCPTool to install a specific skill.
type InstallSkillTool struct {
	engine *engine.Engine
}

func NewInstallSkillTool(eng *engine.Engine) *InstallSkillTool {
	return &InstallSkillTool{engine: eng}
}

func (t *InstallSkillTool) Name() string { return "rag_install_skill" }
func (t *InstallSkillTool) Description() string {
	return "Installs or uninstalls a specific skill into the current workspace. " +
		"Action must be 'install' or 'uninstall'. " +
		"Optional 'target' specifies the tool directory: 'agent' (default/Antigravity/OpenCode), 'agents' (GitHub Copilot), 'claude' (Anthropic Claude), 'cursor' (Cursor), 'windsurf' (Windsurf/Codeium)."
}

type InstallSkillInput struct {
	SkillID  string `json:"skill_id"`
	Action   string `json:"action,omitempty"`
	Target   string `json:"target,omitempty"`
	FilePath string `json:"file_path,omitempty"`
}

func (t *InstallSkillTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input InstallSkillInput) (*mcp.CallToolResult, any, error) {
		args := map[string]interface{}{
			"skill_id":  input.SkillID,
			"action":    input.Action,
			"target":    input.Target,
			"file_path": input.FilePath,
		}
		start := time.Now()
		result, err := t.Execute(ctx, args)
		if err != nil {
			logger.Instance.Error("rag_install_skill failed (%v): %v", time.Since(start), err)
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}
		logger.Instance.Info("rag_install_skill completed in %v", time.Since(start))
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})
}

func (t *InstallSkillTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	skillID, _ := args["skill_id"].(string)
	action, _ := args["action"].(string)
	target, _ := args["target"].(string)

	if skillID == "" {
		return "", fmt.Errorf("skill_id is required")
	}
	if action == "" {
		action = "install"
	}
	if target == "" {
		target = "agent"
	}

	filePath, _ := args["file_path"].(string)

	wctx, err := t.engine.DetectContext(ctx, filePath)
	if err != nil {
		response := ToolResponse{
			Status: "error",
			Error:  err.Error(),
		}
		return response.JSON()
	}

	workspaceRoot := wctx.Root
	source := wctx.DetectionSource

	var installErr error
	if action == "uninstall" {
		installErr = skills.UninstallSkill(skillID, workspaceRoot)
	} else {
		installErr = skills.InstallSkill(skillID, workspaceRoot, target)
	}

	response := ToolResponse{
		Status: "success",
		Context: ContextMetadata{
			WorkspaceRoot:    workspaceRoot,
			DetectionSource:  source,
			IndexingProgress: BuildIndexingProgress(t.engine, wctx.ID, wctx.Root),
		},
	}
	response.SetFallbackWarning(source != "explicit_file_path")

	if installErr != nil {
		response.Status = "error"
		response.Error = installErr.Error()
		return response.JSON()
	}

	if action == "uninstall" {
		response.Message = fmt.Sprintf("Skill '%s' successfully uninstalled from %s", skillID, workspaceRoot)
	} else {
		response.Message = fmt.Sprintf("Skill '%s' successfully installed into %s", skillID, workspaceRoot)
	}

	return response.JSON()
}
