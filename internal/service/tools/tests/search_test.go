package tests

import (
	"context"
	"encoding/json"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/storage"
)

var _ = Describe("RagSearchCodeTool", func() {
	var (
		mockStore *mockVectorStore
		tool      *tools.SearchLocalIndexTool
		ctx       context.Context
	)

	BeforeEach(func() {
		mockStore = &mockVectorStore{
			CollectionExistsFunc: func(ctx context.Context, name string) (bool, error) {
				return true, nil
			},
		}
		eng := setupTestEngine(mockStore)
		tool = tools.NewSearchLocalIndexTool(eng)
		ctx = context.Background()
	})

	Describe("Executing search", func() {
		Context("Basic Parameter Validation", func() {
			It("should fail if query is missing", func() {
				_, err := tool.Execute(ctx, map[string]interface{}{})
				Expect(err).To(HaveOccurred())
				Expect(err.Error()).To(ContainSubstring("query parameter is required"))
			})

			It("should default to discovery mode if mode is not provided", func() {
				mockStore.SearchCodeOnlyFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
					return []storage.SearchResult{}, nil
				}
				resJSON, err := tool.Execute(ctx, map[string]interface{}{"query": "test", "file_path": "main.go"})
				Expect(err).NotTo(HaveOccurred())
				var resp tools.ToolResponse
				json.Unmarshal([]byte(resJSON), &resp)
				Expect(resp.Status).To(Equal("no_results"))
			})
		})

		Context("Engine Failures", func() {
			It("should report error if vector store fails", func() {
				mockStore.SearchCodeOnlyFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
					return nil, errors.New("database down")
				}
				resJSON, err := tool.Execute(ctx, map[string]interface{}{"query": "test", "file_path": "main.go"})
				Expect(err).NotTo(HaveOccurred())
				var resp tools.ToolResponse
				json.Unmarshal([]byte(resJSON), &resp)
				Expect(resp.Status).To(Equal("error"))
				Expect(resp.Error).To(ContainSubstring("search failed"))
			})
		})

		Context("Successful Discovery Search", func() {
			BeforeEach(func() {
				mockStore.SearchCodeOnlyFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
					return []storage.SearchResult{
						{
							Score: 0.9,
							Point: storage.Point{
								ID: "1",
								Payload: map[string]interface{}{
									"Name":      "MyFunc",
									"content":   "func MyFunc() {}",
									"file_path": "/mock/file.go",
								},
							},
						},
					}, nil
				}
			})

			It("should return success and properly mapped data", func() {
				resJSON, err := tool.Execute(ctx, map[string]interface{}{"query": "find func", "file_path": "main.go"})
				Expect(err).NotTo(HaveOccurred())
				var resp tools.ToolResponse
				json.Unmarshal([]byte(resJSON), &resp)
				Expect(resp.Status).To(Equal("success"), "Error: "+resp.Error)

				// Verify Data list
				Expect(resp.Data).NotTo(BeNil())
				data := resp.Data.([]interface{})
				Expect(data).To(HaveLen(1))
				item := data[0].(map[string]interface{})
				Expect(item["Name"]).To(Equal("MyFunc"))
				Expect(item["score"]).To(Equal(0.9))
			})
		})

		Context("Graph Context Expansion", func() {
			It("should auto-fetch dependencies if relations exist", func() {
				mockStore.SearchCodeOnlyFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
					// First call: main result with relations
					if q.Limit == 10 {
						return []storage.SearchResult{
							{
								Score: 1.0,
								Point: storage.Point{
									ID: "root",
									Payload: map[string]interface{}{
										"Name": "Main",
										"Relations": []interface{}{
											map[string]interface{}{"target_name": "Dependency"},
										},
									},
								},
							},
						}, nil
					}
					// Second call: dependency expansion search
					return []storage.SearchResult{
						{
							Score: 0.8,
							Point: storage.Point{
								ID: "dep-id",
								Payload: map[string]interface{}{
									"Name": "Dependency",
								},
							},
						},
					}, nil
				}

				resJSON, err := tool.Execute(ctx, map[string]interface{}{"query": "test", "file_path": "main.go"})
				Expect(err).NotTo(HaveOccurred())
				var resp tools.ToolResponse
				json.Unmarshal([]byte(resJSON), &resp)

				Expect(resp.Status).To(Equal("success"), "Error: "+resp.Error)
				Expect(resp.Message).To(ContainSubstring("Auto-fetched 1 related dependencies"))
				data := resp.Data.([]interface{})
				Expect(data).To(HaveLen(2)) // root + dep

				foundExp := false
				for _, d := range data {
					m := d.(map[string]interface{})
					if m["_graph_expansion"] != nil {
						foundExp = true
						Expect(m["Name"]).To(Equal("Dependency"))
					}
				}
				Expect(foundExp).To(BeTrue(), "Should have found graph expanded node in data")
			})
		})
	})
})
