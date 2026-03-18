# llmtop Design & Reference

Detailed documentation for llmtop — config format, Kubernetes discovery, GPU monitoring, metrics reference, and architecture.

---

## Config File

```yaml
# cluster.yaml
interval: 2  # refresh every 2 seconds

# Optional: DCGM exporter for GPU view
dcgm_endpoint: http://dcgm-exporter:9400

# Optional: Kubernetes discovery settings
kubernetes:
  namespace: inference
  labelSelector: "app.kubernetes.io/component=inference"
  maxConcurrent: 10
  requestTimeout: "2s"

endpoints:
  - url: http://10.0.0.1:8000
    backend: vllm      # optional — auto-detected if omitted
    label: prefill-1   # optional display label

  - url: http://10.0.0.2:8000
    backend: sglang
    label: decode-1

  - url: http://lmcache:8080
    backend: lmcache
    label: kv-cache

  - url: http://10.0.0.5:8000
    backend: nim
    label: nim-pool-1
    metrics_path: /v1/metrics
```

```bash
llmtop --config cluster.yaml
```

---

## CLI Reference

```bash
# Kubernetes discovery (default when kubeconfig is available)
llmtop                                    # auto-discover all namespaces
llmtop -n inference                       # specific namespace
llmtop -n inference -l app=vllm           # label selector
llmtop --kubeconfig ~/.kube/configs/prod.yaml
llmtop --all-namespaces
llmtop --max-concurrent 20               # API proxy concurrency limit

# Direct endpoints
llmtop -e http://10.0.0.1:8000 -e http://10.0.0.2:8000
llmtop --config cluster.yaml

# GPU monitoring
llmtop --dcgm-endpoint http://dcgm-exporter:9400

# Output modes
llmtop --once                             # single snapshot, table format
llmtop --once --output json               # single snapshot, JSON
llmtop --interval 5                       # custom refresh interval

# Control flags
llmtop --no-k8s                           # disable K8s discovery
llmtop version                            # print version
```

---

## Kubernetes Discovery

llmtop auto-discovers inference pods and scrapes their metrics through the Kubernetes API server proxy — no port-forwards, no NodePort services, no direct pod network access required.

### How it works

1. Lists pods (filtered by label selector or auto-detected by container image name)
2. For each Running/Ready pod, resolves the metrics port and path from annotations, named ports, or backend defaults
3. Scrapes metrics via API server proxy: `GET /api/v1/namespaces/{ns}/pods/{name}:{port}/proxy/metrics`
4. The existing Prometheus parser handles the response identically to direct HTTP scraping

### Auto-detection by image name

| Image contains | Backend | Default port | Default path |
|----------------|---------|--------------|--------------|
| `vllm` | vLLM | 8000 | `/metrics` |
| `sglang` | SGLang | 30000 | `/metrics` |
| `lmcache` | LMCache | 8080 | `/metrics` |
| `nim` | NIM | 8000 | `/v1/metrics` |

### Port resolution priority

1. `llmtop.dev/metrics-port` annotation
2. `prometheus.io/port` annotation
3. Container port named "metrics" or "http"
4. Backend default (see table above)
5. Fallback: 8000

### RBAC requirements

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: llmtop
rules:
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["get", "list", "watch"]
  - apiGroups: [""]
    resources: ["pods/proxy"]
    verbs: ["get"]
```

For namespace-scoped discovery, a Role + RoleBinding in the target namespace is sufficient.

### Fallback chain

Explicit `--endpoint` flags / config endpoints > Kubernetes pod discovery > localhost port probe.

### Build without Kubernetes

```bash
go build -tags nokubernetes ./cmd/llmtop
```

---

## NVIDIA Dynamo Support

llmtop natively understands NVIDIA Dynamo deployments. When K8s discovery finds pods with Dynamo labels (`nvidia.com/dynamo-component-type`), it:

- **Filters out frontend/router pods** — Dynamo frontends are request routers, not inference engines.
- **Labels workers by role** — decode workers show as `decode:<pod-name>`, prefill workers as `prefill:<pod-name>`.
- **Uses the correct metrics port** — Dynamo workers expose Prometheus metrics on port 9090, not the default 8000.
- **Parses standard vLLM metrics** — Dynamo workers run vLLM (or SGLang) under the hood.

Works with both aggregated (`vllm-agg-*`) and disaggregated (`vllm-disagg-*`) Dynamo deployments. No configuration needed.

---

## GPU Monitoring

llmtop displays per-GPU metrics from an NVIDIA DCGM exporter. In Kubernetes mode, the DCGM exporter is auto-discovered from the `gpu-operator` namespace. Otherwise, pass the endpoint explicitly:

```bash
llmtop --dcgm-endpoint http://dcgm-exporter:9400
```

Or in config:

```yaml
dcgm_endpoint: http://dcgm-exporter:9400
```

Press `g` to toggle the GPU view showing per-GPU utilization, VRAM usage, temperature, power draw, and which pod/worker is bound to each GPU.

---

## Metrics Collected

### vLLM
- `vllm:num_requests_running` — active requests per worker
- `vllm:num_requests_waiting` — queue depth
- `vllm:gpu_cache_usage_perc` — KV cache utilization
- `vllm:gpu_prefix_cache_hit_rate` — prefix cache hit rate
- `vllm:time_to_first_token_seconds` — TTFT histogram (P50, P99)
- `vllm:time_per_output_token_seconds` — ITL histogram (P50, P99)
- `vllm:prompt_tokens_total` — prompt throughput (tok/s)
- `vllm:generation_tokens_total` — generation throughput (tok/s)

### SGLang
- `sglang:num_running_reqs` / `sglang:num_waiting_reqs`
- `sglang:token_usage` — KV cache utilization
- `sglang:cache_hit_rate` — prefix cache hit rate
- `sglang:time_to_first_token_seconds` — TTFT histogram
- `sglang:time_per_output_token_seconds` — ITL histogram
- `sglang:prompt_tokens_total` / `sglang:generation_tokens_total`

### LMCache
- `lmcache_hit_rate` — cache hit ratio
- `lmcache_store_size_bytes` — total cached data size
- `lmcache_eviction_total` — eviction counter

### NIM
Same metrics as vLLM but without the `vllm:` prefix, served at `/v1/metrics`.

### TGI (Hugging Face Text Generation Inference)
- `tgi_queue_size` — queue depth (gauge)
- `tgi_batch_current_size` — running requests / batch size (gauge)
- `tgi_request_inference_duration` — inference latency histogram, used as TTFT proxy (seconds)
- `tgi_request_mean_time_per_token_duration` — ITL histogram (seconds)
- `tgi_request_generated_tokens` — generation throughput (histogram _sum for rate)
- `tgi_request_input_length` — prompt throughput (histogram _sum for rate)
- No KV cache metrics — TGI does not expose GPU memory stats via Prometheus
- No model_name label — model info available via `/info` endpoint (JSON)
- Default port: 3000, metrics path: `/metrics`

### DCGM (GPU view)
- `DCGM_FI_DEV_GPU_UTIL` — GPU compute utilization (%)
- `DCGM_FI_DEV_FB_USED` / `DCGM_FI_DEV_FB_FREE` — VRAM usage (MiB)
- `DCGM_FI_DEV_GPU_TEMP` — GPU temperature (C)
- `DCGM_FI_DEV_POWER_USAGE` — power draw (W)
- `DCGM_FI_DEV_SM_CLOCK` / `DCGM_FI_DEV_MEM_CLOCK` — clock frequencies (MHz)

---

## Architecture

```
llmtop
├── cmd/llmtop/           Cobra CLI — flags, config resolution, delegates to App
├── internal/
│   ├── app/              Application lifecycle — owns collectors, reconcile loop, TUI/once dispatch
│   ├── collector/        Concurrent goroutine-per-worker polling + DCGM GPU collector
│   │   ├── interfaces.go MetricsSource / GPUSource interfaces (UI depends on these, not concrete types)
│   │   ├── parser.go     BackendParser interface + registry (pluggable backend support)
│   │   └── <backend>.go  Per-backend parser: Detect() + Parse(), registered via init()
│   ├── discovery/        K8s API proxy discovery + localhost port probe fallback
│   │   ├── target.go     Target type — boundary between discovery and collection
│   │   └── interfaces.go Discoverer interface
│   ├── metrics/          Hand-rolled Prometheus text parser + quantile estimation
│   └── ui/               Bubbletea TUI — worker, GPU, model views + detail panels
└── pkg/config/           YAML config file loading
```

### Key design decisions

- **Interfaces at every boundary.** The UI holds `MetricsSource`/`GPUSource` interfaces, not concrete collector pointers. The reconcile loop takes a `Discoverer`. This enables testing with mock sources and adding new collector types without touching the UI.
- **BackendParser registry.** Adding a new backend is a single-file change: implement `Detect()` + `Parse()`, register in `init()`. No switch statements to update.
- **Discovery → Collector decoupling.** Discovery produces `Target` values, callers convert to `WorkerConfig`. No import cycle between packages.
- **Format-then-style rendering.** Table cells are formatted as plain padded strings first, then styled with ANSI colors only for unselected rows. Selected rows get a single clean style application with no embedded escape codes.

No Prometheus server. No external dependencies beyond the TUI library and client-go. Metrics flow: HTTP/API-proxy → Prometheus text parse → render.

---

## Color Thresholds

| Metric | Green | Yellow | Red |
|--------|-------|--------|-----|
| KV Cache % | < 70% | 70-84% | >= 85% |
| TTFT P99 | < 200ms | 200-499ms | >= 500ms |
| Queue depth | < 10 | 10-19 | >= 20 |
| GPU Util % | < 50% | 50-79% | >= 80% |
| GPU Temp | < 70C | 70-84C | >= 85C |
| VRAM % | < 70% | 70-84% | >= 85% |
