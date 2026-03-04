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
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal("error"))
			Expect(resp.Error).To(ContainSubstring("symbol_name parameter is required"))
		})

		It("should return indexing_required if ErrNoCollectionsFound", func() {
			mockStore.CollectionExistsFunc = func(ctx context.Context, name string) (bool, error) {
				return false, nil
			}
			resJSON, err := tool.Execute(ctx, map[string]interface{}{"symbol_name": "GhostFunc", "file_path": "main.go"})
			Expect(err).NotTo(HaveOccurred())
			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())

			// Depending on whether auto-index is started or not, status could be indexing_started or indexing_required
			// FindUsages auto-starts indexing if nothing is found and returns indexing_in_progress
			Expect(resp.Status).To(MatchRegexp("indexing_in_progress|indexing_required"))
		})

		It("should return no_results if no usages are found", func() {
			mockStore.SearchFunc = func(ctx context.Context, col string, q storage.SearchQuery) ([]storage.SearchResult, error) {
				return []storage.SearchResult{}, nil
			}
			resJSON, err := tool.Execute(ctx, map[string]interface{}{"symbol_name": "GhostFunc", "file_path": "main.go"})
			Expect(err).NotTo(HaveOccurred())
			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())

			Expect(resp.Status).To(Equal("success"))
			Expect(resp.Message).To(ContainSubstring("No usages found for symbol 'GhostFunc'"))
		})

		It("should return found usages with markdown formatting", func() {
			mockStore.ExactSearchFunc = func(ctx context.Context, col string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
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
								"relations": []interface{}{
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
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())

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

		It("should send relations[].target_name as filter key to ExactSearch", func() {
			var capturedFilter map[string]interface{}
			mockStore.ExactSearchFunc = func(ctx context.Context, col string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
				capturedFilter = filters
				return []storage.SearchResult{}, nil
			}

			_, _ = tool.Execute(ctx, map[string]interface{}{"symbol_name": "TargetSym", "file_path": "main.go"})

			Expect(capturedFilter).NotTo(BeNil(), "ExactSearch should have been called")
			Expect(capturedFilter["relations[].target_name"]).To(Equal("TargetSym"))
		})

		It("should merge usages from multiple language collections (polyglot)", func() {
			mockStore.ExactSearchFunc = func(ctx context.Context, col string, filters map[string]interface{}, limit int) ([]storage.SearchResult, error) {
				if strings.HasSuffix(col, "-go") {
					return []storage.SearchResult{
						{Score: 1.0, Point: storage.Point{ID: "go-caller", Payload: map[string]interface{}{
							"name": "GoCallerFunc", "type": "function", "file_path": "a.go",
							"relations": []interface{}{map[string]interface{}{"target_name": "SharedSym", "type": "calls"}},
						}}},
					}, nil
				}
				if strings.HasSuffix(col, "-python") {
					return []storage.SearchResult{
						{Score: 1.0, Point: storage.Point{ID: "py-caller", Payload: map[string]interface{}{
							"name": "py_caller_func", "type": "function", "file_path": "b.py",
							"relations": []interface{}{map[string]interface{}{"target_name": "SharedSym", "type": "calls"}},
						}}},
					}, nil
				}
				return []storage.SearchResult{}, nil
			}

			resJSON, err := tool.Execute(ctx, map[string]interface{}{"symbol_name": "SharedSym", "file_path": "main.go"})
			Expect(err).NotTo(HaveOccurred())

			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal("success"))

			data := resp.Data.([]interface{})
			Expect(data).To(HaveLen(2), "Expected usages from both go and python collections")

			names := map[string]bool{}
			for _, d := range data {
				m := d.(map[string]interface{})
				names[m["name"].(string)] = true
			}
			Expect(names["GoCallerFunc"]).To(BeTrue())
			Expect(names["py_caller_func"]).To(BeTrue())
		})
	})
})
