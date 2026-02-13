package tools

import (
    "context"
    "errors"
    "strings"
    "testing"

    "github.com/doITmagic/rag-code-mcp/internal/updater"
)

func restoreUpdateStubs() func() {
    origCheck := checkForUpdates
    origDownload := downloadAndVerify
    origApply := applyUpdateFunc
    return func() {
        checkForUpdates = origCheck
        downloadAndVerify = origDownload
        applyUpdateFunc = origApply
    }
}

func TestCheckUpdateTool_NoUpdates(t *testing.T) {
    defer restoreUpdateStubs()()
    forced := false
    checkForUpdates = func(ctx context.Context, version string, force bool) (*updater.UpdateInfo, error) {
        forced = force
        return nil, nil
    }

    tool := NewCheckUpdateTool("1.0.0")
    msg, err := tool.Execute(context.Background(), map[string]interface{}{})
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if !strings.Contains(msg, "latest version (1.0.0)") {
        t.Fatalf("unexpected message: %s", msg)
    }
    if forced {
        t.Fatalf("expected force=false, got true")
    }
}

func TestCheckUpdateTool_WithUpdate(t *testing.T) {
    defer restoreUpdateStubs()()
    checkForUpdates = func(ctx context.Context, version string, force bool) (*updater.UpdateInfo, error) {
        return &updater.UpdateInfo{LatestVersion: "2.0.0"}, nil
    }

    tool := NewCheckUpdateTool("1.0.0")
    msg, err := tool.Execute(context.Background(), map[string]interface{}{"force": true})
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if !strings.Contains(msg, "2.0.0") {
        t.Fatalf("expected message to mention newer version, got %s", msg)
    }
}

func TestCheckUpdateTool_Error(t *testing.T) {
    defer restoreUpdateStubs()()
    checkForUpdates = func(ctx context.Context, version string, force bool) (*updater.UpdateInfo, error) {
        return nil, errors.New("boom")
    }

    tool := NewCheckUpdateTool("1.0.0")
    if _, err := tool.Execute(context.Background(), nil); err == nil {
        t.Fatalf("expected error")
    }
}

func TestApplyUpdateTool_NoUpdate(t *testing.T) {
    defer restoreUpdateStubs()()
    forced := false
    checkForUpdates = func(ctx context.Context, version string, force bool) (*updater.UpdateInfo, error) {
        forced = force
        return nil, nil
    }

    tool := NewApplyUpdateTool("1.0.0")
    msg, err := tool.Execute(context.Background(), map[string]interface{}{})
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if !strings.Contains(msg, "latest version") {
        t.Fatalf("unexpected message: %s", msg)
    }
    if !forced {
        t.Fatalf("expected force=true by default for apply_update")
    }
}

func TestApplyUpdateTool_Success(t *testing.T) {
    defer restoreUpdateStubs()()
    var downloadCalled, applyCalled bool
    var capturedPath string

    info := &updater.UpdateInfo{LatestVersion: "2.0.0", AssetURL: "foo.tar.gz"}
    checkForUpdates = func(ctx context.Context, version string, force bool) (*updater.UpdateInfo, error) {
        return info, nil
    }
    downloadAndVerify = func(gotInfo *updater.UpdateInfo, ctx context.Context, dest string) error {
        downloadCalled = true
        if gotInfo != info {
            t.Fatalf("download received unexpected info pointer")
        }
        capturedPath = dest
        return nil
    }
    applyUpdateFunc = func(path string) error {
        applyCalled = true
        if path != capturedPath {
            t.Fatalf("apply called with %s, want %s", path, capturedPath)
        }
        return nil
    }

    tool := NewApplyUpdateTool("1.0.0")
    msg, err := tool.Execute(context.Background(), nil)
    if err != nil {
        t.Fatalf("unexpected error: %v", err)
    }
    if !downloadCalled || !applyCalled {
        t.Fatalf("expected download and apply to be called")
    }
    if !strings.Contains(msg, "2.0.0") {
        t.Fatalf("expected success message to mention new version")
    }
}

func TestApplyUpdateTool_DownloadError(t *testing.T) {
    defer restoreUpdateStubs()()
    checkForUpdates = func(ctx context.Context, version string, force bool) (*updater.UpdateInfo, error) {
        return &updater.UpdateInfo{LatestVersion: "2.0.0", AssetURL: "foo.tar.gz"}, nil
    }
    downloadAndVerify = func(info *updater.UpdateInfo, ctx context.Context, dest string) error {
        return errors.New("download failed")
    }

    tool := NewApplyUpdateTool("1.0.0")
    if _, err := tool.Execute(context.Background(), nil); err == nil {
        t.Fatalf("expected error when download fails")
    }
}

func TestApplyUpdateTool_ApplyError(t *testing.T) {
    defer restoreUpdateStubs()()
    checkForUpdates = func(ctx context.Context, version string, force bool) (*updater.UpdateInfo, error) {
        return &updater.UpdateInfo{LatestVersion: "2.0.0", AssetURL: "foo.tar.gz"}, nil
    }
    downloadAndVerify = func(info *updater.UpdateInfo, ctx context.Context, dest string) error {
        return nil
    }
    applyUpdateFunc = func(path string) error {
        return errors.New("apply failed")
    }

    tool := NewApplyUpdateTool("1.0.0")
    if _, err := tool.Execute(context.Background(), nil); err == nil {
        t.Fatalf("expected error when apply fails")
    }
}
