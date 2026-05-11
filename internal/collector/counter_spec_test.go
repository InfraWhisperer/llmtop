// SPDX-License-Identifier: Apache-2.0
// Spec-only contract tests for internal/collector tenant counter delta tracking
// (F3). Source of truth: docs/feature-spec.md F3.
//
// Driven through the public Collector API and a synthetic FetchFunc that
// returns Prometheus-format text — black-box, no impl peek.

package collector_test

import (
	"context"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/collector"
	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// vllmTextWithTags constructs a Prometheus-format vLLM payload with
// per-request_tag counters at the given values.
func vllmTextWithTags(reqs map[string]float64) string {
	out := "# TYPE vllm:num_requests_running gauge\n"
	out += `vllm:num_requests_running{model_name="m"} 1` + "\n"
	out += "# TYPE vllm:gpu_cache_usage_perc gauge\n"
	out += `vllm:gpu_cache_usage_perc{model_name="m"} 0.5` + "\n"
	var sb strings.Builder
	sb.WriteString(out)
	sb.WriteString("# TYPE vllm:request_success_total counter\n")
	for tag, v := range reqs {
		// printf with float repr good enough for the Prometheus parser.
		sb.WriteString("vllm:request_success_total{model_name=\"m\",request_tag=\"")
		sb.WriteString(tag)
		sb.WriteString("\"} ")
		sb.WriteString(formatFloat(v))
		sb.WriteString("\n")
	}
	return sb.String()
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// --- TestCollector_TenantReqs_PopulatedAfterFirstPoll ----------------------

// On the first poll, TenantReqs is populated with the raw counter values.
// (Spec edge case 7 for F3: rate is 0 on first poll; counter stored.)
func TestCollector_TenantReqs_PopulatedAfterFirstPoll(t *testing.T) {
	calls := atomic.Int32{}
	body := vllmTextWithTags(map[string]float64{
		"team-search":  100,
		"team-rag":     50,
		"team-copilot": 25,
	})
	cfg := collector.WorkerConfig{
		Endpoint: "http://fake/poll1",
		Backend:  metrics.BackendVLLM,
		FetchFunc: func(_ context.Context) (string, error) {
			calls.Add(1)
			return body, nil
		},
	}

	c := collector.New([]collector.WorkerConfig{cfg}, 24*time.Hour)
	defer c.Stop()
	c.PollNow(context.Background())

	all := c.GetAll()
	if len(all) != 1 {
		t.Fatalf("expected 1 worker, got %d", len(all))
	}
	w := all[0]
	if w.TenantReqs == nil {
		t.Fatalf("expected TenantReqs populated, got nil")
	}
	for _, tag := range []string{"team-search", "team-rag", "team-copilot"} {
		if _, ok := w.TenantReqs[tag]; !ok {
			t.Fatalf("missing tag %q in TenantReqs: %v", tag, w.TenantReqs)
		}
	}
}

// On the second poll, TenantReqs should reflect updated counter values.
func TestCollector_TenantReqs_UpdatedOnSecondPoll(t *testing.T) {
	pollNum := atomic.Int32{}
	cfg := collector.WorkerConfig{
		Endpoint: "http://fake/poll2",
		Backend:  metrics.BackendVLLM,
		FetchFunc: func(_ context.Context) (string, error) {
			n := pollNum.Add(1)
			val := float64(100 * n) // 100, 200, 300, ...
			return vllmTextWithTags(map[string]float64{"team-search": val}), nil
		},
	}

	c := collector.New([]collector.WorkerConfig{cfg}, 24*time.Hour)
	defer c.Stop()
	c.PollNow(context.Background())
	c.PollNow(context.Background())

	all := c.GetAll()
	w := all[0]
	got, ok := w.TenantReqs["team-search"]
	if !ok {
		t.Fatalf("expected team-search in TenantReqs after 2 polls")
	}
	if got != 200 {
		t.Fatalf("expected counter=200 after 2 polls, got %v", got)
	}
}

// A worker without request_tag samples must produce empty/nil TenantReqs.
func TestCollector_TenantReqs_EmptyForUntaggedWorker(t *testing.T) {
	body := "# TYPE vllm:num_requests_running gauge\n" +
		`vllm:num_requests_running{model_name="m"} 1` + "\n" +
		"# TYPE vllm:gpu_cache_usage_perc gauge\n" +
		`vllm:gpu_cache_usage_perc{model_name="m"} 0.5` + "\n"
	cfg := collector.WorkerConfig{
		Endpoint:  "http://fake/untagged",
		Backend:   metrics.BackendVLLM,
		FetchFunc: func(_ context.Context) (string, error) { return body, nil },
	}
	c := collector.New([]collector.WorkerConfig{cfg}, 24*time.Hour)
	defer c.Stop()
	c.PollNow(context.Background())
	w := c.GetAll()[0]
	if len(w.TenantReqs) != 0 {
		t.Fatalf("expected empty TenantReqs for untagged worker, got %v", w.TenantReqs)
	}
}

// Tag that disappears mid-session: previous tag still tracked, new poll without
// it produces 0 for that tag (or removes it). Spec edge case 3 for F3 says
// "the delta is 0; TokPerSec for that tag becomes 0; it drops out of TopTenants".
// We assert at the WorkerMetrics level that the counter no longer appears or
// its value did not advance (delta 0).
func TestCollector_TenantReqs_DisappearingTagDoesNotIncreaseCounter(t *testing.T) {
	pollNum := atomic.Int32{}
	cfg := collector.WorkerConfig{
		Endpoint: "http://fake/disappearing",
		Backend:  metrics.BackendVLLM,
		FetchFunc: func(_ context.Context) (string, error) {
			n := pollNum.Add(1)
			if n == 1 {
				return vllmTextWithTags(map[string]float64{"team-search": 100, "team-rag": 50}), nil
			}
			return vllmTextWithTags(map[string]float64{"team-search": 150}), nil
		},
	}
	c := collector.New([]collector.WorkerConfig{cfg}, 24*time.Hour)
	defer c.Stop()
	c.PollNow(context.Background())
	c.PollNow(context.Background())

	w := c.GetAll()[0]
	// team-rag may still appear with value 50 (old counter) or be absent.
	// Either is acceptable per spec — but it must not have grown.
	if v, ok := w.TenantReqs["team-rag"]; ok {
		if v > 50 {
			t.Fatalf("disappearing tag team-rag must not advance: got %v", v)
		}
	}
	if w.TenantReqs["team-search"] != 150 {
		t.Fatalf("team-search counter not updated; got %v", w.TenantReqs["team-search"])
	}
}
