package metrics

import "time"

// GPUInfo holds metrics for a single GPU from a DCGM exporter.
type GPUInfo struct {
	Index       int
	UUID        string
	Name        string  // modelName label, e.g. "NVIDIA H100 80GB HBM3"
	Hostname    string
	UtilPct     float64 // 0-100
	MemUsedMB   float64 // DCGM_FI_DEV_FB_USED
	MemTotalMB  float64 // FB_USED + FB_FREE
	MemBWUtil   float64 // DCGM_FI_DEV_MEM_COPY_UTIL (0-100)
	TempC       float64
	PowerW      float64
	NVLinkGBs   float64 // DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL (GB/s)
	ECCErrors   int64   // DCGM_FI_DEV_ECC_DBE_VOL_TOTAL
	SMClockMHz  float64
	MemClockMHz float64
	Pod         string // DCGM "pod" label (empty if unallocated)
	Namespace   string
	Container   string
	UtilHistory []float64 // ring buffer, max 60 samples
	LastScrape  time.Time // timestamp of last successful DCGM scrape
}

// GPUSummary aggregates metrics across all GPUs.
type GPUSummary struct {
	TotalGPUs    int
	ActiveGPUs   int     // GPUs with a pod binding
	AvgUtilPct   float64
	TotalMemUsed float64 // MB
	TotalMemCap  float64 // MB
}

// ComputeGPUSummary computes aggregate stats from a slice of GPU info.
func ComputeGPUSummary(gpus []*GPUInfo) GPUSummary {
	s := GPUSummary{TotalGPUs: len(gpus)}
	var utilSum float64
	for _, g := range gpus {
		if g.Pod != "" {
			s.ActiveGPUs++
		}
		utilSum += g.UtilPct
		s.TotalMemUsed += g.MemUsedMB
		s.TotalMemCap += g.MemTotalMB
	}
	if len(gpus) > 0 {
		s.AvgUtilPct = utilSum / float64(len(gpus))
	}
	return s
}
