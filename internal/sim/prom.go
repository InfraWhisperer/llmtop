package sim

import (
	"fmt"
	"strings"
)

// RenderWorkerMetrics produces Prometheus text format for a single worker.
func RenderWorkerMetrics(w *WorkerState) string {
	if !w.Online {
		return "" // 503 handler returns error, not metrics
	}

	var sb strings.Builder
	model := w.ModelName
	labels := fmt.Sprintf(`model_name="%s",worker="%s"`, model, w.Name)

	// Core gauges
	sb.WriteString(promGauge("vllm:gpu_cache_usage_perc", labels, w.KVPercGPU))
	sb.WriteString(promGauge("vllm:cpu_cache_usage_perc", labels, w.KVPercCPU))
	if w.KVPercNVMe > 0 {
		sb.WriteString(promGauge("vllm:nvme_cache_usage_perc", labels, w.KVPercNVMe))
	}
	sb.WriteString(promGauge("vllm:num_requests_running", labels, float64(w.RunningReqs)))
	sb.WriteString(promGauge("vllm:num_requests_waiting", labels, float64(w.QueueDepth)))
	sb.WriteString(promGauge("vllm:gpu_prefix_cache_hit_rate", labels, w.CacheHitRate))

	// Counters
	sb.WriteString(promCounter("vllm:num_preemptions_total", labels, float64(w.Preemptions)))
	sb.WriteString(promCounter("vllm:prompt_tokens_total", labels, float64(w.PromptToks)))
	sb.WriteString(promCounter("vllm:generation_tokens_total", labels, float64(w.GenToks)))

	// Per-tenant request counters
	for tenant, count := range w.TenantReqs {
		tLabels := fmt.Sprintf(`model_name="%s",worker="%s",request_tag="%s"`, model, w.Name, tenant)
		sb.WriteString(promCounter("vllm:request_success_total", tLabels, float64(count)))
	}
	// Total requests (no tag)
	sb.WriteString(promCounter("vllm:request_success_total", labels, float64(w.RequestsTotal)))

	// TTFT histogram (prefill workers primarily, but emit for all)
	sb.WriteString(renderHistogram("vllm:time_to_first_token_seconds", labels,
		w.TTFTp99Ms/1000.0, w.RequestsTotal))

	// ITL histogram
	sb.WriteString(renderHistogram("vllm:time_per_output_token_seconds", labels,
		w.ITLp99Ms/1000.0, w.GenToks/100))

	// KV offload histogram (prefill only). Synthetic P99 is ~2% of TTFT P99 —
	// matches the disagg invariant where prestage is small relative to TTFT.
	if w.Role == "prefill" {
		xferP99Sec := (w.TTFTp99Ms * 0.02) / 1000.0
		sb.WriteString(renderHistogram("vllm:kv_cache_offload_time_seconds", labels,
			xferP99Sec, w.RequestsTotal))
	}

	return sb.String()
}

// RenderDCGMMetrics produces Prometheus text format for all GPUs.
func RenderDCGMMetrics(gpus []*GPUState) string {
	var sb strings.Builder

	for _, g := range gpus {
		labels := fmt.Sprintf(`gpu="%d",UUID="%s",modelName="NVIDIA H100 80GB HBM3",Hostname="%s",exported_pod="%s"`,
			g.GPUID, g.UUID, g.NodeName, g.WorkerName)

		sb.WriteString(promGauge("DCGM_FI_DEV_GPU_UTIL", labels, g.SMUtil))
		sb.WriteString(promGauge("DCGM_FI_DEV_MEM_COPY_UTIL", labels, g.MemBWUtil))
		sb.WriteString(promGauge("DCGM_FI_DEV_GPU_TEMP", labels, g.TempC))
		sb.WriteString(promGauge("DCGM_FI_DEV_POWER_USAGE", labels, g.PowerW))
		sb.WriteString(promGauge("DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL", labels, g.NVLinkGBs))
		sb.WriteString(promGauge("DCGM_FI_DEV_ECC_DBE_VOL_TOTAL", labels, float64(g.ECCErrors)))
		sb.WriteString(promGauge("DCGM_FI_DEV_FB_USED", labels, g.VRAMUsedGB*1024))
		sb.WriteString(promGauge("DCGM_FI_DEV_FB_FREE", labels, (g.VRAMTotalGB-g.VRAMUsedGB)*1024))
	}

	return sb.String()
}

func promGauge(name, labels string, value float64) string {
	return fmt.Sprintf("%s{%s} %g\n", name, labels, value)
}

func promCounter(name, labels string, value float64) string {
	return fmt.Sprintf("%s{%s} %g\n", name, labels, value)
}

// renderHistogram synthesizes a Prometheus histogram from a p99 value.
// Buckets: 0.5, 1.0, 2.0, 5.0, +Inf
func renderHistogram(name, labels string, p99Seconds float64, totalCount int64) string {
	if totalCount <= 0 {
		totalCount = 1
	}
	count := float64(totalCount)

	// Distribute count across buckets based on p99
	buckets := []float64{0.5, 1.0, 2.0, 5.0}
	var sb strings.Builder

	cumulative := 0.0
	for _, le := range buckets {
		var fraction float64
		ratio := le / p99Seconds
		if ratio >= 1.0 {
			fraction = 0.99
		} else {
			fraction = ratio * 0.85
		}
		cumulative = fraction * count
		fmt.Fprintf(&sb, "%s_bucket{%s,le=\"%.1f\"} %.0f\n", name, labels, le, cumulative)
	}
	fmt.Fprintf(&sb, "%s_bucket{%s,le=\"+Inf\"} %.0f\n", name, labels, count)
	fmt.Fprintf(&sb, "%s_sum{%s} %g\n", name, labels, p99Seconds*0.6*count)
	fmt.Fprintf(&sb, "%s_count{%s} %.0f\n", name, labels, count)

	return sb.String()
}
