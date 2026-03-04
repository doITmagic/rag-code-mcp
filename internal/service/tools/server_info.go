package tools

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var (
	// Preluăm valorile injectate la build, dar le facem globale pentru tool
	serverVersion string
	serverCommit  string
	serverDate    string
	serverStart   time.Time
)

func init() {
	serverStart = time.Now()
}

// SetServerBuildInfo permite main.go să injecteze datele de build în tool
func SetServerBuildInfo(version, commit, date string) {
	serverVersion = version
	serverCommit = commit
	serverDate = date
}

// ServerInfoTool implements the rag_server_info MCP tool.
type ServerInfoTool struct{}

// NewServerInfoTool creates a new server info tool.
func NewServerInfoTool() *ServerInfoTool {
	return &ServerInfoTool{}
}

func (t *ServerInfoTool) Name() string { return "rag_server_info" }
func (t *ServerInfoTool) Description() string {
	return "Returns diagnostic information about the currently running RagCode MCP server process, including PID, executable path, build time, and uptime. Use this to verify if you are talking to the newly compiled binary."
}

type ServerInfoInput struct{}

func (t *ServerInfoTool) Register(server *mcp.Server) {
	mcp.AddTool(server, &mcp.Tool{
		Name:        t.Name(),
		Description: t.Description(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, input ServerInfoInput) (*mcp.CallToolResult, any, error) {
		_ = input
		result, err := t.Execute(ctx, map[string]interface{}{})
		if err != nil {
			res := &mcp.CallToolResult{}
			res.SetError(err)
			return res, nil, nil
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: result}},
		}, nil, nil
	})
}

func (t *ServerInfoTool) Execute(ctx context.Context, _ map[string]interface{}) (string, error) {
	exePath, _ := os.Executable()
	var binModTime string
	if stat, err := os.Stat(exePath); err == nil {
		binModTime = stat.ModTime().Format(time.RFC3339)
	} else {
		binModTime = "unknown"
	}

	data := map[string]interface{}{
		"pid":                os.Getpid(),
		"executable_path":    exePath,
		"binary_modified_at": binModTime,
		"started_at":         serverStart.Format(time.RFC3339),
		"uptime":             time.Since(serverStart).String(),
		"go_version":         runtime.Version(),
		"build_info": map[string]string{
			"version": serverVersion,
			"commit":  serverCommit,
			"date":    serverDate,
		},
	}

	response := ToolResponse{
		Status:  "success",
		Message: fmt.Sprintf("RagCode MCP Server is running (PID: %d, Uptime: %s)", os.Getpid(), time.Since(serverStart).Round(time.Second)),
		Data:    data,
	}

	return response.JSON()
}
