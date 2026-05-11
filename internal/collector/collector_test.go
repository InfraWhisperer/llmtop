package collector

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

func TestCollectorConcurrentAddRemove(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, `vllm:num_requests_running{model_name="test"} 1`)
	}))
	defer ts.Close()

	configs := []WorkerConfig{
		{Endpoint: ts.URL, Backend: metrics.BackendVLLM},
	}
	c := New(configs, time.Second)
	ctx := context.Background()

	var wg sync.WaitGroup

	// Goroutine 1: poll repeatedly
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 10 {
			c.PollNow(ctx)
		}
	}()

	// Goroutine 2: add/remove workers
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 10 {
			ep := fmt.Sprintf("http://localhost:%d", 9000+i)
			c.AddWorker(WorkerConfig{Endpoint: ep, Backend: metrics.BackendVLLM})
			c.RemoveWorker(ep)
		}
	}()

	// Goroutine 3: read all
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 10 {
			_ = c.GetAll()
			_ = c.Endpoints()
		}
	}()

	wg.Wait()
}

func TestCollectorGetAll(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintln(w, `vllm:num_requests_running{model_name="llama"} 3`)
		_, _ = fmt.Fprintln(w, `vllm:num_requests_waiting{model_name="llama"} 1`)
	}))
	defer ts.Close()

	configs := []WorkerConfig{
		{Endpoint: ts.URL, Backend: metrics.BackendVLLM},
	}
	c := New(configs, time.Second)
	c.PollNow(context.Background())

	all := c.GetAll()
	if len(all) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(all))
	}
	w := all[0]
	if w.Endpoint != ts.URL {
		t.Errorf("expected endpoint %s, got %s", ts.URL, w.Endpoint)
	}
	if !w.Online {
		t.Error("expected worker to be online after successful poll")
	}
	if w.Backend != metrics.BackendVLLM {
		t.Errorf("expected BackendVLLM, got %s", w.Backend)
	}
	if w.RequestsRunning != 3 {
		t.Errorf("expected RequestsRunning=3, got %d", w.RequestsRunning)
	}
}
