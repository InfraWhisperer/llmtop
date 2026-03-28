package collector

// counterState holds raw Prometheus counter values for rate computation.
// These are internal to the collector and never exported or serialized.
// Previously, these values were aliased onto WorkerMetrics.StoreSizeBytes
// and WorkerMetrics.EvictionTotal, which corrupted JSON export output
// for non-LMCache backends.
//
// All three rate-computing backends (vLLM, SGLang, NIM) use the same
// two counters: prompt_tokens_total and generation_tokens_total.
type counterState struct {
	promptTokensTotal float64
	genTokensTotal    float64
	preemptionsTotal  float64
}
