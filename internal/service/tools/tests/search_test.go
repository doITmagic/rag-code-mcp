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
				Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
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
				Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
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
				Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
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

		Context("Graph Context Expansion - ExactSearch First (Phase 3)", func() {
			It("should use ExactSearch (no embedding) when dependency is found by name", func() {
				// Track SearchCodeOnly calls with limit=2 (expansion fallback limit).
				// If ExactSearch succeeds for the dependency, limit=2 calls should be 0.
				expansionFallbackCalls := 0

				mockStore.SearchCodeOnlyFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
					if q.Limit == 2 {
						expansionFallbackCalls++
					}
					// Main query — returns root with relations
					return []storage.SearchResult{
						{
							Score: 1.0,
							Point: storage.Point{
								ID: "root-id",
								Payload: map[string]interface{}{
									"name": "MainFunc",
									"relations": []interface{}{
										map[string]interface{}{"target_name": "HelperFunc"},
									},
								},
							},
						},
					}, nil
				}

				// ExactSearch returns the dependency directly (SearchByName path)
				mockStore.ExactSearchFunc = func(ctx context.Context, col string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
					if filters["name"] == "HelperFunc" {
						return []storage.SearchResult{
							{
								Score: 1.0,
								Point: storage.Point{
									ID:      "helper-id",
									Payload: map[string]interface{}{"name": "HelperFunc"},
								},
							},
						}, nil
					}
					return []storage.SearchResult{}, nil
				}

				resJSON, err := tool.Execute(ctx, map[string]interface{}{"query": "test", "file_path": "main.go"})
				Expect(err).NotTo(HaveOccurred())

				var resp tools.ToolResponse
				Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
				Expect(resp.Status).To(Equal("success"), "Error: "+resp.Error)
				Expect(resp.Message).To(ContainSubstring("Auto-fetched 1 related dependencies"))

				data := resp.Data.([]interface{})
				Expect(data).To(HaveLen(2)) // root + helper

				// No fallback embedding for expansion when ExactSearch found the dependency
				Expect(expansionFallbackCalls).To(Equal(0),
					"SearchCodeOnly with expansion limit should not be called when ExactSearch succeeds")
			})

			It("should NOT fall back to embedding when ExactSearch finds nothing for a dependency", func() {
				// Regression test: graph expansion must NOT trigger embedding calls for
				// relations not found in the local index (stdlib/external symbols).
				// Each fallback embedding call costs ~N seconds serialized through Ollama.
				embeddingCallsForExpansion := 0

				mockStore.SearchCodeOnlyFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
					if q.Limit == 2 {
						// This would be a fallback expansion embedding — must NOT be called.
						embeddingCallsForExpansion++
					}
					// Main query result — has a relation to an external/stdlib symbol
					return []storage.SearchResult{
						{
							Score: 1.0,
							Point: storage.Point{
								ID: "root-id",
								Payload: map[string]interface{}{
									"name": "MainFunc",
									"relations": []interface{}{
										map[string]interface{}{"target_name": "ExternalHelper"},
									},
								},
							},
						},
					}, nil
				}

				// ExactSearch finds nothing — external symbol not in index
				mockStore.ExactSearchFunc = func(ctx context.Context, col string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
					return []storage.SearchResult{}, nil
				}

				resJSON, err := tool.Execute(ctx, map[string]interface{}{"query": "test", "file_path": "main.go"})
				Expect(err).NotTo(HaveOccurred())

				var resp tools.ToolResponse
				Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
				Expect(resp.Status).To(Equal("success"), "Error: "+resp.Error)

				// Only root result — dependency was skipped (not in index, no fallback)
				data := resp.Data.([]interface{})
				Expect(data).To(HaveLen(1))

				// No embedding fallback — critical for performance
				Expect(embeddingCallsForExpansion).To(Equal(0),
					"Embedding fallback must NOT be called for graph expansion: external/stdlib symbols not in index would cause N×26s latency")
			})

			It("should deduplicate expansion targets when same dependency appears multiple times in relations", func() {
				mockStore.SearchCodeOnlyFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
					return []storage.SearchResult{
						{
							Score: 1.0,
							Point: storage.Point{
								ID: "root-id",
								Payload: map[string]interface{}{
									"name": "MainFunc",
									"relations": []interface{}{
										map[string]interface{}{"target_name": "Shared"},
										map[string]interface{}{"target_name": "Shared"}, // duplicate
										map[string]interface{}{"target_name": "Shared"}, // duplicate
									},
								},
							},
						},
					}, nil
				}

				searchByNameCalls := 0
				mockStore.ExactSearchFunc = func(ctx context.Context, col string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
					if filters["name"] == "Shared" {
						searchByNameCalls++
						return []storage.SearchResult{
							{Score: 1.0, Point: storage.Point{ID: "shared-id", Payload: map[string]interface{}{"name": "Shared"}}},
						}, nil
					}
					return []storage.SearchResult{}, nil
				}

				resJSON, err := tool.Execute(ctx, map[string]interface{}{"query": "test", "file_path": "main.go"})
				Expect(err).NotTo(HaveOccurred())
				var resp tools.ToolResponse
				Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
				Expect(resp.Status).To(Equal("success"), "Error: "+resp.Error)

				// Despite 3 identical relations, output should have exactly root + 1 Shared
				data := resp.Data.([]interface{})
				Expect(data).To(HaveLen(2), "Expected root + 1 Shared (deduped), not "+resp.Message)

				// seenTargets prevents duplicate goroutines — ExactSearch called per-lang but only
				// for a single goroutine (not 3 goroutines for same name)
				// Verify expansion result is present with _graph_expansion marker
				foundExpansion := false
				for _, d := range data {
					m := d.(map[string]interface{})
					if m["_graph_expansion"] != nil {
						foundExpansion = true
					}
				}
				Expect(foundExpansion).To(BeTrue(), "Should have expansion result in data")
			})
		})
	})
})
