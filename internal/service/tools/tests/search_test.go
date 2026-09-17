package tests

import (
	"context"
	"encoding/json"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

var _ = Describe("SmartSearchTool (rag_search)", func() {
	var (
		mockStore *mockVectorStore
		tool      *tools.SmartSearchTool
		ctx       context.Context
	)

	BeforeEach(func() {
		mockStore = &mockVectorStore{
			CollectionExistsFunc: func(ctx context.Context, name string) (bool, error) {
				return true, nil
			},
		}
		eng := setupTestEngine(mockStore)
		tool = tools.NewSmartSearchTool(eng)
		ctx = context.Background()
	})

	Describe("Executing search", func() {
		Context("Basic Parameter Validation", func() {
			It("should fail if query is missing", func() {
				_, err := tool.Execute(ctx, tools.SmartSearchInput{})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("query parameter is required"))
			})

			It("should return no_results when nothing matches", func() {
				mockStore.SearchCodeOnlyFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
					return []storage.SearchResult{}, nil
				}
				// A token no file contains: _test.go files are indexed now, so "test"
				// would be found by the on-disk fallback scan.
				resJSON, err := tool.Execute(ctx, tools.SmartSearchInput{Query: "zzqxnomatch", FilePath: "main.go"})
				Expect(err).NotTo(HaveOccurred())
				var resp tools.ToolResponse
				Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
				Expect(resp.Status).To(Equal("no_results"))
			})
		})

		Context("Successful Search", func() {
			var tmpFile *os.File

			BeforeEach(func() {
				// Create a real temp file so stale detection doesn't filter it out
				var err error
				tmpFile, err = os.CreateTemp("", "ragcode_test_search_*.go")
				Expect(err).NotTo(HaveOccurred())
				_, _ = tmpFile.WriteString("package main\nfunc MyFunc() {}\n")
				tmpFile.Close()

				mockStore.SearchCodeOnlyFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
					return []storage.SearchResult{
						{
							Score: 0.9,
							Point: storage.Point{
								ID: "1",
								Payload: map[string]interface{}{
									"name":       "MyFunc",
									"content":    "func MyFunc() {}",
									"file_path":  tmpFile.Name(),
									"type":       "function",
									"signature":  "func MyFunc()",
									"package":    "main",
									"start_line": float64(1),
									"end_line":   float64(3),
								},
							},
						},
					}, nil
				}
			})

			AfterEach(func() {
				if tmpFile != nil {
					os.Remove(tmpFile.Name())
				}
			})

			It("should return success and properly mapped data", func() {
				resJSON, err := tool.Execute(ctx, tools.SmartSearchInput{Query: "find func", FilePath: "main.go"})
				Expect(err).NotTo(HaveOccurred())
				var resp tools.ToolResponse
				Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
				Expect(resp.Status).To(Equal("success"), "Error: "+resp.Error)

				// Verify Data list
				Expect(resp.Data).NotTo(BeNil())
				data := resp.Data.([]interface{})
				Expect(data).To(HaveLen(1))
			})

			It("falls back for inferred identifiers but keeps explicit exact lookup strict", func() {
				result, err := tool.Execute(ctx, tools.SmartSearchInput{Query: "apply_vat", FilePath: tmpFile.Name()})
				Expect(err).NotTo(HaveOccurred())
				var resp tools.ToolResponse
				Expect(json.Unmarshal([]byte(result), &resp)).To(Succeed())
				Expect(resp.Status).To(Equal("success"))
				exact := true
				result, err = tool.Execute(ctx, tools.SmartSearchInput{Query: "apply_vat", FilePath: tmpFile.Name(), ExactSymbol: &exact})
				Expect(err).NotTo(HaveOccurred())
				Expect(json.Unmarshal([]byte(result), &resp)).To(Succeed())
				Expect(resp.Status).To(Equal("no_results"))
			})

			It("finds a single-word symbol by name before semantic search", func() {
				mockStore.ExactSearchFunc = func(_ context.Context, _ string, filters map[string]interface{}, _ int) ([]storage.SearchResult, error) {
					if filters["name"] != "Marker" {
						return nil, nil
					}
					return []storage.SearchResult{{Score: 1, Point: storage.Point{ID: "marker", Payload: map[string]interface{}{
						"name": "Marker", "content": "func Marker() {}", "file_path": tmpFile.Name(), "type": "function",
					}}}}, nil
				}
				result, err := tool.Execute(ctx, tools.SmartSearchInput{Query: "Marker", FilePath: tmpFile.Name()})
				Expect(err).NotTo(HaveOccurred())
				var resp tools.ToolResponse
				Expect(json.Unmarshal([]byte(result), &resp)).To(Succeed())
				Expect(resp.Status).To(Equal("success"))
			})
		})
	})
})
