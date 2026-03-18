// Package discovery provides auto-discovery of local LLM inference workers.
package discovery

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// DefaultPorts are the well-known ports to probe for local LLM workers.
var DefaultPorts = []int{8000, 8001, 8002, 8003, 8080, 8081, 8090, 11434}

// DiscoverResult holds the result of discovering a single endpoint.
type DiscoverResult struct {
	Endpoint string
	Backend  metrics.Backend
	Online   bool
}

// DiscoverLocal probes localhost on all DefaultPorts concurrently.
// Returns discovered worker configs.
func DiscoverLocal(ctx context.Context) []Target {
	return DiscoverPorts(ctx, "localhost", DefaultPorts)
}

// DiscoverPorts probes the given host:ports concurrently. It first tries /metrics;
// if that fails (connection error or non-200), it falls back to /v1/metrics for NIM.
// Only ports that return a recognised backend are included in the results.
func DiscoverPorts(ctx context.Context, host string, ports []int) []Target {
	client := &http.Client{
		Timeout: 500 * time.Millisecond,
	}

	results := make(chan Target, len(ports))
	var wg sync.WaitGroup

	for _, port := range ports {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			endpoint := fmt.Sprintf("http://%s:%d", host, p)

			// Phase 1: try /metrics
			body, err := probeEndpoint(ctx, client, endpoint+"/metrics")
			if err == nil {
				backend := detectBackend(body)
				if backend != metrics.BackendUnknown {
					results <- Target{
						Endpoint:    endpoint,
						Backend:     backend,
						MetricsPath: "/metrics",
					}
					return
				}
			}

			// Phase 2: /v1/metrics fallback — covers NIM which does not expose /metrics
			body, err = probeEndpoint(ctx, client, endpoint+"/v1/metrics")
			if err == nil {
				backend := detectBackend(body)
				if backend != metrics.BackendUnknown {
					results <- Target{
						Endpoint:    endpoint,
						Backend:     backend,
						MetricsPath: "/v1/metrics",
					}
					return
				}
			}
			// Phase 3: /api/ps — Ollama JSON endpoint (no Prometheus)
			body, err = probeEndpoint(ctx, client, endpoint+"/api/ps")
			if err == nil && isOllamaResponse(body) {
				results <- Target{
					Endpoint:    endpoint,
					Backend:     metrics.BackendOllama,
					MetricsPath: "/api/ps",
				}
			}
		}(port)
	}

	go func() {
		wg.Wait()
		close(results)
	}()

	var configs []Target
	for cfg := range results {
		configs = append(configs, cfg)
	}
	return configs
}

// probeEndpoint issues a GET to url and returns the body on HTTP 200.
// Any transport error or non-200 status is returned as an error.
func probeEndpoint(ctx context.Context, client *http.Client, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("non-200: %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// isOllamaResponse checks if a response body looks like an Ollama /api/ps JSON response.
func isOllamaResponse(body string) bool {
	return strings.Contains(body, "\"models\"") && strings.Contains(body, "\"size_vram\"")
}

// detectBackend identifies the backend type from raw Prometheus metric text.
// This is a fast line-prefix heuristic used during port-scan discovery.
// The authoritative detection lives in BackendParser.Detect() implementations
// in the collector package, which operate on parsed samples.
//
// IMPORTANT: When adding a new backend, update BOTH this heuristic AND
// the corresponding BackendParser.Detect() in internal/collector/.
// Registered backends: vLLM (vllm:), SGLang (sglang:), LMCache (lmcache_), NIM (conjunction), TGI (tgi_), TRT-LLM (trtllm_), Triton (nv_inference_/nv_trt_llm_).
func detectBackend(body string) metrics.Backend {
	hasRunning := false
	hasCachePerc := false
	hasTTFT := false

	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#") {
			continue
		}
		// Prefixed backends take priority — return immediately on first match.
		if strings.HasPrefix(line, "vllm:") {
			return metrics.BackendVLLM
		}
		if strings.HasPrefix(line, "sglang:") {
			return metrics.BackendSGLang
		}
		if strings.HasPrefix(line, "lmcache_") {
			return metrics.BackendLMCache
		}
		if strings.HasPrefix(line, "tgi_") {
			return metrics.BackendTGI
		}
		if strings.HasPrefix(line, "trtllm_") {
			return metrics.BackendTRTLLM
		}
		if strings.HasPrefix(line, "nv_trt_llm_") || strings.HasPrefix(line, "nv_inference_") {
			return metrics.BackendTriton
		}
		if strings.HasPrefix(line, "llamacpp:") {
			return metrics.BackendLlamaCpp
		}
		if strings.HasPrefix(line, "litellm_") {
			return metrics.BackendLiteLLM
		}
		// Accumulate NIM signals; all three must be present to avoid false positives.
		if strings.HasPrefix(line, "num_requests_running") {
			hasRunning = true
		}
		if strings.HasPrefix(line, "gpu_cache_usage_perc") {
			hasCachePerc = true
		}
		if strings.HasPrefix(line, "time_to_first_token_seconds") {
			hasTTFT = true
		}
	}

	if hasRunning && hasCachePerc && hasTTFT {
		return metrics.BackendNIM
	}

	return metrics.BackendUnknown
}
