package discovery

import (
	"context"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// Target describes a discovered worker endpoint that a collector can poll.
// This type is the boundary between discovery and collection — discovery
// produces Targets, collectors consume them.
type Target struct {
	Endpoint    string
	Label       string
	Backend     metrics.Backend
	MetricsPath string
	Role        string                                    // "prefill", "decode", or "mono"
	NodeName    string                                    // K8s node running the pod
	FetchFunc   func(ctx context.Context) (string, error) // nil = use default HTTP fetch
}
