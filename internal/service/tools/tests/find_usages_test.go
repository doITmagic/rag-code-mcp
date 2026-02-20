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

var _ = Describe("FindUsagesTool", func() {
	var (
		mockStore *mockVectorStore
		tool      *tools.FindUsagesTool
		ctx       context.Context
	)

	BeforeEach(func() {
		mockStore = &mockVectorStore{
			CollectionExistsFunc: func(ctx context.Context, name string) (bool, error) {
				return true, nil
			},
		}
		eng := setupTestEngine(mockStore)
		tool = tools.NewFindUsagesTool(eng)
		ctx = context.Background()
	})

	Describe("Execute", func() {
		It("should fail if symbol_name is missing", func() {
			resJSON, err := tool.Execute(ctx, map[string]interface{}{"file_path": "main.go"})
			Expect(err).NotTo(HaveOccurred())
			var resp tools.ToolResponse
			json.Unmarshal([]byte(resJSON), &resp)
			Expect(resp.Status).To(Equal("error"))
			Expect(resp.Error).To(ContainSubstring("symbol_name parameter is required"))
		})

		It("should return no_results if no usages are found", func() {
			mockStore.SearchFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
				return []storage.SearchResult{}, nil
			}
			resJSON, err := tool.Execute(ctx, map[string]interface{}{"symbol_name": "GhostFunc", "file_path": "main.go"})
			Expect(err).NotTo(HaveOccurred())
			var resp tools.ToolResponse
			json.Unmarshal([]byte(resJSON), &resp)

			Expect(resp.Status).To(Equal("success"))
			Expect(resp.Message).To(ContainSubstring("No usages found for symbol 'GhostFunc'"))
		})

		It("should return found usages with markdown formatting", func() {
			mockStore.SearchFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
				// Only return for "go" collection to simplify
				if !strings.Contains(col, "-go") {
					return []storage.SearchResult{}, nil
				}

				return []storage.SearchResult{
					{
						Score: 1.0,
						Point: storage.Point{
							ID: "usage-1",
							Payload: map[string]interface{}{
								"name":       "CallerFunc",
								"type":       "function",
								"file":       "caller.go",
								"start_line": 10,
								"code":       "func CallerFunc() { MySymbol() }",
								"Relations": []interface{}{
									map[string]interface{}{"target_name": "MySymbol", "type": "calls"},
								},
							},
						},
					},
				}, nil
			}

			resJSON, err := tool.Execute(ctx, map[string]interface{}{
				"symbol_name": "MySymbol",
				"file_path":   "main.go",
			})
			Expect(err).NotTo(HaveOccurred())

			var resp tools.ToolResponse
			json.Unmarshal([]byte(resJSON), &resp)

			if resp.Status != "success" {
				GinkgoWriter.Printf("FindUsages Error status: %s, Error: %s\n", resp.Status, resp.Error)
			}
			Expect(resp.Status).To(Equal("success"), "Expected success status")

			// Verify Data list
			Expect(resp.Data).NotTo(BeNil(), "Data should not be nil if usages found")
			data := resp.Data.([]interface{})
			Expect(data).To(HaveLen(1))
			usage := data[0].(map[string]interface{})
			Expect(usage["name"]).To(Equal("CallerFunc"))
		})
	})
})
