package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// Ollama answers only after a few failed polls, as when it is still
// initialising CUDA; the wait must ride that out instead of giving up.
func TestWaitForOllamaWaitsUntilReady(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	if err := waitForOllama(srv.URL, 10*time.Second); err != nil {
		t.Fatal(err)
	}
	if calls.Load() < 3 {
		t.Fatalf("returned before Ollama was ready (%d calls)", calls.Load())
	}
}

func TestWaitForOllamaTimesOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if err := waitForOllama(srv.URL, time.Second); err == nil {
		t.Fatal("expected a timeout error")
	}
}
