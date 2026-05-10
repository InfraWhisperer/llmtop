package collector

import (
	"testing"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

const sampleDCGMMetrics = `# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-aaa",modelName="NVIDIA H100 80GB HBM3",Hostname="node1",container="nim-llm",namespace="nim",pod="pool3"} 45
DCGM_FI_DEV_GPU_UTIL{gpu="1",UUID="GPU-bbb",modelName="NVIDIA H100 80GB HBM3",Hostname="node1"} 0
# TYPE DCGM_FI_DEV_FB_USED gauge
DCGM_FI_DEV_FB_USED{gpu="0",UUID="GPU-aaa",modelName="NVIDIA H100 80GB HBM3",Hostname="node1",container="nim-llm",namespace="nim",pod="pool3"} 74359
DCGM_FI_DEV_FB_USED{gpu="1",UUID="GPU-bbb",modelName="NVIDIA H100 80GB HBM3",Hostname="node1"} 0
# TYPE DCGM_FI_DEV_FB_FREE gauge
DCGM_FI_DEV_FB_FREE{gpu="0",UUID="GPU-aaa",modelName="NVIDIA H100 80GB HBM3",Hostname="node1",container="nim-llm",namespace="nim",pod="pool3"} 6635
DCGM_FI_DEV_FB_FREE{gpu="1",UUID="GPU-bbb",modelName="NVIDIA H100 80GB HBM3",Hostname="node1"} 80994
# TYPE DCGM_FI_DEV_GPU_TEMP gauge
DCGM_FI_DEV_GPU_TEMP{gpu="0",UUID="GPU-aaa",modelName="NVIDIA H100 80GB HBM3",Hostname="node1",container="nim-llm",namespace="nim",pod="pool3"} 33
DCGM_FI_DEV_GPU_TEMP{gpu="1",UUID="GPU-bbb",modelName="NVIDIA H100 80GB HBM3",Hostname="node1"} 25
# TYPE DCGM_FI_DEV_POWER_USAGE gauge
DCGM_FI_DEV_POWER_USAGE{gpu="0",UUID="GPU-aaa",modelName="NVIDIA H100 80GB HBM3",Hostname="node1",container="nim-llm",namespace="nim",pod="pool3"} 118.344
DCGM_FI_DEV_POWER_USAGE{gpu="1",UUID="GPU-bbb",modelName="NVIDIA H100 80GB HBM3",Hostname="node1"} 70.013
# TYPE DCGM_FI_DEV_MEM_COPY_UTIL gauge
DCGM_FI_DEV_MEM_COPY_UTIL{gpu="0",UUID="GPU-aaa",modelName="NVIDIA H100 80GB HBM3",Hostname="node1",container="nim-llm",namespace="nim",pod="pool3"} 58
DCGM_FI_DEV_MEM_COPY_UTIL{gpu="1",UUID="GPU-bbb",modelName="NVIDIA H100 80GB HBM3",Hostname="node1"} 12
# TYPE DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL gauge
DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL{gpu="0",UUID="GPU-aaa",modelName="NVIDIA H100 80GB HBM3",Hostname="node1",container="nim-llm",namespace="nim",pod="pool3"} 88
DCGM_FI_DEV_NVLINK_BANDWIDTH_TOTAL{gpu="1",UUID="GPU-bbb",modelName="NVIDIA H100 80GB HBM3",Hostname="node1"} 0
# TYPE DCGM_FI_DEV_ECC_DBE_VOL_TOTAL gauge
DCGM_FI_DEV_ECC_DBE_VOL_TOTAL{gpu="0",UUID="GPU-aaa",modelName="NVIDIA H100 80GB HBM3",Hostname="node1",container="nim-llm",namespace="nim",pod="pool3"} 0
DCGM_FI_DEV_ECC_DBE_VOL_TOTAL{gpu="1",UUID="GPU-bbb",modelName="NVIDIA H100 80GB HBM3",Hostname="node1"} 3
# TYPE DCGM_FI_DEV_SM_CLOCK gauge
DCGM_FI_DEV_SM_CLOCK{gpu="0",UUID="GPU-aaa",modelName="NVIDIA H100 80GB HBM3",Hostname="node1",container="nim-llm",namespace="nim",pod="pool3"} 1980
DCGM_FI_DEV_SM_CLOCK{gpu="1",UUID="GPU-bbb",modelName="NVIDIA H100 80GB HBM3",Hostname="node1"} 345
# TYPE DCGM_FI_DEV_MEM_CLOCK gauge
DCGM_FI_DEV_MEM_CLOCK{gpu="0",UUID="GPU-aaa",modelName="NVIDIA H100 80GB HBM3",Hostname="node1",container="nim-llm",namespace="nim",pod="pool3"} 2619
DCGM_FI_DEV_MEM_CLOCK{gpu="1",UUID="GPU-bbb",modelName="NVIDIA H100 80GB HBM3",Hostname="node1"} 2619
`

func TestParseDCGMMetrics(t *testing.T) {
	pm := metrics.ParseText(sampleDCGMMetrics)
	gpus := make(map[gpuKey]*metrics.GPUInfo)
	parseDCGMMetrics(gpus, pm)

	if len(gpus) != 2 {
		t.Fatalf("expected 2 GPUs, got %d", len(gpus))
	}

	// GPU 0 — allocated with pod binding
	g0 := gpus[gpuKey{Hostname: "node1", Index: 0}]
	if g0 == nil {
		t.Fatal("GPU 0 on node1 not found")
	}
	if g0.UUID != "GPU-aaa" {
		t.Errorf("UUID = %q, want GPU-aaa", g0.UUID)
	}
	if g0.Name != "NVIDIA H100 80GB HBM3" {
		t.Errorf("Name = %q, want NVIDIA H100 80GB HBM3", g0.Name)
	}
	if g0.Hostname != "node1" {
		t.Errorf("Hostname = %q, want node1", g0.Hostname)
	}
	if g0.UtilPct != 45 {
		t.Errorf("UtilPct = %f, want 45", g0.UtilPct)
	}
	if g0.MemUsedMB != 74359 {
		t.Errorf("MemUsedMB = %f, want 74359", g0.MemUsedMB)
	}
	if g0.MemTotalMB != 80994 { // 74359 + 6635
		t.Errorf("MemTotalMB = %f, want 80994", g0.MemTotalMB)
	}
	if g0.TempC != 33 {
		t.Errorf("TempC = %f, want 33", g0.TempC)
	}
	if g0.PowerW != 118.344 {
		t.Errorf("PowerW = %f, want 118.344", g0.PowerW)
	}
	if g0.SMClockMHz != 1980 {
		t.Errorf("SMClockMHz = %f, want 1980", g0.SMClockMHz)
	}
	if g0.MemClockMHz != 2619 {
		t.Errorf("MemClockMHz = %f, want 2619", g0.MemClockMHz)
	}
	if g0.Pod != "pool3" {
		t.Errorf("Pod = %q, want pool3", g0.Pod)
	}
	if g0.Namespace != "nim" {
		t.Errorf("Namespace = %q, want nim", g0.Namespace)
	}
	if g0.Container != "nim-llm" {
		t.Errorf("Container = %q, want nim-llm", g0.Container)
	}
	if g0.MemBWUtil != 58 {
		t.Errorf("MemBWUtil = %f, want 58", g0.MemBWUtil)
	}
	if g0.NVLinkGBs != 88 {
		t.Errorf("NVLinkGBs = %f, want 88", g0.NVLinkGBs)
	}
	if g0.ECCErrors != 0 {
		t.Errorf("ECCErrors = %d, want 0", g0.ECCErrors)
	}
	if g0.LastScrape.IsZero() {
		t.Error("LastScrape should be set after parsing")
	}

	// GPU 1 — unallocated
	g1 := gpus[gpuKey{Hostname: "node1", Index: 1}]
	if g1 == nil {
		t.Fatal("GPU 1 on node1 not found")
	}
	if g1.UUID != "GPU-bbb" {
		t.Errorf("UUID = %q, want GPU-bbb", g1.UUID)
	}
	if g1.Pod != "" {
		t.Errorf("Pod = %q, want empty for unallocated GPU", g1.Pod)
	}
	if g1.MemTotalMB != 80994 { // 0 + 80994
		t.Errorf("MemTotalMB = %f, want 80994", g1.MemTotalMB)
	}
	if g1.TempC != 25 {
		t.Errorf("TempC = %f, want 25", g1.TempC)
	}
	if g1.MemBWUtil != 12 {
		t.Errorf("MemBWUtil = %f, want 12", g1.MemBWUtil)
	}
	if g1.ECCErrors != 3 {
		t.Errorf("ECCErrors = %d, want 3", g1.ECCErrors)
	}
}

func TestParseDCGMMetrics_UtilHistory(t *testing.T) {
	pm1 := metrics.ParseText(sampleDCGMMetrics)
	gpus := make(map[gpuKey]*metrics.GPUInfo)
	parseDCGMMetrics(gpus, pm1)

	// Simulate a second poll with a different util value for GPU 0.
	const secondPoll = `# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-aaa",modelName="NVIDIA H100 80GB HBM3",Hostname="node1",container="nim-llm",namespace="nim",pod="pool3"} 72
DCGM_FI_DEV_GPU_UTIL{gpu="1",UUID="GPU-bbb",modelName="NVIDIA H100 80GB HBM3",Hostname="node1"} 10
`
	pm2 := metrics.ParseText(secondPoll)
	parseDCGMMetrics(gpus, pm2)

	g0 := gpus[gpuKey{Hostname: "node1", Index: 0}]
	if g0 == nil {
		t.Fatal("GPU 0 on node1 not found after second poll")
	}
	if len(g0.UtilHistory) != 2 {
		t.Fatalf("UtilHistory len = %d, want 2", len(g0.UtilHistory))
	}
	if g0.UtilHistory[0] != 45 {
		t.Errorf("UtilHistory[0] = %f, want 45", g0.UtilHistory[0])
	}
	if g0.UtilHistory[1] != 72 {
		t.Errorf("UtilHistory[1] = %f, want 72", g0.UtilHistory[1])
	}
	if g0.UtilPct != 72 {
		t.Errorf("UtilPct = %f, want 72 after second poll", g0.UtilPct)
	}
}

func TestParseDCGMMetrics_UtilHistoryCap(t *testing.T) {
	gpus := make(map[gpuKey]*metrics.GPUInfo)
	// Seed with MaxHistory+5 existing entries.
	existing := make([]float64, MaxHistory+5)
	for i := range existing {
		existing[i] = float64(i)
	}
	k := gpuKey{Hostname: "node1", Index: 0}
	gpus[k] = &metrics.GPUInfo{
		Index:       0,
		Hostname:    "node1",
		UtilHistory: existing,
	}

	const poll = `# TYPE DCGM_FI_DEV_GPU_UTIL gauge
DCGM_FI_DEV_GPU_UTIL{gpu="0",UUID="GPU-aaa",modelName="NVIDIA H100 80GB HBM3",Hostname="node1"} 99
`
	pm := metrics.ParseText(poll)
	parseDCGMMetrics(gpus, pm)

	g := gpus[k]
	if len(g.UtilHistory) != MaxHistory {
		t.Errorf("UtilHistory len = %d, want %d (capped)", len(g.UtilHistory), MaxHistory)
	}
	// Last entry must be the new sample.
	if g.UtilHistory[len(g.UtilHistory)-1] != 99 {
		t.Errorf("last UtilHistory entry = %f, want 99", g.UtilHistory[len(g.UtilHistory)-1])
	}
}

func TestComputeGPUSummary(t *testing.T) {
	gpus := []*metrics.GPUInfo{
		{Index: 0, Hostname: "node1", UtilPct: 45, MemUsedMB: 74359, MemTotalMB: 80994, Pod: "pool3"},
		{Index: 1, Hostname: "node1", UtilPct: 0, MemUsedMB: 0, MemTotalMB: 80994, Pod: ""},
	}
	s := metrics.ComputeGPUSummary(gpus)

	if s.TotalGPUs != 2 {
		t.Errorf("TotalGPUs = %d, want 2", s.TotalGPUs)
	}
	if s.ActiveGPUs != 1 {
		t.Errorf("ActiveGPUs = %d, want 1", s.ActiveGPUs)
	}
	if s.AvgUtilPct != 22.5 {
		t.Errorf("AvgUtilPct = %f, want 22.5", s.AvgUtilPct)
	}
	if s.TotalMemUsed != 74359 {
		t.Errorf("TotalMemUsed = %f, want 74359", s.TotalMemUsed)
	}
	if s.TotalMemCap != 161988 {
		t.Errorf("TotalMemCap = %f, want 161988", s.TotalMemCap)
	}
}
