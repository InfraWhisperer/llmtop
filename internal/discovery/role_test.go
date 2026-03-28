package discovery

import "testing"

func TestDetectRole(t *testing.T) {
	tests := []struct {
		name       string
		labels     map[string]string
		dynamoRole string
		want       string
	}{
		{
			name:   "grove-role takes highest priority",
			labels: map[string]string{
				"nvidia.com/grove-role":        "prefill",
				"llm-d.ai/role":               "decode",
				"app.kubernetes.io/component":  "decode",
			},
			want: "prefill",
		},
		{
			name:   "llm-d role when grove-role absent",
			labels: map[string]string{
				"llm-d.ai/role":               "decode",
				"app.kubernetes.io/component":  "prefill",
			},
			want: "decode",
		},
		{
			name:   "component label when llm-d and grove absent",
			labels: map[string]string{
				"app.kubernetes.io/component": "prefill",
			},
			want: "prefill",
		},
		{
			name:       "dynamo role as fallback",
			labels:     map[string]string{},
			dynamoRole: "decode",
			want:       "decode",
		},
		{
			name:   "defaults to mono when no labels",
			labels: map[string]string{},
			want:   "mono",
		},
		{
			name:   "unrecognized grove-role falls through",
			labels: map[string]string{
				"nvidia.com/grove-role": "router",
				"llm-d.ai/role":        "prefill",
			},
			want: "prefill",
		},
		{
			name:   "case insensitive role values",
			labels: map[string]string{
				"nvidia.com/grove-role": "Prefill",
			},
			want: "prefill",
		},
		{
			name:   "grove-role decode",
			labels: map[string]string{
				"nvidia.com/grove-role": "decode",
			},
			want: "decode",
		},
		{
			name:   "component mono value",
			labels: map[string]string{
				"app.kubernetes.io/component": "mono",
			},
			want: "mono",
		},
		{
			name:   "component monolithic normalizes to mono",
			labels: map[string]string{
				"app.kubernetes.io/component": "monolithic",
			},
			want: "mono",
		},
		{
			name:       "grove-role overrides dynamo role",
			labels:     map[string]string{
				"nvidia.com/grove-role": "decode",
			},
			dynamoRole: "prefill",
			want:       "decode",
		},
		{
			name:   "all labels absent with unrelated labels",
			labels: map[string]string{
				"app": "vllm",
				"version": "v1",
			},
			want: "mono",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pod := DiscoveredPod{
				Labels:     tt.labels,
				DynamoRole: tt.dynamoRole,
			}
			got := DetectRole(pod)
			if got != tt.want {
				t.Errorf("DetectRole() = %q, want %q", got, tt.want)
			}
		})
	}
}
