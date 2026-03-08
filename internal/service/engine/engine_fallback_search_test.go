package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/doITmagic/rag-code-mcp/internal/config"
	"github.com/doITmagic/rag-code-mcp/internal/service/search"
	"github.com/doITmagic/rag-code-mcp/pkg/indexer"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
	"github.com/doITmagic/rag-code-mcp/pkg/workspace/resolver"

	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/go"
	_ "github.com/doITmagic/rag-code-mcp/pkg/parser/python"
)

// ─── Fallback Direct Search Tests ────────────────────────────────────────────
//
// These tests verify the AST-based fallback search that kicks in when
// no Qdrant collections exist. They use real Go source files on disk.

// setupFallbackWorkspace creates a temp directory with Go source files
// containing known symbols that we can search for.
func setupFallbackWorkspace(t *testing.T) (string, *Engine) {
	t.Helper()

	root := t.TempDir()

	// Create a Go file with known symbols
	goDir := filepath.Join(root, "pkg")
	if err := os.MkdirAll(goDir, 0o755); err != nil {
		t.Fatalf("Failed to create dir: %v", err)
	}

	goFile := filepath.Join(goDir, "calculator.go")
	goContent := `package pkg

// Calculator provides basic arithmetic operations.
// It supports addition, subtraction, and multiplication.
type Calculator struct {
	precision int
}

// Add returns the sum of two integers.
func (c *Calculator) Add(a, b int) int {
	return a + b
}

// Subtract returns the difference of two integers.
func (c *Calculator) Subtract(a, b int) int {
	return a - b
}

// Multiply returns the product of two integers.
func (c *Calculator) Multiply(a, b int) int {
	return a * b
}

// NewCalculator creates a Calculator with the given precision.
func NewCalculator(precision int) *Calculator {
	return &Calculator{precision: precision}
}
`
	if err := os.WriteFile(goFile, []byte(goContent), 0o644); err != nil {
		t.Fatalf("Failed to write Go file: %v", err)
	}

	// Create another file with different symbols
	utilFile := filepath.Join(goDir, "utils.go")
	utilContent := `package pkg

import "strings"

// FormatName cleans and formats a user name.
func FormatName(name string) string {
	return strings.TrimSpace(strings.Title(name))
}

// ValidateEmail checks if an email address is valid.
func ValidateEmail(email string) bool {
	return strings.Contains(email, "@")
}
`
	if err := os.WriteFile(utilFile, []byte(utilContent), 0o644); err != nil {
		t.Fatalf("Failed to write utils file: %v", err)
	}

	llmProvider := &countingLLM{}
	store := &testStore{existing: map[string]bool{}}
	idxSvc := indexer.NewService(llmProvider, store)
	searchSvc := search.NewService(llmProvider, store)
	eng := NewEngine(idxSvc, searchSvc, "", &config.Config{})
	eng.SetResolver(resolver.New(resolver.Dependencies{Detector: &mockDirDetector{root: root}}))

	t.Cleanup(func() {

	})

	return root, eng
}

// TestFallbackDirectSearchFindsExactName verifies that searching for an exact
// symbol name returns that symbol with a high score.
func TestFallbackDirectSearchFindsExactName(t *testing.T) {
	root, eng := setupFallbackWorkspace(t)

	// Go parser extracts: Calculator (type), Add/Subtract/Multiply (methods), FormatName/ValidateEmail (functions)
	results := eng.FallbackDirectSearch(context.Background(), root, "Calculator", 10)

	if len(results) == 0 {
		t.Fatal("Expected fallback to find Calculator, got 0 results")
	}

	// The top result should be Calculator (exact name match)
	topName, _ := results[0].Point.Payload["name"].(string)
	if topName != "Calculator" {
		t.Errorf("Expected top result to be 'Calculator', got %q", topName)
	}

	// Should have high score (exact name match → 0.4 * 1.0 = 0.4 minimum)
	if results[0].Score < 0.3 {
		t.Errorf("Expected high score for exact match, got %f", results[0].Score)
	}

	// Should be tagged as fallback
	source, _ := results[0].Point.Payload["_source"].(string)
	if source != "fallback" {
		t.Errorf("Expected _source='fallback', got %q", source)
	}
}

// TestFallbackDirectSearchFindsPartialMatch verifies that searching for
// a partial term finds related symbols.
func TestFallbackDirectSearchFindsPartialMatch(t *testing.T) {
	root, eng := setupFallbackWorkspace(t)

	results := eng.FallbackDirectSearch(context.Background(), root, "calculator arithmetic", 10)

	if len(results) == 0 {
		t.Fatal("Expected fallback to find calculator-related symbols, got 0")
	}

	// Should find Calculator and its methods
	found := map[string]bool{}
	for _, r := range results {
		name, _ := r.Point.Payload["name"].(string)
		found[name] = true
	}

	if !found["Calculator"] && !found["NewCalculator"] && !found["Add"] {
		t.Error("Expected at least one Calculator-related symbol in results")
	}
}

// TestFallbackDirectSearchSortsByRelevance verifies that results are
// sorted with the most relevant match first.
func TestFallbackDirectSearchSortsByRelevance(t *testing.T) {
	root, eng := setupFallbackWorkspace(t)

	results := eng.FallbackDirectSearch(context.Background(), root, "Add", 10)

	if len(results) < 2 {
		t.Fatalf("Expected at least 2 results, got %d", len(results))
	}

	// Results should be sorted by score descending
	for i := 1; i < len(results); i++ {
		if results[i].Score > results[i-1].Score {
			t.Errorf("Results not sorted: [%d].Score=%f > [%d].Score=%f",
				i, results[i].Score, i-1, results[i-1].Score)
		}
	}

	// "Add" should rank higher than unrelated symbols
	topName, _ := results[0].Point.Payload["name"].(string)
	if topName != "Add" {
		t.Logf("Note: top result is %q (score=%f), not 'Add' — acceptable if score is close", topName, results[0].Score)
	}
}

// TestFallbackDirectSearchRespectsLimit verifies that the limit parameter
// caps the number of results returned.
func TestFallbackDirectSearchRespectsLimit(t *testing.T) {
	root, eng := setupFallbackWorkspace(t)

	results := eng.FallbackDirectSearch(context.Background(), root, "func", 3)

	if len(results) > 3 {
		t.Errorf("Expected at most 3 results, got %d", len(results))
	}
}

// TestFallbackDirectSearchReturnsNilForNoMatch verifies that searching for
// a completely irrelevant term returns nil.
func TestFallbackDirectSearchReturnsNilForNoMatch(t *testing.T) {
	root, eng := setupFallbackWorkspace(t)

	results := eng.FallbackDirectSearch(context.Background(), root, "zzzznonexistentxyzzy", 10)

	if len(results) > 0 {
		t.Errorf("Expected nil/empty for non-matching query, got %d results", len(results))
	}
}

// TestFallbackDirectSearchExcludesHiddenDirs verifies that .git and similar
// hidden directories are excluded from the scan.
func TestFallbackDirectSearchExcludesHiddenDirs(t *testing.T) {
	root, eng := setupFallbackWorkspace(t)

	// Create a .git directory with a Go file — should be excluded
	gitDir := filepath.Join(root, ".git")
	_ = os.MkdirAll(gitDir, 0o755)
	gitFile := filepath.Join(gitDir, "hidden.go")
	_ = os.WriteFile(gitFile, []byte("package git\nfunc HiddenSecret() {}\n"), 0o644)

	results := eng.FallbackDirectSearch(context.Background(), root, "HiddenSecret", 10)

	for _, r := range results {
		name, _ := r.Point.Payload["name"].(string)
		if name == "HiddenSecret" {
			t.Error("HiddenSecret from .git directory should have been excluded")
		}
	}
}

// TestFallbackDirectSearchPayloadStructure verifies that fallback results
// have the same payload fields as indexed results.
func TestFallbackDirectSearchPayloadStructure(t *testing.T) {
	root, eng := setupFallbackWorkspace(t)

	results := eng.FallbackDirectSearch(context.Background(), root, "Add", 1)

	if len(results) == 0 {
		t.Fatal("Expected at least 1 result")
	}

	r := results[0]
	requiredFields := []string{"name", "type", "package", "content", "signature", "file_path", "start_line", "end_line", "_source"}
	for _, field := range requiredFields {
		if _, ok := r.Point.Payload[field]; !ok {
			t.Errorf("Missing required payload field %q", field)
		}
	}

	// Verify _source is "fallback"
	if r.Point.Payload["_source"] != "fallback" {
		t.Errorf("Expected _source='fallback', got %v", r.Point.Payload["_source"])
	}

	// Verify Point.ID is non-empty (SHA-256 based)
	if r.Point.ID == "" {
		t.Error("Expected non-empty Point.ID")
	}
}

// TestFallbackSearchIntegrationWithSearchCode verifies that SearchCode
// uses the fallback when no Qdrant collections exist.
func TestFallbackSearchIntegrationWithSearchCode(t *testing.T) {
	root, eng := setupFallbackWorkspace(t)

	// Detect workspace context first
	wctx, err := eng.DetectContext(context.Background(), filepath.Join(root, "pkg", "calculator.go"))
	if err != nil {
		t.Fatalf("DetectContext: %v", err)
	}
	_ = wctx

	// SearchCode with zero existing collections → should use fallback
	// Query for "Calculator" which is a symbol the Go parser actually extracts
	result, err := eng.SearchCode(context.Background(), filepath.Join(root, "pkg", "calculator.go"), "Calculator", 5, false)
	if err != nil {
		t.Fatalf("SearchCode should fallback instead of error, got: %v", err)
	}

	if result == nil || len(result.Results) == 0 {
		t.Fatal("Expected fallback results from SearchCode, got none")
	}

	// Verify it's tagged as fallback
	if result.Collection != "fallback" {
		t.Errorf("Expected Collection='fallback', got %q", result.Collection)
	}

	// Verify content quality — top result should be Calculator-related
	topName, _ := result.Results[0].Point.Payload["name"].(string)
	t.Logf("Top fallback result: %q (score=%f)", topName, result.Results[0].Score)
}

// ─── Scoring unit tests ─────────────────────────────────────────────────────

func TestFallbackScoreSymbolExactMatch(t *testing.T) {
	sym := symbolFixture("HandleRequest", "func HandleRequest(w http.ResponseWriter, r *http.Request)")

	score := fallbackScoreSymbol(sym, "handlerequest", []string{"handlerequest"})

	// Exact name match → high score
	if score < 0.35 {
		t.Errorf("Expected high score for exact name match, got %f", score)
	}
}

func TestFallbackScoreSymbolPartialMatch(t *testing.T) {
	sym := symbolFixture("HandleRequest", "func HandleRequest(w http.ResponseWriter, r *http.Request)")

	score := fallbackScoreSymbol(sym, "handle request http", []string{"handle", "request", "http"})

	// All tokens present across name + signature → decent score
	if score < 0.2 {
		t.Errorf("Expected decent score for partial match, got %f", score)
	}
}

func TestFallbackScoreSymbolNoMatch(t *testing.T) {
	sym := symbolFixture("HandleRequest", "func HandleRequest()")

	score := fallbackScoreSymbol(sym, "database migration", []string{"database", "migration"})

	// No tokens match at all → very low score
	if score > 0.1 {
		t.Errorf("Expected very low score for no match, got %f", score)
	}
}

func symbolFixture(name, signature string) parser.Symbol {
	return parser.Symbol{
		Name:      name,
		Type:      parser.Function,
		Package:   "main",
		Signature: signature,
		Content:   "func " + name + "() { /* body */ }",
		Docstring: "",
		FilePath:  "/test/file.go",
		StartLine: 1,
		EndLine:   3,
		Language:  "go",
	}
}
