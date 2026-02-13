package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/doITmagic/rag-code-mcp/internal/updater"
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

func (t *CheckUpdateTool) Name() string { return "check_update" }
func (t *CheckUpdateTool) Description() string {
	return "Checks for available ragcode-mcp updates on GitHub and reports if a newer version is available."
}

func (t *CheckUpdateTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	force := false
	if f, ok := args["force"].(bool); ok {
		force = f
	}
	info, err := checkForUpdates(ctx, t.version, force)
	if err != nil {
		return "", fmt.Errorf("failed to check for updates: %w", err)
	}

	if info == nil {
		return fmt.Sprintf("✅ You are using the latest version (%s).", t.version), nil
	}

	return fmt.Sprintf("🌟 New version available: %s\nRun 'apply_update' to upgrade.", info.LatestVersion), nil
}

type ApplyUpdateTool struct {
	version string
}

func NewApplyUpdateTool(version string) *ApplyUpdateTool {
	return &ApplyUpdateTool{version: version}
}

func (t *ApplyUpdateTool) Name() string { return "apply_update" }
func (t *ApplyUpdateTool) Description() string {
	return "Downloads and installs the latest version of ragcode-mcp. The server will need to be restarted after completion."
}

func (t *ApplyUpdateTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	force := true // default to true for apply_update if not specified
	if f, ok := args["force"].(bool); ok {
		force = f
	}

	info, err := checkForUpdates(ctx, t.version, force)
	if err != nil {
		return "", err
	}
	if info == nil {
		return "✅ You are already on the latest version.", nil
	}

	// Create a unique temporary file securely
	// Use pattern with extension based on asset type
	ext := ".tar.gz"
	if strings.HasSuffix(info.AssetURL, ".zip") {
		ext = ".zip"
	}

	tempFile, err := os.CreateTemp("", "ragcode_update_*"+ext)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil { // Close immediately, DownloadAndVerify re-opens/overwrites it
		os.Remove(tempPath)
		return "", fmt.Errorf("failed to close temp file: %w", err)
	}
	defer os.Remove(tempPath)

	if err := downloadAndVerify(info, ctx, tempPath); err != nil {
		return "", fmt.Errorf("failed to download update: %w", err)
	}

	if err := applyUpdateFunc(tempPath); err != nil {
		return "", fmt.Errorf("failed to install update: %w", err)
	}

	return fmt.Sprintf("✅ Successfully updated to %s! Please restart your IDE or MCP command to use the new version.", info.LatestVersion), nil
}
