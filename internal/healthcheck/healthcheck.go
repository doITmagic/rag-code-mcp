package healthcheck

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/doITmagic/rag-code-mcp/internal/config"
)

// CheckResult represents the result of a health check
type CheckResult struct {
	Service string
	Status  string
	Message string
	Error   error
}

// OllamaModel represents basic item in tags response
type OllamaModel struct {
	Name string `json:"name"`
}

// OllamaTagsResponse represents response from /api/tags
type OllamaTagsResponse struct {
	Models []OllamaModel `json:"models"`
}

func normalizeModelName(name string) (string, string) {
	if !strings.Contains(name, ":") {
		return name, "latest"
	}
	parts := strings.SplitN(name, ":", 2)
	return parts[0], parts[1]
}

func fetchInstalledModels(baseURL string) ([]OllamaModel, error) {
	baseURL = resolveOllamaBaseURL(baseURL)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama returned status %d", resp.StatusCode)
	}

	var tags OllamaTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		return nil, err
	}

	return tags.Models, nil
}

// MissingRequiredModels returns required models that are not installed in Ollama.
func MissingRequiredModels(baseURL string, requiredModels []string) ([]string, error) {
	if len(requiredModels) == 0 {
		return nil, nil
	}

	models, err := fetchInstalledModels(baseURL)
	if err != nil {
		return nil, err
	}

	missing := make([]string, 0)
	for _, requiredModel := range requiredModels {
		requiredModel = strings.TrimSpace(requiredModel)
		if requiredModel == "" {
			continue
		}

		reqBase, reqTag := normalizeModelName(requiredModel)
		found := false

		for _, m := range models {
			mBase, mTag := normalizeModelName(m.Name)
			if reqBase == mBase && reqTag == mTag {
				found = true
				break
			}
		}

		if !found {
			missing = append(missing, requiredModel)
		}
	}

	return missing, nil
}

// PullModel downloads a model in Ollama. Returns error when model cannot be pulled.
func PullModel(baseURL, name string) error {
	baseURL = resolveOllamaBaseURL(baseURL)
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("model name is required")
	}

	body, err := json.Marshal(map[string]string{"name": name})
	if err != nil {
		return err
	}

	resp, err := http.Post(baseURL+"/api/pull", "application/json", bytes.NewBuffer(body))
	if err != nil {
		return fmt.Errorf("failed to pull model '%s': %w", name, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to pull model '%s': ollama returned status %d", name, resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 0, 1024)
	scanner.Buffer(buf, 1024*1024)
	lastLine := ""

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		lastLine = line

		var chunk map[string]interface{}
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			continue
		}

		if errMsg, ok := chunk["error"].(string); ok && strings.TrimSpace(errMsg) != "" {
			return fmt.Errorf("failed to pull model '%s': %s", name, errMsg)
		}

		if status, ok := chunk["status"].(string); ok && status == "success" {
			return nil
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to pull model '%s': %w", name, err)
	}

	if lastLine != "" {
		return fmt.Errorf("failed to pull model '%s': %s", name, lastLine)
	}

	return fmt.Errorf("failed to pull model '%s': pull did not report success", name)
}

// CheckOllama verifies Ollama is running and accessible
func CheckOllama(baseURL string) CheckResult {
	return CheckOllamaWithModels(baseURL, nil)
}

// CheckOllamaWithModels verifies Ollama and checks for required models
func CheckOllamaWithModels(baseURL string, requiredModels []string) CheckResult {
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

	if resp.StatusCode != http.StatusOK {
		result.Status = "error"
		result.Message = fmt.Sprintf("Ollama returned status %d", resp.StatusCode)
		return result
	}

	// If we have required models, check them
	if len(requiredModels) > 0 {
		var tags OllamaTagsResponse
		if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
			result.Status = "error"
			result.Message = "Failed to parse Ollama models response"
			return result
		}

		var missing []string
		for _, requiredModel := range requiredModels {
			reqBase, reqTag := normalizeModelName(requiredModel)
			found := false

			for _, m := range tags.Models {
				mBase, mTag := normalizeModelName(m.Name)
				if reqBase == mBase && reqTag == mTag {
					found = true
					break
				}
			}

			if !found {
				missing = append(missing, requiredModel)
			}
		}

		if len(missing) > 0 {
			result.Status = "error"
			result.Message = fmt.Sprintf("Missing models: %s (Ollama implicit :latest tag might cause mismatch if you have specific versions installed)", strings.Join(missing, ", "))
			return result
		}

		// Success: include verified models in message
		result.Status = "ok"
		result.Message = fmt.Sprintf("Connected to Ollama at %s (Verified: %s)", baseURL, strings.Join(requiredModels, ", "))
		return result
	}

	result.Status = "ok"
	result.Message = fmt.Sprintf("Connected to Ollama at %s", baseURL)
	return result
}

// CheckQdrant verifies Qdrant is running and accessible
func CheckQdrant(url string) CheckResult {
	result := CheckResult{
		Service: "Qdrant",
		Status:  "unknown",
	}

	url = resolveQdrantURL(url)

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

// CheckAllWithModels runs all health checks including configured models
func CheckAllWithModels(ollamaURL, qdrantURL string, requiredModels []string) []CheckResult {
	return []CheckResult{
		CheckOllamaWithModels(ollamaURL, requiredModels),
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
    ollama pull mxbai-embed-large
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

func resolveOllamaBaseURL(value string) string {
	if strings.TrimSpace(value) == "" {
		return config.DefaultOllamaBaseURL
	}
	return value
}

func resolveQdrantURL(value string) string {
	if strings.TrimSpace(value) == "" {
		return config.DefaultQdrantURL
	}
	return value
}
