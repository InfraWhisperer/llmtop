package metrics

import (
	"math"
	"testing"
)

const sampleVLLMMetrics = `# HELP vllm:num_requests_running Number of requests currently running on GPU.
# TYPE vllm:num_requests_running gauge
vllm:num_requests_running{model_name="Llama-3.1-8B-Instruct",version="0.6.3"} 47.0
# HELP vllm:num_requests_waiting Number of requests waiting to be processed.
# TYPE vllm:num_requests_waiting gauge
vllm:num_requests_waiting{model_name="Llama-3.1-8B-Instruct",version="0.6.3"} 12.0
# HELP vllm:gpu_cache_usage_perc GPU KV-cache usage. 1 means 100 percent usage.
# TYPE vllm:gpu_cache_usage_perc gauge
vllm:gpu_cache_usage_perc{model_name="Llama-3.1-8B-Instruct",version="0.6.3"} 0.82
# HELP vllm:gpu_prefix_cache_hit_rate GPU prefix cache block hit rate.
# TYPE vllm:gpu_prefix_cache_hit_rate gauge
vllm:gpu_prefix_cache_hit_rate{model_name="Llama-3.1-8B-Instruct",version="0.6.3"} 0.71
# HELP vllm:time_to_first_token_seconds Histogram of time to first token in seconds.
# TYPE vllm:time_to_first_token_seconds histogram
vllm:time_to_first_token_seconds_bucket{le="0.001",model_name="Llama-3.1-8B-Instruct"} 0
vllm:time_to_first_token_seconds_bucket{le="0.005",model_name="Llama-3.1-8B-Instruct"} 0
vllm:time_to_first_token_seconds_bucket{le="0.01",model_name="Llama-3.1-8B-Instruct"} 0
vllm:time_to_first_token_seconds_bucket{le="0.02",model_name="Llama-3.1-8B-Instruct"} 0
vllm:time_to_first_token_seconds_bucket{le="0.04",model_name="Llama-3.1-8B-Instruct"} 0
vllm:time_to_first_token_seconds_bucket{le="0.06",model_name="Llama-3.1-8B-Instruct"} 0
vllm:time_to_first_token_seconds_bucket{le="0.08",model_name="Llama-3.1-8B-Instruct"} 1
vllm:time_to_first_token_seconds_bucket{le="0.1",model_name="Llama-3.1-8B-Instruct"} 5
vllm:time_to_first_token_seconds_bucket{le="0.15",model_name="Llama-3.1-8B-Instruct"} 20
vllm:time_to_first_token_seconds_bucket{le="0.2",model_name="Llama-3.1-8B-Instruct"} 45
vllm:time_to_first_token_seconds_bucket{le="0.3",model_name="Llama-3.1-8B-Instruct"} 80
vllm:time_to_first_token_seconds_bucket{le="0.4",model_name="Llama-3.1-8B-Instruct"} 95
vllm:time_to_first_token_seconds_bucket{le="0.5",model_name="Llama-3.1-8B-Instruct"} 99
vllm:time_to_first_token_seconds_bucket{le="1.0",model_name="Llama-3.1-8B-Instruct"} 100
vllm:time_to_first_token_seconds_bucket{le="+Inf",model_name="Llama-3.1-8B-Instruct"} 100
vllm:time_to_first_token_seconds_count{model_name="Llama-3.1-8B-Instruct"} 100
vllm:time_to_first_token_seconds_sum{model_name="Llama-3.1-8B-Instruct"} 18.5
`

func TestParseText_Gauge(t *testing.T) {
	pm := ParseText(sampleVLLMMetrics)

	v, _, ok := pm.GetGaugeAny("vllm:num_requests_running")
	if !ok {
		t.Fatal("expected vllm:num_requests_running to be present")
	}
	if v != 47.0 {
		t.Errorf("expected 47.0, got %f", v)
	}

	v, _, ok = pm.GetGaugeAny("vllm:gpu_cache_usage_perc")
	if !ok {
		t.Fatal("expected vllm:gpu_cache_usage_perc to be present")
	}
	if v != 0.82 {
		t.Errorf("expected 0.82, got %f", v)
	}
}

func TestParseText_Labels(t *testing.T) {
	pm := ParseText(sampleVLLMMetrics)

	v, labels, ok := pm.GetGaugeAny("vllm:num_requests_running")
	if !ok {
		t.Fatal("expected metric to be present")
	}
	if v != 47.0 {
		t.Errorf("expected 47.0, got %f", v)
	}
	if labels["model_name"] != "Llama-3.1-8B-Instruct" {
		t.Errorf("expected model_name=Llama-3.1-8B-Instruct, got %q", labels["model_name"])
	}
}

func TestParseText_Histogram(t *testing.T) {
	pm := ParseText(sampleVLLMMetrics)

	// P50 should be around 0.15-0.2s range (50th of 100 observations)
	p50, ok := pm.GetHistogramQuantileAny("vllm:time_to_first_token_seconds", 0.50)
	if !ok {
		t.Fatal("expected histogram to be present")
	}
	if p50 <= 0 {
		t.Errorf("expected p50 > 0, got %f", p50)
	}

	// P99 should be around 0.4-0.5s
	p99, ok := pm.GetHistogramQuantileAny("vllm:time_to_first_token_seconds", 0.99)
	if !ok {
		t.Fatal("expected histogram to be present")
	}
	if p99 <= p50 {
		t.Errorf("expected p99 (%f) > p50 (%f)", p99, p50)
	}
}

func TestParseText_MissingMetric(t *testing.T) {
	pm := ParseText(sampleVLLMMetrics)

	_, _, ok := pm.GetGaugeAny("nonexistent_metric")
	if ok {
		t.Error("expected nonexistent_metric to not be found")
	}
}

func TestParseText_Empty(t *testing.T) {
	pm := ParseText("")
	if len(pm.Samples) != 0 {
		t.Errorf("expected 0 samples, got %d", len(pm.Samples))
	}
}

func TestEstimateQuantile_Empty(t *testing.T) {
	result := estimateQuantile(nil, 0, 0.99)
	if result != 0 {
		t.Errorf("expected 0 for empty buckets, got %f", result)
	}
}

func TestParseLabels(t *testing.T) {
	labels := parseLabels(`model_name="Llama-3.1-8B",version="0.6.3"`)
	if labels["model_name"] != "Llama-3.1-8B" {
		t.Errorf("expected Llama-3.1-8B, got %q", labels["model_name"])
	}
	if labels["version"] != "0.6.3" {
		t.Errorf("expected 0.6.3, got %q", labels["version"])
	}
}

func TestGetHistogramBucketsAny_Sorted(t *testing.T) {
	pm := ParseText(sampleVLLMMetrics)
	buckets, total, ok := pm.GetHistogramBucketsAny("vllm:time_to_first_token_seconds")
	if !ok {
		t.Fatal("expected histogram present")
	}
	if total != 100 {
		t.Errorf("expected count=100, got %f", total)
	}
	for i := 1; i < len(buckets); i++ {
		if buckets[i].UpperBound < buckets[i-1].UpperBound {
			t.Errorf("buckets not sorted asc at index %d: %f < %f", i, buckets[i].UpperBound, buckets[i-1].UpperBound)
		}
	}
}

func TestGetHistogramBucketsAny_Missing(t *testing.T) {
	pm := ParseText(sampleVLLMMetrics)
	_, _, ok := pm.GetHistogramBucketsAny("does_not_exist")
	if ok {
		t.Error("expected ok=false for missing histogram")
	}
}

func TestGetAllGaugesByLabel_TenantTags(t *testing.T) {
	src := `vllm:request_success_total{model_name="m",worker="w",request_tag="team-a"} 100
vllm:request_success_total{model_name="m",worker="w",request_tag="team-b"} 50
vllm:request_success_total{model_name="m",worker="w",request_tag="team-c"} 25
`
	pm := ParseText(src)
	out := pm.GetAllGaugesByLabel("vllm:request_success_total", "request_tag")
	if len(out) != 3 {
		t.Fatalf("expected 3 entries, got %d: %v", len(out), out)
	}
	if out["team-a"] != 100 || out["team-b"] != 50 || out["team-c"] != 25 {
		t.Errorf("unexpected counters: %v", out)
	}
}

func TestGetAllGaugesByLabel_AbsentLabel(t *testing.T) {
	src := `vllm:request_success_total{model_name="m"} 200
`
	pm := ParseText(src)
	out := pm.GetAllGaugesByLabel("vllm:request_success_total", "request_tag")
	if len(out) != 0 {
		t.Errorf("expected empty map when label absent, got %v", out)
	}
}

func TestComputeCDFPoints_ZeroMaxLatency(t *testing.T) {
	buckets := []HistogramBucket{{UpperBound: 1.0, Count: 10}}
	out := ComputeCDFPoints(buckets, 10, 0)
	for i, v := range out {
		if v != 0 {
			t.Errorf("expected zero at index %d, got %f", i, v)
		}
	}
}

func TestComputeCDFPoints_Monotonic(t *testing.T) {
	pm := ParseText(sampleVLLMMetrics)
	buckets, total, ok := pm.GetHistogramBucketsAny("vllm:time_to_first_token_seconds")
	if !ok {
		t.Fatal("histogram missing")
	}
	out := ComputeCDFPoints(buckets, total, 500)
	prev := 0.0
	for i, v := range out {
		if v < 0 || v > 1.0 {
			t.Errorf("CDF[%d]=%f outside [0,1]", i, v)
		}
		if v < prev {
			t.Errorf("non-monotonic at %d: %f < %f", i, v, prev)
		}
		prev = v
	}
}

func TestComputeCDFPoints_SingleInfBucketAllOnes(t *testing.T) {
	buckets := []HistogramBucket{{UpperBound: math.Inf(1), Count: 10}}
	out := ComputeCDFPoints(buckets, 10, 100)
	for i, v := range out {
		if v != 1.0 {
			t.Errorf("expected 1.0 at index %d, got %f", i, v)
		}
	}
}
