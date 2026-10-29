package tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

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

		// bufio.Scanner's default 64 KB token limit made every read whose
		// window reached a longer line fail with "token too long".
		It("should read files with a line longer than 64 KB", func() {
			dir := GinkgoT().TempDir()
			long := strings.Repeat("x", 70_000)
			goFile := filepath.Join(dir, "blob.go")
			Expect(os.WriteFile(goFile, []byte("package blob\n\nvar Blob = \""+long+"\"\n\nfunc Hello() {\n\tprintln(1)\n}\n"), 0o600)).To(Succeed())
			txtFile := filepath.Join(dir, "notes.txt")
			Expect(os.WriteFile(txtFile, []byte("one\ntwo\n"+long+"\nfour\nfive\n"), 0o600)).To(Succeed())

			for _, tc := range []struct {
				path, want, contextType string
				line                    float64
			}{
				{goFile, "func Hello", "ast", 5},
				{txtFile, "five", "naive", 5},
			} {
				resJSON, err := tool.Execute(ctx, map[string]interface{}{"file_path": tc.path, "line_number": tc.line})
				Expect(err).NotTo(HaveOccurred())
				var resp struct {
					Status string                  `json:"status"`
					Error  string                  `json:"error"`
					Data   tools.CodeContextResult `json:"data"`
				}
				Expect(json.Unmarshal([]byte(resJSON), &resp)).To(Succeed())
				Expect(resp.Status).To(Equal("success"), resp.Error)
				Expect(resp.Data.ContextType).To(Equal(tc.contextType))
				Expect(resp.Data.CodeSnippet).To(ContainSubstring(tc.want))
			}
		})
	})
})
