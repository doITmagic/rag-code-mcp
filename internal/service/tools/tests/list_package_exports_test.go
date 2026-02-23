package tests

import (
	"context"
	"encoding/json"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

var _ = Describe("ListPackageExportsTool", func() {
	var (
		mockStore *mockVectorStore
		tool      *tools.ListPackageExportsTool
		ctx       context.Context
	)

	BeforeEach(func() {
		mockStore = &mockVectorStore{
			CollectionExistsFunc: func(ctx context.Context, name string) (bool, error) {
				return true, nil
			},
		}
		eng := setupTestEngine(mockStore)
		tool = tools.NewListPackageExportsTool(eng)
		ctx = context.Background()
	})

	Describe("Execute", func() {
		It("should fail if package is missing", func() {
			resJSON, err := tool.Execute(ctx, map[string]interface{}{"file_path": "main.go"})
			Expect(err).NotTo(HaveOccurred())
			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal("error"))
			Expect(resp.Error).To(ContainSubstring("package parameter is required"))
		})

		It("should return exported symbols for a package", func() {
			mockStore.ExactSearchFunc = func(ctx context.Context, col string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
				// Only return for "go" collection to simplify
				if !strings.Contains(col, "-go") {
					return []storage.SearchResult{}, nil
				}

				return []storage.SearchResult{
					{
						Score: 1.0,
						Point: storage.Point{
							ID: "sym-1",
							Payload: map[string]interface{}{
								"name":      "ExportedFunc",
								"type":      "function",
								"package":   "mypkg",
								"code":      "func ExportedFunc() {}",
								"is_public": true,
							},
						},
					},
				}, nil
			}

			resJSON, err := tool.Execute(ctx, map[string]interface{}{
				"package":   "mypkg",
				"file_path": "main.go",
			})
			Expect(err).NotTo(HaveOccurred())

			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			if resp.Status != "success" {
				GinkgoWriter.Printf("ListPackageExports Error: %s\n", resp.Error)
			}
			Expect(resp.Status).To(Equal("success"))

			// Verify Data is present
			Expect(resp.Data).NotTo(BeNil(), "Data should not be nil if exports found")
			data := resp.Data.(map[string]interface{})

			// Symbol types are keys
			funcs := data["function"].([]interface{})
			Expect(funcs).To(HaveLen(1))

			Expect(resp.Message).To(ContainSubstring("Found package exports"))
		})

		It("should NOT include is_public in the Qdrant filter", func() {
			var capturedFilter map[string]interface{}
			mockStore.ExactSearchFunc = func(ctx context.Context, col string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
				capturedFilter = filters
				return []storage.SearchResult{}, nil
			}

			_, _ = tool.Execute(ctx, map[string]interface{}{"package": "mypkg", "file_path": "main.go"})

			Expect(capturedFilter).NotTo(BeNil())
			Expect(capturedFilter["package"]).To(Equal("mypkg"))
			_, hasIsPublic := capturedFilter["is_public"]
			Expect(hasIsPublic).To(BeFalse(), "is_public should NOT be in the Qdrant filter — filtering is done in Go code")
		})

		It("should include symbol when is_public is absent but name is exported (isExported fallback)", func() {
			mockStore.ExactSearchFunc = func(ctx context.Context, col string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
				if !strings.HasSuffix(col, "-go") {
					return []storage.SearchResult{}, nil
				}
				return []storage.SearchResult{
					// is_public absent (old indexed entry), but name is uppercase-exported
					{Score: 1.0, Point: storage.Point{ID: "old-sym", Payload: map[string]interface{}{
						"name": "ExportedOldFunc", "type": "function", "package": "mypkg",
					}}},
				}, nil
			}

			resJSON, err := tool.Execute(ctx, map[string]interface{}{"package": "mypkg", "file_path": "main.go"})
			Expect(err).NotTo(HaveOccurred())
			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal("success"))
			data := resp.Data.(map[string]interface{})
			funcs := data["function"].([]interface{})
			Expect(funcs).To(HaveLen(1), "ExportedOldFunc should be included via isExported fallback")
		})

		It("should exclude symbol when is_public is absent and name is NOT exported", func() {
			mockStore.ExactSearchFunc = func(ctx context.Context, col string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
				if !strings.HasSuffix(col, "-go") {
					return []storage.SearchResult{}, nil
				}
				return []storage.SearchResult{
					// is_public absent and name starts lowercase → unexported
					{Score: 1.0, Point: storage.Point{ID: "priv-sym", Payload: map[string]interface{}{
						"name": "internalHelper", "type": "function", "package": "mypkg",
					}}},
				}, nil
			}

			resJSON, err := tool.Execute(ctx, map[string]interface{}{"package": "mypkg", "file_path": "main.go"})
			Expect(err).NotTo(HaveOccurred())
			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal("success"))
			Expect(resp.Data).To(BeNil(), "internalHelper should be excluded by isExported check")
		})

		It("should populate RelationsCount and show it in output", func() {
			mockStore.ExactSearchFunc = func(ctx context.Context, col string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
				if !strings.HasSuffix(col, "-go") {
					return []storage.SearchResult{}, nil
				}
				return []storage.SearchResult{
					{Score: 1.0, Point: storage.Point{ID: "sym-rel", Payload: map[string]interface{}{
						"name": "MyExportedFn", "type": "function", "package": "mypkg",
						"is_public": true,
						"relations": []interface{}{
							map[string]interface{}{"target_name": "A", "type": "calls"},
							map[string]interface{}{"target_name": "B", "type": "calls"},
							map[string]interface{}{"target_name": "C", "type": "calls"},
						},
					}}},
				}, nil
			}

			resJSON, err := tool.Execute(ctx, map[string]interface{}{"package": "mypkg", "file_path": "main.go"})
			Expect(err).NotTo(HaveOccurred())
			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal("success"))
			Expect(resp.Message).To(ContainSubstring("**Relations:** 3"), "Output should display RelationsCount when > 0")
		})
	})
})
