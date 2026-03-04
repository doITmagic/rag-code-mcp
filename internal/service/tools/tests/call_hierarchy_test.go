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

var _ = Describe("CallHierarchyTool", func() {
	var (
		mockStore *mockVectorStore
		tool      *tools.CallHierarchyTool
		ctx       context.Context
	)

	BeforeEach(func() {
		mockStore = &mockVectorStore{
			CollectionExistsFunc: func(ctx context.Context, name string) (bool, error) {
				return true, nil
			},
		}
		eng := setupTestEngine(mockStore)
		tool = tools.NewCallHierarchyTool(eng)
		ctx = context.Background()
	})

	Describe("Execute", func() {
		It("should fail if symbol_name is missing", func() {
			resJSON, err := tool.Execute(ctx, map[string]interface{}{"file_path": "main.go"})
			Expect(err).NotTo(HaveOccurred())
			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal("error"))
			Expect(resp.Error).To(ContainSubstring("symbol_name parameter is required"))
		})

		It("should return outgoing calls (callees)", func() {
			mockStore.ExactSearchFunc = func(ctx context.Context, col string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
				if !strings.Contains(col, "-go") {
					return []storage.SearchResult{}, nil
				}

				// Mock finding the symbol itself to see its relations
				return []storage.SearchResult{
					{
						Score: 1.0,
						Point: storage.Point{
							ID: "sym-1",
							Payload: map[string]interface{}{
								"name": "MyFunction",
								"type": "function",
								"relations": []interface{}{
									map[string]interface{}{"target_name": "CalledFunc", "type": "calls"},
								},
							},
						},
					},
				}, nil
			}

			resJSON, err := tool.Execute(ctx, map[string]interface{}{
				"symbol_name": "MyFunction",
				"direction":   "outgoing",
				"file_path":   "main.go",
			})
			Expect(err).NotTo(HaveOccurred())

			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal("success"))

			// Data is the root node
			data := resp.Data.(map[string]interface{})
			Expect(data["name"]).To(Equal("MyFunction"))

			children := data["children"].([]interface{})
			Expect(children).To(HaveLen(1))

			child := children[0].(map[string]interface{})
			Expect(child["name"]).To(Equal("CalledFunc"))
		})
	})
})
