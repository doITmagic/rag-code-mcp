package runner

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func requireContainerCmd(t *testing.T) {
	t.Helper()
	cmd := os.Getenv("RAGCODE_LXC_CMD")
	if cmd == "" {
		if _, err := exec.LookPath("lxc"); err == nil {
			cmd = "lxc"
		} else if _, err := exec.LookPath("incus"); err == nil {
			cmd = "incus"
		}
	}
	if cmd == "" {
		t.Skip("neither lxc nor incus found (set RAGCODE_LXC_CMD if installed elsewhere)")
	}
}

func buildBinaries(t *testing.T, repoRoot, binDir string) {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", binDir, err)
	}

	build := func(outName, pkg string) {
		cmd := exec.Command("go", "build", "-o", filepath.Join(binDir, outName), pkg)
		cmd.Dir = repoRoot
		var out bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			t.Fatalf("build %s failed: %v\n%s", pkg, err, out.String())
		}
	}

	build("rag-code-install", "./cmd/rag-code-install")
	build("rag-code-mcp", "./cmd/rag-code-mcp")
	build("sse-client-test", "./cmd/sse-client-test")
}

func TestRunner_CleanDockerScenario_WritesCaptureFiles(t *testing.T) {
	if os.Getenv("RAGCODE_E2E") != "1" {
		t.Skip("set RAGCODE_E2E=1 to enable LXC E2E tests")
	}
	requireContainerCmd(t)

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// tests/e2e/runner -> tests/e2e -> repo root
	repoRoot = filepath.Clean(filepath.Join(repoRoot, "..", "..", ".."))

	scenarioFile := filepath.Join(repoRoot, "tests", "e2e", "scenarios", "clean_docker.json")
	binDir := filepath.Join(repoRoot, "tests", "e2e", "bin")
	outDir := filepath.Join(repoRoot, "tests", "e2e", "out")

	buildBinaries(t, repoRoot, binDir)

	var logf func(string, ...any)
	if os.Getenv("RAGCODE_E2E_LOG") == "1" {
		logf = t.Logf
	}
	r := NewRunner(logf)

	overrides := map[string]string{
		"bin_dir":      binDir,
		"out_base_dir": outDir,
	}

	if err := r.RunScenarioFile(context.Background(), scenarioFile, overrides); err != nil {
		t.Fatalf("scenario failed: %v", err)
	}

	// Capture in clean_docker.json: capture.as = search_result
	capFile := filepath.Join(outDir, "clean_docker", "search_result.json")
	if _, err := os.Stat(capFile); err != nil {
		t.Fatalf("expected capture file to exist: %s (%v)", capFile, err)
	}
}

func TestGolden_RagSearchCode_CurrentBinaries_RepoV1120(t *testing.T) {
	if os.Getenv("RAGCODE_E2E") != "1" {
		t.Skip("set RAGCODE_E2E=1 to enable LXC E2E tests")
	}
	requireContainerCmd(t)

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot = filepath.Clean(filepath.Join(repoRoot, "..", "..", ".."))

	cleanScenario := filepath.Join(repoRoot, "tests", "e2e", "scenarios", "clean_docker.json")
	compareScenario := filepath.Join(repoRoot, "tests", "e2e", "scenarios", "golden_compare_rag_search_code.json")
	binDir := filepath.Join(repoRoot, "tests", "e2e", "bin")
	outDir := filepath.Join(repoRoot, "tests", "e2e", "out")

	buildBinaries(t, repoRoot, binDir)

	// 1) Run current binaries, but inside container checkout repo to v1.1.20
	var logf func(string, ...any)
	if os.Getenv("RAGCODE_E2E_LOG") == "1" {
		logf = t.Logf
	}
	run := NewRunner(logf)
	if err := run.RunScenarioFile(context.Background(), cleanScenario, map[string]string{
		"bin_dir":      binDir,
		"out_base_dir": outDir,
		"repo_ref":     "v1.1.20",
	}); err != nil {
		t.Fatalf("clean scenario failed: %v", err)
	}

	// 2) Compare capture vs golden file (normalized). First run can generate golden with RAGCODE_GOLDEN_UPDATE=1.
	right := filepath.Join(outDir, "clean_docker", "search_result.json")
	left := filepath.Join(repoRoot, "tests", "e2e", "golden", "v1.1.20", "clean_docker", "search_result.norm")

	cmp := NewRunner(logf)
	if err := cmp.RunScenarioFile(context.Background(), compareScenario, map[string]string{
		"left":      left,
		"right":     right,
		"max_items": "0",
	}); err != nil {
		t.Fatalf("golden compare failed: %v", err)
	}
}
