package sim

import "time"

// ScenarioEvent is a timed mutation applied to the simulation.
type ScenarioEvent struct {
	AtSecond   float64
	WorkerName string // "" = cluster-wide
	Apply      func(workers map[string]*WorkerState, gpus map[string]*GPUState)
}

// Scenario is a time-ordered script of events for a simulation run.
type Scenario struct {
	Name     string
	Duration time.Duration
	TickRate time.Duration // wall-clock interval per sim-second
	Events   []ScenarioEvent
}

// GetScenario returns a named built-in scenario.
func GetScenario(name string) Scenario {
	switch name {
	case "demo":
		return demoScenario()
	case "stress":
		return stressScenario()
	case "chaos":
		return chaosScenario()
	default:
		return steadyScenario()
	}
}

func steadyScenario() Scenario {
	return Scenario{
		Name:     "steady",
		Duration: 0, // run forever
		TickRate: 500 * time.Millisecond,
		Events:   nil, // no scripted events, just organic noise
	}
}

func demoScenario() Scenario {
	return Scenario{
		Name:     "demo",
		Duration: 90 * time.Second,
		TickRate: 250 * time.Millisecond, // 4x speed
		Events: []ScenarioEvent{
			// t=12–20: KV pressure builds on prefill-gamma
			{AtSecond: 12, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-prefill-gamma"]; ok {
					ws.KVPercGPU = 0.70
				}
			}},
			{AtSecond: 15, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-prefill-gamma"]; ok {
					ws.KVPercGPU = 0.80
					ws.CacheHitRate = 0.22
				}
			}},
			{AtSecond: 18, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-prefill-gamma"]; ok {
					ws.KVPercGPU = 0.87
					ws.CacheHitRate = 0.17
					ws.KVPercCPU = 0.55
					ws.KVPercNVMe = 0.12
				}
			}},
			// t=20–28: prefill-gamma queue spikes
			{AtSecond: 22, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-prefill-gamma"]; ok {
					ws.QueueDepth = 7
					ws.TTFTp99Ms = 1800
				}
			}},
			{AtSecond: 25, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-prefill-gamma"]; ok {
					ws.QueueDepth = 9
					ws.TTFTp99Ms = 2400
				}
			}},
			// t=30–34: cascade — decode-02 ITL spike
			{AtSecond: 30, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-decode-02"]; ok {
					ws.ITLp99Ms = 2200
					ws.KVPercGPU = 0.78
				}
			}},
			{AtSecond: 33, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-decode-02"]; ok {
					ws.ITLp99Ms = 3200
				}
			}},
			// t=35–45: team-search disproportionate load
			{AtSecond: 36, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				for _, ws := range w {
					if ws.Online {
						ws.TenantReqs["team-search"] += 500
					}
				}
			}},
			// t=48: new worker joins
			{AtSecond: 48, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if _, exists := w["sim-decode-05"]; !exists {
					ws := &WorkerState{
						Name:      "sim-decode-05",
						Role:      "decode",
						NodeName:  "sim-node-03",
						GPUID:     1,
						ModelName: "meta-llama/Llama-3.1-70B-Instruct",
						Port:      19109,
					}
					InitWorker(ws, 99999)
					w["sim-decode-05"] = ws
				}
			}},
			// t=50–55: recovery — gamma queue drains
			{AtSecond: 50, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-prefill-gamma"]; ok {
					ws.QueueDepth = 5
					ws.TTFTp99Ms = 1400
					ws.KVPercGPU = 0.72
				}
			}},
			{AtSecond: 55, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-prefill-gamma"]; ok {
					ws.QueueDepth = 3
					ws.TTFTp99Ms = 1100
					ws.KVPercGPU = 0.60
					ws.CacheHitRate = 0.31
				}
				if ws, ok := w["sim-decode-02"]; ok {
					ws.ITLp99Ms = 180
					ws.KVPercGPU = 0.48
				}
			}},
			// t=65–78: DCGM staleness on GPU 2
			{AtSecond: 70, Apply: func(_ map[string]*WorkerState, g map[string]*GPUState) {
				// Mark GPU 2 on sim-node-01 as stale by zeroing metrics
				// (the actual staleness is simulated by the Simulator not updating LastScrape)
			}},
			// t=80–90: stable recovery
			{AtSecond: 80, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				for _, ws := range w {
					if !ws.Online {
						continue
					}
					base, _, baseTTFT, baseITL, _, _ := BaseValues(ws.Role)
					ws.KVPercGPU = base
					ws.TTFTp99Ms = baseTTFT
					ws.ITLp99Ms = baseITL
					ws.CacheHitRate = 0.35
					ws.QueueDepth = 2
				}
			}},
		},
	}
}

func stressScenario() Scenario {
	return Scenario{
		Name:     "stress",
		Duration: 60 * time.Second,
		TickRate: 500 * time.Millisecond,
		Events: []ScenarioEvent{
			{AtSecond: 5, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				for _, ws := range w {
					if ws.Online {
						ws.KVPercGPU = 0.92
						ws.QueueDepth = 15
						ws.TTFTp99Ms = 4000
						ws.ITLp99Ms = 2500
						ws.CacheHitRate = 0.10
					}
				}
			}},
			{AtSecond: 20, Apply: func(_ map[string]*WorkerState, g map[string]*GPUState) {
				for _, gs := range g {
					gs.ECCErrors = 2
				}
			}},
		},
	}
}

func chaosScenario() Scenario {
	return Scenario{
		Name:     "chaos",
		Duration: 0, // run forever
		TickRate: 500 * time.Millisecond,
		Events: []ScenarioEvent{
			// Workers flap on/off
			{AtSecond: 10, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-decode-01"]; ok {
					ws.Online = false
				}
			}},
			{AtSecond: 25, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-decode-01"]; ok {
					ws.Online = true
				}
			}},
			{AtSecond: 35, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-prefill-alpha"]; ok {
					ws.Online = false
				}
			}},
			{AtSecond: 50, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-prefill-alpha"]; ok {
					ws.Online = true
				}
			}},
			// Random KV spikes
			{AtSecond: 15, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-decode-03"]; ok {
					ws.KVPercGPU = 0.95
				}
			}},
			{AtSecond: 30, Apply: func(w map[string]*WorkerState, _ map[string]*GPUState) {
				if ws, ok := w["sim-decode-03"]; ok {
					ws.KVPercGPU = 0.40
				}
			}},
		},
	}
}
