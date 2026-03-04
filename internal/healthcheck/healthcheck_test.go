package healthcheck

import (
	"fmt"
	"testing"
)

func BenchmarkFormatResults(b *testing.B) {
	results := make([]CheckResult, 100)
	for i := 0; i < 100; i++ {
		results[i] = CheckResult{
			Service: fmt.Sprintf("Service-%d", i),
			Status:  "ok",
			Message: "Service is running fine",
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		FormatResults(results)
	}
}

func BenchmarkGetRemediation(b *testing.B) {
	results := make([]CheckResult, 100)
	for i := 0; i < 100; i++ {
		service := "Ollama"
		if i%2 == 0 {
			service = "Qdrant"
		}
		results[i] = CheckResult{
			Service: service,
			Status:  "error",
			Message: "Service is down",
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		GetRemediation(results)
	}
}

func TestFormatResults(t *testing.T) {
	results := []CheckResult{
		{Service: "Ollama", Status: "ok", Message: "Connected"},
		{Service: "Qdrant", Status: "error", Message: "Failed"},
	}
	output := FormatResults(results)
	expected := "\n=== Dependency Health Check ===\n\n✓ Ollama: Connected\n✗ Qdrant: Failed\n"
	if output != expected {
		t.Errorf("expected %q, got %q", expected, output)
	}
}

func TestGetRemediation(t *testing.T) {
	results := []CheckResult{
		{Service: "Ollama", Status: "error", Message: "Failed"},
	}
	output := GetRemediation(results)
	if output == "" {
		t.Error("expected remediation output, got empty string")
	}
}
