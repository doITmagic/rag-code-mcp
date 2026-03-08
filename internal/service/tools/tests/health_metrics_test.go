package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

var _ = Describe("Health Metrics & Index Status", func() {
	var (
		mockStore *mockVectorStore
		ctx       context.Context
	)

	BeforeEach(func() {
		mockStore = &mockVectorStore{
			CollectionExistsFunc: func(ctx context.Context, name string) (bool, error) {
				return true, nil
			},
		}
		ctx = context.Background()
	})


	// ─── 2. Stale chunk detection ────────────────────────────────────────────────

	Describe("rag_search (SmartSearchTool) — stale chunk detection", func() {
		var tool *tools.SmartSearchTool

		BeforeEach(func() {
			eng := setupTestEngine(mockStore)
			tool = tools.NewSmartSearchTool(eng)
		})

		It("adds a warning when indexed file no longer exists on disk", func() {
			missingPath := filepath.Join(os.TempDir(), "ragcode_test_missing_file_xyz.go")
			// Ensure file does NOT exist
			_ = os.Remove(missingPath)

			mockStore.SearchCodeOnlyFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
				return []storage.SearchResult{
					{
						Score: 0.9,
						Point: storage.Point{
							ID: "stale-id",
							Payload: map[string]interface{}{
								"name":       "DeletedFunc",
								"content":    "func DeletedFunc() {}",
								"file_path":  missingPath,
								"type":       "function",
								"signature":  "func DeletedFunc()",
								"start_line": float64(1),
								"end_line":   float64(3),
							},
						},
					},
				}, nil
			}

			resJSON, err := tool.Execute(ctx, tools.SmartSearchInput{
				Query:    "deleted func",
				FilePath: "main.go",
			})
			Expect(err).NotTo(HaveOccurred())

			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal("success"))
			Expect(resp.Warning).To(ContainSubstring("stale file(s) detected"))
			Expect(resp.Warning).To(ContainSubstring(missingPath))
			Expect(resp.Warning).To(ContainSubstring("Auto-cleanup triggered"))

			// Stale results should be FILTERED OUT from data
			if resp.Data != nil {
				data := resp.Data.([]interface{})
				Expect(data).To(HaveLen(0))
			}
		})

		It("does NOT add stale warning when all indexed files exist on disk", func() {
			// Create a real temp file
			tmpFile, err := os.CreateTemp("", "ragcode_test_existing_*.go")
			Expect(err).NotTo(HaveOccurred())
			tmpFile.Close()
			defer os.Remove(tmpFile.Name())

			mockStore.SearchCodeOnlyFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
				return []storage.SearchResult{
					{
						Score: 0.9,
						Point: storage.Point{
							ID: "valid-id",
							Payload: map[string]interface{}{
								"name":       "ExistingFunc",
								"content":    "func ExistingFunc() {}",
								"file_path":  tmpFile.Name(),
								"type":       "function",
								"signature":  "func ExistingFunc()",
								"start_line": float64(1),
								"end_line":   float64(3),
							},
						},
					},
				}, nil
			}

			resJSON, err := tool.Execute(ctx, tools.SmartSearchInput{
				Query:    "existing func",
				FilePath: "main.go",
			})
			Expect(err).NotTo(HaveOccurred())

			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal("success"))
			Expect(resp.Warning).NotTo(ContainSubstring("stale index"))
		})

		It("counts multiple missing files in the warning", func() {
			missing1 := filepath.Join(os.TempDir(), "ragcode_missing_a.go")
			missing2 := filepath.Join(os.TempDir(), "ragcode_missing_b.go")
			_ = os.Remove(missing1)
			_ = os.Remove(missing2)

			mockStore.SearchCodeOnlyFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
				return []storage.SearchResult{
					{Score: 0.9, Point: storage.Point{ID: "id1", Payload: map[string]interface{}{"name": "A", "content": "x", "file_path": missing1, "type": "function", "start_line": float64(1), "end_line": float64(1)}}},
					{Score: 0.8, Point: storage.Point{ID: "id2", Payload: map[string]interface{}{"name": "B", "content": "y", "file_path": missing2, "type": "function", "start_line": float64(1), "end_line": float64(1)}}},
				}, nil
			}

			resJSON, err := tool.Execute(ctx, tools.SmartSearchInput{
				Query:    "func",
				FilePath: "main.go",
			})
			Expect(err).NotTo(HaveOccurred())

			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			Expect(resp.Warning).To(ContainSubstring("2 stale file(s)"))
		})
	})
})
