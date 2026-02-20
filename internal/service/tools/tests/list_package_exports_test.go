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
			json.Unmarshal([]byte(resJSON), &resp)
			Expect(resp.Status).To(Equal("error"))
			Expect(resp.Error).To(ContainSubstring("package parameter is required"))
		})

		It("should return exported symbols for a package", func() {
			mockStore.SearchFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
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
								"name":    "ExportedFunc",
								"type":    "function",
								"package": "mypkg",
								"code":    "func ExportedFunc() {}",
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
			json.Unmarshal([]byte(resJSON), &resp)
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
	})
})
