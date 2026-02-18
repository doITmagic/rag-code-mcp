package healthcheck

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMissingRequiredModels(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"qwen2.5-coder:0.5b"},{"name":"qwen3-embedding:0.6b"}]}`))
	}))
	defer ts.Close()

	missing, err := MissingRequiredModels(ts.URL, []string{"qwen2.5-coder:0.5b", "qwen3-embedding:4b"})
	if err != nil {
		t.Fatalf("MissingRequiredModels returned error: %v", err)
	}
	if len(missing) != 1 || missing[0] != "qwen3-embedding:4b" {
		t.Fatalf("unexpected missing models: %v", missing)
	}
}

func TestPullModelSuccessAndError(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/pull" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = fmt.Fprintln(w, `{"status":"downloading"}`)
			_, _ = fmt.Fprintln(w, `{"status":"success"}`)
		}))
		defer ts.Close()

		if err := PullModel(ts.URL, "qwen2.5-coder:7b"); err != nil {
			t.Fatalf("PullModel should succeed, got error: %v", err)
		}
	})

	t.Run("model_not_found", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/pull" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "application/x-ndjson")
			_, _ = fmt.Fprintln(w, `{"error":"model not found"}`)
		}))
		defer ts.Close()

		err := PullModel(ts.URL, "does-not-exist:999")
		if err == nil {
			t.Fatalf("expected error for missing model")
		}
	})
}
