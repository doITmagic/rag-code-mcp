package e2e

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

type toolResponse struct {
	Status  string           `json:"status"`
	Message string           `json:"message,omitempty"`
	Warning string           `json:"warning,omitempty"`
	Error   string           `json:"error,omitempty"`
	Context map[string]any   `json:"context"`
	Data    []map[string]any `json:"data"`
}

type normalizedSearch struct {
	Status string
	Items  []string
}

func normalizeSearchJSON(raw []byte, maxItems int) (normalizedSearch, error) {
	var tr toolResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return normalizedSearch{}, fmt.Errorf("unmarshal tool response: %w", err)
	}

	// Only normalize successful/no_results states for deterministic comparison.
	if tr.Status != "success" && tr.Status != "no_results" {
		return normalizedSearch{Status: tr.Status}, nil
	}

	items := make([]string, 0, len(tr.Data))
	for _, d := range tr.Data {
		// Pick stable identifiers. Scores/IDs may change.
		filePath, _ := d["file_path"].(string)
		name, _ := d["name"].(string)
		kind, _ := d["kind"].(string)
		startLine := fmt.Sprint(d["start_line"])
		endLine := fmt.Sprint(d["end_line"])
		// Graph expansion markers should also be stable-ish, keep them.
		graph, _ := d["_graph_expansion"].(string)

		key := strings.Join([]string{filePath, name, kind, startLine, endLine, graph}, "|")
		items = append(items, key)
	}

	sort.Strings(items)
	if maxItems > 0 && len(items) > maxItems {
		items = items[:maxItems]
	}

	return normalizedSearch{Status: tr.Status, Items: items}, nil
}

func extractMarkedBlock(out string, begin, end string) ([]byte, error) {
	b := strings.Index(out, begin)
	e := strings.Index(out, end)
	if b == -1 || e == -1 || e <= b {
		return nil, errors.New("missing SSE output markers")
	}
	b += len(begin)
	block := strings.TrimSpace(out[b:e])
	return []byte(block), nil
}

func buildBinaries(t *testing.T, repoRoot string, outDir string) {
	t.Helper()
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}

	build := func(outName, pkg string) {
		cmd := exec.Command("go", "build", "-o", filepath.Join(outDir, outName), pkg)
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
	build("mcp-http-client-test", "./cmd/mcp-http-client-test")
}

func cloneAtRef(t *testing.T, repoRoot string, ref string) string {
	t.Helper()

	parent := filepath.Dir(repoRoot)
	dst := filepath.Join(parent, fmt.Sprintf("ragcode-baseline-%s", strings.ReplaceAll(ref, "/", "_")))

	// If it exists, we reuse it to avoid re-cloning.
	if _, err := os.Stat(filepath.Join(dst, ".git")); err == nil {
		cmd := exec.Command("git", "fetch", "--tags", "--force")
		cmd.Dir = dst
		_ = cmd.Run()
		cmd = exec.Command("git", "checkout", "-f", ref)
		cmd.Dir = dst
		if err := cmd.Run(); err != nil {
			t.Fatalf("checkout %s in %s: %v", ref, dst, err)
		}
		return dst
	}

	cmd := exec.Command("git", "clone", repoRoot, dst)
	cmd.Dir = parent
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git clone baseline failed: %v\n%s", err, out.String())
	}

	cmd = exec.Command("git", "checkout", "-f", ref)
	cmd.Dir = dst
	out.Reset()
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		t.Fatalf("git checkout %s failed: %v\n%s", ref, err, out.String())
	}
	return dst
}

func runScenarioAndCaptureJSON(t *testing.T, script string, repoRoot string, binDir string, repoRef string, scenario string) normalizedSearch {
	t.Helper()

	cmd := exec.Command(script,
		"-scenario", scenario,
		"-bin-dir", binDir,
		"-repo-ref", repoRef,
		"-sse-mode", "exact",
		"-sse-file", "cmd/rag-code-mcp/main.go",
		"-sse-query", "SearchLocalIndexTool implements the rag_search_code MCP tool",
		"-sse-limit", "5",
	)
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "SSE_EMIT_JSON=1")

	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out

	deadline := 120 * time.Minute
	timer := time.AfterFunc(deadline, func() {
		_ = cmd.Process.Kill()
	})
	defer timer.Stop()

	if err := cmd.Run(); err != nil {
		t.Fatalf("scenario %s failed: %v\n--- output ---\n%s", scenario, err, out.String())
	}

	raw, err := extractMarkedBlock(out.String(), "@@@SSE_OUT_BEGIN@@@", "@@@SSE_OUT_END@@@")
	if err != nil {
		t.Fatalf("extract SSE block: %v\n--- output ---\n%s", err, out.String())
	}

	norm, err := normalizeSearchJSON(raw, 50)
	if err != nil {
		t.Fatalf("normalize: %v\nraw=%s", err, string(raw))
	}
	return norm
}

func TestLXCScenarios(t *testing.T) {
	if os.Getenv("RAGCODE_E2E") != "1" {
		t.Skip("set RAGCODE_E2E=1 to enable LXC E2E tests")
	}
	if _, err := exec.LookPath("lxc"); err != nil {
		t.Skip("lxc not found")
	}

	// Scenarios are intentionally explicit so CI can shard or you can run a subset by editing env.
	scenarios := []string{
		"clean_docker",
		"reinstall_docker",
		"uninstall",
		"ollama_local_running",
		"ollama_local_missing_fallback",
	}
	if s := os.Getenv("RAGCODE_E2E_SCENARIO"); s != "" {
		scenarios = []string{s}
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// tests/e2e is under repo root, but go test runs in package dir.
	// Walk up two levels.
	repoRoot = filepath.Clean(filepath.Join(repoRoot, "..", ".."))
	script := filepath.Join(repoRoot, "tests", "e2e", "run_lxc_tests.sh")

	for _, scenario := range scenarios {
		scenario := scenario
		t.Run(scenario, func(t *testing.T) {
			// These are slow tests due to real model downloads.
			if os.Getenv("RAGCODE_E2E_PARALLEL") == "1" {
				t.Parallel()
			}

			cmd := exec.Command(script, "-scenario", scenario)
			cmd.Dir = repoRoot
			cmd.Env = os.Environ()

			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out

			// Hard ceiling to prevent stuck CI runs.
			deadline := 90 * time.Minute
			if d := os.Getenv("RAGCODE_E2E_TIMEOUT"); d != "" {
				if parsed, err := time.ParseDuration(d); err == nil {
					deadline = parsed
				}
			}

			timer := time.AfterFunc(deadline, func() {
				_ = cmd.Process.Kill()
			})
			defer timer.Stop()

			err := cmd.Run()
			if err != nil {
				t.Fatalf("scenario %s failed: %v\n--- output ---\n%s", scenario, err, out.String())
			}
		})
	}
}

func TestGoldenSearchAgainstV1120(t *testing.T) {
	if os.Getenv("RAGCODE_E2E") != "1" {
		t.Skip("set RAGCODE_E2E=1 to enable LXC E2E tests")
	}
	if os.Getenv("RAGCODE_E2E_GOLDEN") != "1" {
		t.Skip("set RAGCODE_E2E_GOLDEN=1 to enable golden comparison")
	}
	if _, err := exec.LookPath("lxc"); err != nil {
		t.Skip("lxc not found")
	}

	repoRoot, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	repoRoot = filepath.Clean(filepath.Join(repoRoot, "..", ".."))
	script := filepath.Join(repoRoot, "tests", "e2e", "run_lxc_tests.sh")

	baselineRef := os.Getenv("RAGCODE_E2E_BASELINE_REF")
	if baselineRef == "" {
		baselineRef = "v1.1.20"
	}

	baselineRepo := cloneAtRef(t, repoRoot, baselineRef)
	baselineBin := filepath.Join(repoRoot, "tests", "e2e", "bin-baseline")
	headBin := filepath.Join(repoRoot, "tests", "e2e", "bin-head")

	buildBinaries(t, baselineRepo, baselineBin)
	buildBinaries(t, repoRoot, headBin)

	// Index the same codebase version for both runs, so differences come only from tool behavior.
	indexRef := os.Getenv("RAGCODE_E2E_INDEX_REF")
	if indexRef == "" {
		indexRef = baselineRef
	}

	scenario := os.Getenv("RAGCODE_E2E_SCENARIO")
	if scenario == "" {
		scenario = "clean_docker"
	}

	baseline := runScenarioAndCaptureJSON(t, script, repoRoot, baselineBin, indexRef, scenario)
	head := runScenarioAndCaptureJSON(t, script, repoRoot, headBin, indexRef, scenario)

	if baseline.Status != head.Status {
		t.Fatalf("status mismatch: baseline=%s head=%s", baseline.Status, head.Status)
	}
	if strings.Join(baseline.Items, "\n") != strings.Join(head.Items, "\n") {
		t.Fatalf("golden mismatch vs %s\n--- baseline (%d items) ---\n%s\n--- head (%d items) ---\n%s",
			baselineRef,
			len(baseline.Items), strings.Join(baseline.Items, "\n"),
			len(head.Items), strings.Join(head.Items, "\n"),
		)
	}
}
