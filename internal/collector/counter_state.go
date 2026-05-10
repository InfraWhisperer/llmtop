package collector

// counterState holds raw Prometheus counter values for rate computation.
// These are internal to the collector and never exported or serialized.
// Previously, these values were aliased onto WorkerMetrics.StoreSizeBytes
// and WorkerMetrics.EvictionTotal, which corrupted JSON export output
// for non-LMCache backends.
//
// All three rate-computing backends (vLLM, SGLang, NIM) use the same
// two counters: prompt_tokens_total and generation_tokens_total.
//
// tenantReqTotals retains the previous-poll vllm:request_success_total
// counter per request_tag value. It is consumed by the model_group
// aggregation layer (which currently relies on raw counters, not rates),
// but kept here so future rate computation has a stable place to live
// without churning unrelated parsers.
type counterState struct {
	promptTokensTotal float64
	genTokensTotal    float64
	preemptionsTotal  float64
	tenantReqTotals   map[string]float64
}
