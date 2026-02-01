package healthcheck

import (
"context"
"fmt"
"net/http"
"time"
)

// CheckResult represents the result of a health check
type CheckResult struct {
Service string
Status  string
Message string
Error   error
}

// CheckOllama verifies Ollama is running and accessible
func CheckOllama(baseURL string) CheckResult {
result := CheckResult{
Service: "Ollama",
Status:  "unknown",
}

if baseURL == "" {
baseURL = "http://localhost:11434"
}

ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/tags", nil)
if err != nil {
result.Status = "error"
result.Error = err
result.Message = fmt.Sprintf("Failed to create request: %v", err)
return result
}

client := &http.Client{Timeout: 5 * time.Second}
resp, err := client.Do(req)
if err != nil {
result.Status = "error"
result.Error = err
result.Message = fmt.Sprintf("Cannot connect to Ollama at %s", baseURL)
return result
}
defer resp.Body.Close()

if resp.StatusCode == http.StatusOK {
result.Status = "ok"
result.Message = fmt.Sprintf("Connected to Ollama at %s", baseURL)
} else {
result.Status = "error"
result.Message = fmt.Sprintf("Ollama returned status %d", resp.StatusCode)
}

return result
}

// CheckQdrant verifies Qdrant is running and accessible
func CheckQdrant(url string) CheckResult {
result := CheckResult{
Service: "Qdrant",
Status:  "unknown",
}

if url == "" {
url = "http://localhost:6333"
}

ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()

req, err := http.NewRequestWithContext(ctx, "GET", url+"/readyz", nil)
if err != nil {
result.Status = "error"
result.Error = err
result.Message = fmt.Sprintf("Failed to create request: %v", err)
return result
}

client := &http.Client{Timeout: 5 * time.Second}
resp, err := client.Do(req)
if err != nil {
result.Status = "error"
result.Error = err
result.Message = fmt.Sprintf("Cannot connect to Qdrant at %s", url)
return result
}
defer resp.Body.Close()

if resp.StatusCode == http.StatusOK {
result.Status = "ok"
result.Message = fmt.Sprintf("Connected to Qdrant at %s", url)
} else {
result.Status = "error"
result.Message = fmt.Sprintf("Qdrant returned status %d", resp.StatusCode)
}

return result
}

// CheckAll runs all health checks and returns results
func CheckAll(ollamaURL, qdrantURL string) []CheckResult {
return []CheckResult{
CheckOllama(ollamaURL),
CheckQdrant(qdrantURL),
}
}

// FormatResults formats health check results for display
func FormatResults(results []CheckResult) string {
output := "\n=== Dependency Health Check ===\n\n"

for _, result := range results {
var status string
switch result.Status {
case "ok":
status = "✓"
case "error":
status = "✗"
default:
status = "?"
}

output += fmt.Sprintf("%s %s: %s\n", status, result.Service, result.Message)
}

return output
}

// GetRemediation provides remediation steps for failed checks
func GetRemediation(results []CheckResult) string {
var remediation string

for _, result := range results {
if result.Status != "ok" {
remediation += fmt.Sprintf("\n%s is not accessible:\n", result.Service)

switch result.Service {
case "Ollama":
remediation += `
  Install Ollama:
    curl -fsSL https://ollama.ai/install.sh | sh

  Start Ollama (it usually starts automatically):
    ollama serve

  Pull required models:
    ollama pull nomic-embed-text
    ollama pull phi3:medium
`
case "Qdrant":
remediation += `
  Start Qdrant with Docker:
    docker run -d -p 6333:6333 \
      -v $(pwd)/qdrant_data:/qdrant/storage \
      qdrant/qdrant

  Or use docker-compose:
    docker compose up -d qdrant
`
}
}
}

return remediation
}
