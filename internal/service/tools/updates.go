package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/logger"
	"github.com/doITmagic/rag-code-mcp/internal/updater"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	checkForUpdates   = updater.CheckForUpdates
	applyUpdateFunc   = updater.ApplyUpdate
	downloadAndVerify = func(info *updater.UpdateInfo, ctx context.Context, dest string) error {
		return info.DownloadAndVerify(ctx, dest)
	}
)

type CheckUpdateTool struct {
	version string
}

func NewCheckUpdateTool(version string) *CheckUpdateTool {
	return &CheckUpdateTool{version: version}
}

func (t *CheckUpdateTool) Name() string { return "rag_check_update" }
func (t *CheckUpdateTool) Description() string {
	return "Checks for available ragcode-mcp updates on GitHub and reports if a newer version is available."
}

type CheckUpdateInput struct {
	Force bool `json:"force,omitempty"`
}

func (t *CheckUpdateTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input CheckUpdateInput) (*mcp.CallToolResult, any, error) {
		args := map[string]interface{}{"force": input.Force}
		start := time.Now()
		result, err := t.Execute(ctx, args)
		if err != nil {
			logger.Instance.Error("rag_check_update failed (%v): %v", time.Since(start), err)
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}
		logger.Instance.Info("rag_check_update completed in %v", time.Since(start))
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})
}

func (t *CheckUpdateTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	force, _ := args["force"].(bool)
	info, err := checkForUpdates(ctx, t.version, force)

	response := ToolResponse{
		Status: "success",
		Context: ContextMetadata{
			Language: t.version, // Use language field to store current version for info
		},
	}

	if err != nil {
		response.Status = "error"
		response.Error = err.Error()
		return response.JSON()
	}

	if info == nil {
		response.Message = fmt.Sprintf("✅ You are using the latest version (%s).", t.version)
		return response.JSON()
	}

	response.Message = fmt.Sprintf("🌟 New version available: %s", info.LatestVersion)
	response.Data = info
	return response.JSON()
}

type ApplyUpdateTool struct {
	version string
}

func NewApplyUpdateTool(version string) *ApplyUpdateTool {
	return &ApplyUpdateTool{version: version}
}

func (t *ApplyUpdateTool) Name() string { return "rag_apply_update" }
func (t *ApplyUpdateTool) Description() string {
	return "Downloads and installs the latest version of ragcode-mcp. The server will need to be restarted after completion."
}

type ApplyUpdateInput struct {
	Force bool `json:"force,omitempty"`
}

func (t *ApplyUpdateTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ApplyUpdateInput) (*mcp.CallToolResult, any, error) {
		args := map[string]interface{}{"force": input.Force}
		start := time.Now()
		result, err := t.Execute(ctx, args)
		if err != nil {
			logger.Instance.Error("rag_apply_update failed (%v): %v", time.Since(start), err)
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}
		logger.Instance.Info("rag_apply_update completed in %v", time.Since(start))
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})
}

func (t *ApplyUpdateTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	force, ok := args["force"].(bool)
	if !ok {
		force = true // Default to true for apply_update
	}

	response := ToolResponse{
		Status: "success",
		Context: ContextMetadata{
			Language: t.version,
		},
	}

	info, err := checkForUpdates(ctx, t.version, force)
	if err != nil {
		response.Status = "error"
		response.Error = err.Error()
		return response.JSON()
	}
	if info == nil {
		response.Message = "✅ You are already on the latest version."
		return response.JSON()
	}

	ext := ".tar.gz"
	if strings.HasSuffix(info.AssetURL, ".zip") {
		ext = ".zip"
	}

	tempFile, err := os.CreateTemp("", "ragcode_update_*"+ext)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	tempFile.Close()
	defer os.Remove(tempPath)

	if err := downloadAndVerify(info, ctx, tempPath); err != nil {
		return "", fmt.Errorf("failed to download update: %w", err)
	}

	if err := applyUpdateFunc(tempPath); err != nil {
		return "", fmt.Errorf("failed to install update: %w", err)
	}

	response.Message = fmt.Sprintf("✅ Successfully updated to %s! Please restart your IDE or MCP command to use the new version.", info.LatestVersion)
	response.Data = info
	return response.JSON()
}
