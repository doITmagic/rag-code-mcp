package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
	"github.com/doITmagic/rag-code-mcp/pkg/parser"
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
			// Enough ordinary lines that the file does not count as minified.
			rest := strings.Repeat("// an ordinary comment line\n", 200)
			Expect(os.WriteFile(goFile, []byte("package blob\n\nvar Blob = \""+long+"\"\n\nfunc Hello() {\n\tprintln(1)\n}\n"+rest), 0o600)).To(Succeed())
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

		// Parsing a minified bundle whole took minutes and gigabytes, and held
		// the JavaScript analyzer's lock for every other request meanwhile.
		It("should read a minified bundle as plain lines without parsing it", func() {
			var bundle strings.Builder
			for i := 0; i < 200; i++ {
				fmt.Fprintf(&bundle, "function bundled%d(a,b){return a+b}", i)
			}
			path := filepath.Join(GinkgoT().TempDir(), "app.js")
			Expect(os.WriteFile(path, []byte(bundle.String()), 0o600)).To(Succeed())

			resJSON, err := tool.Execute(ctx, map[string]interface{}{"file_path": path, "line_number": float64(1)})
			Expect(err).NotTo(HaveOccurred())
			var resp struct {
				Status string                  `json:"status"`
				Error  string                  `json:"error"`
				Data   tools.CodeContextResult `json:"data"`
			}
			Expect(json.Unmarshal([]byte(resJSON), &resp)).To(Succeed())
			Expect(resp.Status).To(Equal("success"), resp.Error)
			Expect(resp.Data.ContextType).To(Equal("naive"))
			Expect(resp.Data.CodeSnippet).To(ContainSubstring("bundled199"))
		})

		// Every read of a 4 MB source file parsed it whole, about 15 s and
		// 1.2 GB, and a few parallel reads got the daemon OOM-killed.
		It("should read a source file over the AST size cap as plain lines", func() {
			old := parser.MaxASTFileSize
			parser.MaxASTFileSize = 1 << 10
			DeferCleanup(func() { parser.MaxASTFileSize = old })
			var src strings.Builder
			for i := 0; i < 100; i++ {
				fmt.Fprintf(&src, "def big_function_%d(a, b):\n    return a + b\n\n", i)
			}
			path := filepath.Join(GinkgoT().TempDir(), "big.py")
			Expect(os.WriteFile(path, []byte(src.String()), 0o600)).To(Succeed())

			resJSON, err := tool.Execute(ctx, map[string]interface{}{"file_path": path, "line_number": float64(151)})
			Expect(err).NotTo(HaveOccurred())
			var resp struct {
				Status string                  `json:"status"`
				Error  string                  `json:"error"`
				Data   tools.CodeContextResult `json:"data"`
			}
			Expect(json.Unmarshal([]byte(resJSON), &resp)).To(Succeed())
			Expect(resp.Status).To(Equal("success"), resp.Error)
			Expect(resp.Data.ContextType).To(Equal("naive"))
			Expect(resp.Data.CodeSnippet).To(ContainSubstring("big_function_50"))
		})

		// The AST snippet printed the parser's chunk text numbered from the
		// symbol's first line. For an arrow-function constant that text drops
		// "export const" and the ";", and shifts every line after a first
		// declarator, so the numbered lines did not match the file.
		It("should number the AST snippet with the file's own lines", func() {
			dir := GinkgoT().TempDir()
			for name, src := range map[string]string{
				"merge.ts": "import x from './x';\n\nexport const replaceOption = <T>(value: T): T => {\n\treturn value;\n};\n",
				"multi.ts": "export const LIMIT = 10,\n\tclampValue = (value: number): number => {\n\t\tconst top = LIMIT;\n\t\treturn Math.min(value, top);\n\t};\n",
			} {
				path := filepath.Join(dir, name)
				Expect(os.WriteFile(path, []byte(src), 0o600)).To(Succeed())
				fileLines := strings.Split(src, "\n")

				resJSON, err := tool.Execute(ctx, map[string]interface{}{"file_path": path, "line_number": float64(4)})
				Expect(err).NotTo(HaveOccurred())
				var resp struct {
					Status string                  `json:"status"`
					Error  string                  `json:"error"`
					Data   tools.CodeContextResult `json:"data"`
				}
				Expect(json.Unmarshal([]byte(resJSON), &resp)).To(Succeed())
				Expect(resp.Status).To(Equal("success"), resp.Error)
				Expect(resp.Data.ContextType).To(Equal("ast"), name)
				Expect(resp.Data.CodeSnippet).To(ContainSubstring("   4 -> ┃ "+fileLines[3]+"\n"), name)
				for _, line := range strings.Split(strings.TrimSuffix(resp.Data.CodeSnippet, "\n"), "\n") {
					var n int
					_, err := fmt.Sscanf(line, "%d", &n)
					Expect(err).NotTo(HaveOccurred(), line)
					Expect(line[len("   1 -> ┃ "):]).To(Equal(fileLines[n-1]), name)
				}
			}
		})

		// A one-line file over the size cap (a 10 MB bundle) failed with
		// "token too long", and a long line went whole into the response.
		It("should read a single line longer than the file size cap", func() {
			old := parser.MaxFileSize
			parser.MaxFileSize = 64 << 10
			DeferCleanup(func() { parser.MaxFileSize = old })
			path := filepath.Join(GinkgoT().TempDir(), "bundle.txt")
			Expect(os.WriteFile(path, []byte("start"+strings.Repeat("x", 100<<10)+"\n"), 0o600)).To(Succeed())

			resJSON, err := tool.Execute(ctx, map[string]interface{}{"file_path": path, "line_number": float64(1)})
			Expect(err).NotTo(HaveOccurred())
			var resp struct {
				Status string                  `json:"status"`
				Error  string                  `json:"error"`
				Data   tools.CodeContextResult `json:"data"`
			}
			Expect(json.Unmarshal([]byte(resJSON), &resp)).To(Succeed())
			Expect(resp.Status).To(Equal("success"), resp.Error)
			Expect(resp.Data.CodeSnippet).To(ContainSubstring("start"))
			Expect(resp.Data.CodeSnippet).To(ContainSubstring("line truncated"))
			Expect(len(resp.Data.CodeSnippet)).To(BeNumerically("<", 32<<10))
		})

		// The truncation note counted the line terminator as cut content, so a
		// line of exactly 16 KiB was marked "1 more bytes" with nothing cut.
		It("should count only the cut content in the truncation note", func() {
			const keep = 16 << 10
			path := filepath.Join(GinkgoT().TempDir(), "long.txt")
			content := strings.Repeat("x", keep) + "\n" + strings.Repeat("y", keep+3) + "\n" + strings.Repeat("z", keep+2) + "\r\n"
			Expect(os.WriteFile(path, []byte(content), 0o600)).To(Succeed())

			resJSON, err := tool.Execute(ctx, map[string]interface{}{"file_path": path, "line_number": float64(1)})
			Expect(err).NotTo(HaveOccurred())
			var resp struct {
				Status string                  `json:"status"`
				Error  string                  `json:"error"`
				Data   tools.CodeContextResult `json:"data"`
			}
			Expect(json.Unmarshal([]byte(resJSON), &resp)).To(Succeed())
			Expect(resp.Status).To(Equal("success"), resp.Error)
			lines := strings.Split(resp.Data.CodeSnippet, "\n")
			Expect(lines[0]).NotTo(ContainSubstring("line truncated"))
			Expect(lines[1]).To(HaveSuffix("[line truncated, 3 more bytes]"))
			Expect(lines[2]).To(HaveSuffix("[line truncated, 2 more bytes]"))
		})
	})
})
