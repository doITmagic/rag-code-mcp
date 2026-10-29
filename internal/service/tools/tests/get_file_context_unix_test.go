//go:build unix

package tests

import (
	"context"
	"encoding/json"
	"path/filepath"
	"syscall"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/doITmagic/rag-code-mcp/internal/service/tools"
)

var _ = Describe("GetFileContextTool on special files", func() {
	// Opening a FIFO with no writer blocks forever, so the request never
	// answered and its goroutine leaked.
	It("should refuse a FIFO instead of blocking on it", func() {
		path := filepath.Join(GinkgoT().TempDir(), "fifo.go")
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			Skip("mkfifo unavailable: " + err.Error())
		}
		tool := tools.NewReadFileContextTool(setupTestEngine(&mockVectorStore{}))

		done := make(chan string, 1)
		go func() {
			resJSON, _ := tool.Execute(context.Background(), map[string]interface{}{"file_path": path, "line_number": float64(1)})
			done <- resJSON
		}()
		var resJSON string
		Eventually(done, 5*time.Second).Should(Receive(&resJSON), "read blocked on a FIFO")
		var resp tools.ToolResponse
		Expect(json.Unmarshal([]byte(resJSON), &resp)).To(Succeed())
		Expect(resp.Status).To(Equal("error"))
		Expect(resp.Error).To(ContainSubstring("not a regular file"))
	})
})
