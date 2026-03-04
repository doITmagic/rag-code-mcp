package tests

import (
	"context"
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
)

var _ = Describe("GetFileContextTool", func() {
	var (
		mockStore *mockVectorStore
		tool      *tools.ReadFileContextTool
		ctx       context.Context
	)

	BeforeEach(func() {
		mockStore = &mockVectorStore{}
		eng := setupTestEngine(mockStore)
		tool = tools.NewReadFileContextTool(eng)
		ctx = context.Background()
	})

	Describe("Execute", func() {
		It("should fail if file_path is missing", func() {
			resJSON, err := tool.Execute(ctx, map[string]interface{}{})
			Expect(err).NotTo(HaveOccurred())
			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal("error"))
			Expect(resp.Error).To(ContainSubstring("file_path parameter is required"))
		})

		It("should report error if file does not exist", func() {
			resJSON, err := tool.Execute(ctx, map[string]interface{}{"file_path": "/non/existent/file.go"})
			Expect(err).NotTo(HaveOccurred())
			var resp tools.ToolResponse
			Expect(json.Unmarshal([]byte(resJSON), &resp)).NotTo(HaveOccurred())
			Expect(resp.Status).To(Equal("error"))
			Expect(resp.Error).To(ContainSubstring("file not found"))
		})
	})
})
