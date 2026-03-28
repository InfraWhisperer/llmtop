package sim

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// FakeK8sServer returns an http.Handler that responds to the K8s API calls
// llmtop makes during pod discovery and reconciliation.
func FakeK8sServer(workers []*WorkerState) http.Handler {
	mux := http.NewServeMux()

	// GET /api/v1/namespaces/{ns}/pods — list pods
	mux.HandleFunc("/api/v1/namespaces/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if strings.HasSuffix(path, "/pods") {
			renderPodList(w, workers)
			return
		}
		if strings.Contains(path, "/pods/") && strings.Contains(path, "/proxy/") {
			// API server proxy: extract pod name and route to its metrics handler
			renderProxyMetrics(w, r, path, workers)
			return
		}
		if strings.HasSuffix(path, "/events") {
			renderEventList(w)
			return
		}
		http.NotFound(w, r)
	})

	// Health endpoint
	mux.HandleFunc("/api", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"kind":"APIVersions","versions":["v1"]}`)
	})

	return mux
}

func renderPodList(w http.ResponseWriter, workers []*WorkerState) {
	type containerPort struct {
		Name          string `json:"name"`
		ContainerPort int    `json:"containerPort"`
	}
	type container struct {
		Name  string          `json:"name"`
		Image string          `json:"image"`
		Ports []containerPort `json:"ports"`
	}
	type podSpec struct {
		NodeName   string      `json:"nodeName"`
		Containers []container `json:"containers"`
	}
	type podCondition struct {
		Type   string `json:"type"`
		Status string `json:"status"`
	}
	type podStatus struct {
		Phase      string         `json:"phase"`
		PodIP      string         `json:"podIP"`
		Conditions []podCondition `json:"conditions"`
	}
	type podMeta struct {
		Name        string            `json:"name"`
		Namespace   string            `json:"namespace"`
		Labels      map[string]string `json:"labels"`
		Annotations map[string]string `json:"annotations"`
	}
	type pod struct {
		Metadata podMeta   `json:"metadata"`
		Spec     podSpec   `json:"spec"`
		Status   podStatus `json:"status"`
	}
	type podList struct {
		Kind       string `json:"kind"`
		APIVersion string `json:"apiVersion"`
		Items      []pod  `json:"items"`
	}

	items := make([]pod, 0, len(workers))
	for _, ws := range workers {
		labels := map[string]string{
			"app.kubernetes.io/name":      "llmtop-worker",
			"app.kubernetes.io/component": ws.Role,
			"nvidia.com/grove-role":       ws.Role,
		}

		phase := "Running"
		if !ws.Online {
			phase = "Running" // still running but metrics fail
		}

		p := pod{
			Metadata: podMeta{
				Name:      ws.Name,
				Namespace: "inference",
				Labels:    labels,
				Annotations: map[string]string{
					"prometheus.io/port": fmt.Sprintf("%d", ws.Port),
				},
			},
			Spec: podSpec{
				NodeName: ws.NodeName,
				Containers: []container{
					{
						Name:  "vllm",
						Image: "vllm/vllm-openai:latest",
						Ports: []containerPort{
							{Name: "metrics", ContainerPort: ws.Port},
						},
					},
				},
			},
			Status: podStatus{
				Phase: phase,
				PodIP: fmt.Sprintf("10.0.%d.%d", ws.GPUID, ws.Port%256),
				Conditions: []podCondition{
					{Type: "Ready", Status: "True"},
				},
			},
		}
		items = append(items, p)
	}

	result := podList{
		Kind:       "PodList",
		APIVersion: "v1",
		Items:      items,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func renderProxyMetrics(w http.ResponseWriter, _ *http.Request, path string, workers []*WorkerState) {
	// Path format: /api/v1/namespaces/inference/pods/<name>:<port>/proxy/metrics
	parts := strings.Split(path, "/")
	for i, p := range parts {
		if p == "pods" && i+1 < len(parts) {
			podPortStr := parts[i+1]
			podName := strings.Split(podPortStr, ":")[0]
			for _, ws := range workers {
				if ws.Name == podName {
					if !ws.Online {
						http.Error(w, "scrape error: connection refused", http.StatusServiceUnavailable)
						return
					}
					w.Header().Set("Content-Type", "text/plain")
					fmt.Fprint(w, RenderWorkerMetrics(ws))
					return
				}
			}
		}
	}
	http.NotFound(w, nil)
}

func renderEventList(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"kind":"EventList","apiVersion":"v1","items":[]}`)
}
