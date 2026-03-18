//go:build !nokubernetes

// Package discovery provides auto-discovery of LLM inference workers.
// This file implements Kubernetes-native pod discovery using the API server proxy
// to scrape Prometheus metrics without requiring port-forwards or direct pod access.
package discovery

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// DiscoveredPod holds metadata for a pod discovered via the Kubernetes API.
type DiscoveredPod struct {
	Name        string
	Namespace   string
	NodeName    string
	PodIP       string
	Backend     metrics.Backend
	MetricsPort int
	MetricsPath string
	Labels      map[string]string
	GPURequests int64
	Ready       bool
	DynamoRole  string // "prefill", "decode", "frontend", or "" (not a Dynamo pod)
}

// Dynamo label keys
const (
	dynamoComponentPodLabel    = "nvidia.com/dynamo-component-pod"
	dynamoComponentTypeLabel   = "nvidia.com/dynamo-component-type"
	dynamoSubComponentLabel    = "nvidia.com/dynamo-sub-component-type"
	dynamoComponentNameLabel   = "nvidia.com/dynamo-component"
)

// KubernetesDiscoverer discovers LLM inference pods and scrapes their metrics
// through the Kubernetes API server proxy.
type KubernetesDiscoverer struct {
	clientset     kubernetes.Interface
	restClient    rest.Interface
	namespace     string
	selector      string
	maxConcurrent int
	reqTimeout    time.Duration
	contextName   string
}

// knownImagePatterns maps container image substrings to backends.
var knownImagePatterns = []struct {
	pattern string
	backend metrics.Backend
}{
	{"vllm", metrics.BackendVLLM},
	{"sglang", metrics.BackendSGLang},
	{"lmcache", metrics.BackendLMCache},
	{"nim", metrics.BackendNIM},
	{"text-generation-inference", metrics.BackendTGI},
	{"tgi", metrics.BackendTGI},
	{"tensorrt-llm", metrics.BackendTRTLLM},
	{"trtllm", metrics.BackendTRTLLM},
	{"tritonserver", metrics.BackendTriton},
	{"triton", metrics.BackendTriton},
	{"llama-server", metrics.BackendLlamaCpp},
	{"llama.cpp", metrics.BackendLlamaCpp},
	{"llama-cpp", metrics.BackendLlamaCpp},
}

// defaultPorts maps backends to their well-known metrics ports.
var defaultPorts = map[metrics.Backend]int{
	metrics.BackendVLLM:    8000,
	metrics.BackendSGLang:  30000,
	metrics.BackendNIM:     8000,
	metrics.BackendLMCache: 8080,
	metrics.BackendTGI:     3000,
	metrics.BackendTRTLLM:  8000,
	metrics.BackendTriton:   8002,
	metrics.BackendLlamaCpp: 8080,
}

// NewKubernetesDiscoverer creates a discoverer from a kubeconfig path.
// If kubeconfig is empty, falls back to in-cluster config.
func NewKubernetesDiscoverer(kubeconfig, namespace, selector string, maxConcurrent int, reqTimeout time.Duration) (*KubernetesDiscoverer, error) {
	var config *rest.Config
	var contextName string

	if kubeconfig != "" {
		loadingRules := &clientcmd.ClientConfigLoadingRules{ExplicitPath: kubeconfig}
		configOverrides := &clientcmd.ConfigOverrides{}
		clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

		var err error
		config, err = clientConfig.ClientConfig()
		if err != nil {
			return nil, fmt.Errorf("loading kubeconfig %s: %w", kubeconfig, err)
		}

		rawConfig, err := clientConfig.RawConfig()
		if err == nil {
			contextName = rawConfig.CurrentContext
		}
	} else {
		// Try default kubeconfig loading rules first (respects KUBECONFIG env, ~/.kube/config)
		loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
		configOverrides := &clientcmd.ConfigOverrides{}
		clientConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

		var err error
		config, err = clientConfig.ClientConfig()
		if err != nil {
			// Fall back to in-cluster config
			config, err = rest.InClusterConfig()
			if err != nil {
				return nil, fmt.Errorf("no kubeconfig found and not running in-cluster: %w", err)
			}
			contextName = "in-cluster"
		} else {
			rawConfig, rawErr := clientConfig.RawConfig()
			if rawErr == nil {
				contextName = rawConfig.CurrentContext
			}
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating kubernetes clientset: %w", err)
	}

	if maxConcurrent <= 0 {
		maxConcurrent = 10
	}
	if reqTimeout <= 0 {
		reqTimeout = 2 * time.Second
	}

	return &KubernetesDiscoverer{
		clientset:     clientset,
		restClient:    clientset.CoreV1().RESTClient(),
		namespace:     namespace,
		selector:      selector,
		maxConcurrent: maxConcurrent,
		reqTimeout:    reqTimeout,
		contextName:   contextName,
	}, nil
}

// ContextName returns the Kubernetes context name for display.
func (d *KubernetesDiscoverer) ContextName() string {
	return d.contextName
}

// DiscoverPods lists pods matching the configured selector or auto-detects
// inference pods by container image name. Only Running pods with a Ready
// condition are returned.
func (d *KubernetesDiscoverer) DiscoverPods(ctx context.Context) ([]DiscoveredPod, error) {
	listOpts := metav1.ListOptions{}
	if d.selector != "" {
		listOpts.LabelSelector = d.selector
	}

	podList, err := d.clientset.CoreV1().Pods(d.namespace).List(ctx, listOpts)
	if err != nil {
		return nil, fmt.Errorf("listing pods: %w", err)
	}

	var pods []DiscoveredPod
	for i := range podList.Items {
		pod := &podList.Items[i]

		if pod.Status.Phase != corev1.PodRunning {
			continue
		}
		if !isPodReady(pod) {
			continue
		}

		// Check if this is a Dynamo-managed pod via component-type label
		compType := pod.Labels[dynamoComponentTypeLabel]
		isDynamo := compType != ""
		var dynamoRole string

		if isDynamo {
			subComp := pod.Labels[dynamoSubComponentLabel]

			switch compType {
			case "frontend":
				// Skip Dynamo frontends — they're routers, not inference engines
				continue
			case "worker":
				if subComp == "prefill" {
					dynamoRole = "prefill"
				} else {
					dynamoRole = "decode"
				}
			case "planner":
				// Skip planners — they're autoscaling controllers
				continue
			default:
				// Unknown Dynamo component, skip
				continue
			}
		}

		// Detect backend from container images
		backend, containerIdx := detectBackendFromImages(pod)

		// If no label selector was provided and we couldn't detect a known backend
		// from the image, skip this pod — it's not an inference worker.
		if d.selector == "" && backend == metrics.BackendUnknown && !isDynamo {
			continue
		}

		// Dynamo workers default to vLLM if image detection didn't match
		if isDynamo && backend == metrics.BackendUnknown {
			backend = metrics.BackendVLLM
		}

		metricsPort := resolveMetricsPort(pod, backend, containerIdx)
		metricsPath := resolveMetricsPath(pod, backend)
		gpuRequests := countGPURequests(pod)

		pods = append(pods, DiscoveredPod{
			Name:        pod.Name,
			Namespace:   pod.Namespace,
			NodeName:    pod.Spec.NodeName,
			PodIP:       pod.Status.PodIP,
			Backend:     backend,
			MetricsPort: metricsPort,
			MetricsPath: metricsPath,
			Labels:      pod.Labels,
			GPURequests: gpuRequests,
			Ready:       true,
			DynamoRole:  dynamoRole,
		})
	}

	return pods, nil
}

// ScrapeMetrics fetches Prometheus metrics from a pod via the API server proxy.
// Uses bounded concurrency internally when called from a batch context; callers
// can also use this for individual pod scrapes.
func (d *KubernetesDiscoverer) ScrapeMetrics(ctx context.Context, pod DiscoveredPod) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, d.reqTimeout)
	defer cancel()

	result := d.restClient.Get().
		Namespace(pod.Namespace).
		Resource("pods").
		Name(fmt.Sprintf("%s:%d", pod.Name, pod.MetricsPort)).
		SubResource("proxy").
		Suffix(strings.TrimPrefix(pod.MetricsPath, "/")).
		Timeout(d.reqTimeout).
		Do(ctx)

	if err := result.Error(); err != nil {
		return "", fmt.Errorf("proxy scrape %s/%s:%d%s: %w", pod.Namespace, pod.Name, pod.MetricsPort, pod.MetricsPath, err)
	}

	raw, err := result.Raw()
	if err != nil {
		return "", fmt.Errorf("reading proxy response %s/%s: %w", pod.Namespace, pod.Name, err)
	}
	return string(raw), nil
}

// ToTargets converts discovered pods into discovery Targets.
// Each target's FetchFunc closure captures the discoverer and pod, routing
// metric fetches through the API server proxy.
func (d *KubernetesDiscoverer) ToTargets(pods []DiscoveredPod) []Target {
	configs := make([]Target, 0, len(pods))
	for _, pod := range pods {
		p := pod // capture for closure
		endpoint := fmt.Sprintf("k8s://%s/%s", p.Namespace, p.Name)
		label := dynamoLabel(p)
		configs = append(configs, Target{
			Endpoint:    endpoint,
			Label:       label,
			Backend:     p.Backend,
			MetricsPath: p.MetricsPath,
			FetchFunc: func(ctx context.Context) (string, error) {
				return d.ScrapeMetrics(ctx, p)
			},
		})
	}
	return configs
}

// dynamoLabel builds a human-friendly label for a pod.
// Dynamo pods get a role prefix + pod name (e.g., "decode:vllm-worker-abc123").
// Non-Dynamo pods just use the pod name.
func dynamoLabel(p DiscoveredPod) string {
	if p.DynamoRole == "" {
		return p.Name
	}
	return p.DynamoRole + ":" + p.Name
}

// ScrapeAll fetches metrics from all given pods concurrently with bounded parallelism.
func (d *KubernetesDiscoverer) ScrapeAll(ctx context.Context, pods []DiscoveredPod) map[string]string {
	results := make(map[string]string)
	var mu sync.Mutex
	sem := make(chan struct{}, d.maxConcurrent)
	var wg sync.WaitGroup

	for _, pod := range pods {
		wg.Add(1)
		go func(p DiscoveredPod) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			body, err := d.ScrapeMetrics(ctx, p)
			if err != nil {
				return
			}
			key := fmt.Sprintf("k8s://%s/%s", p.Namespace, p.Name)
			mu.Lock()
			results[key] = body
			mu.Unlock()
		}(pod)
	}

	wg.Wait()
	return results
}

// isPodReady checks if a pod has the Ready condition set to True.
func isPodReady(pod *corev1.Pod) bool {
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

// detectBackendFromImages inspects container images and returns the detected
// backend type and the index of the matching container.
func detectBackendFromImages(pod *corev1.Pod) (metrics.Backend, int) {
	for i, container := range pod.Spec.Containers {
		imageLower := strings.ToLower(container.Image)
		for _, kip := range knownImagePatterns {
			if strings.Contains(imageLower, kip.pattern) {
				return kip.backend, i
			}
		}
	}
	return metrics.BackendUnknown, 0
}

// metricsPortNames lists container port names that indicate a Prometheus
// metrics endpoint, checked in priority order across ALL containers.
var metricsPortNames = []string{"metrics", "prometheus", "system"}

// resolveMetricsPort determines the metrics port for a pod by inspecting the
// actual K8s pod spec rather than relying on hardcoded defaults.
//
// Priority:
//  1. llmtop.dev/metrics-port annotation (explicit override)
//  2. prometheus.io/port annotation (common convention)
//  3. Port named "metrics", "prometheus", or "system" on ANY container
//  4. Known backend defaults (vLLM=8000, SGLang=30000, etc.)
//  5. Fallback: 8000
func resolveMetricsPort(pod *corev1.Pod, backend metrics.Backend, _ int) int {
	// Priority 1: llmtop annotation
	if v, ok := pod.Annotations["llmtop.dev/metrics-port"]; ok {
		if port := parsePort(v); port > 0 {
			return port
		}
	}

	// Priority 2: prometheus.io/port annotation
	if v, ok := pod.Annotations["prometheus.io/port"]; ok {
		if port := parsePort(v); port > 0 {
			return port
		}
	}

	// Priority 3: scan ALL containers for a well-known metrics port name.
	// This handles Dynamo pods where metrics live on the "main" container's
	// "system" port (9090), not the backend-detected container's "http" port.
	for _, name := range metricsPortNames {
		for i := range pod.Spec.Containers {
			for _, p := range pod.Spec.Containers[i].Ports {
				if strings.ToLower(p.Name) == name {
					return int(p.ContainerPort)
				}
			}
		}
	}

	// Priority 4: backend defaults
	if port, ok := defaultPorts[backend]; ok {
		return port
	}

	// Priority 5: fallback
	return 8000
}

// resolveMetricsPath determines the metrics path for a pod.
func resolveMetricsPath(pod *corev1.Pod, backend metrics.Backend) string {
	if v, ok := pod.Annotations["llmtop.dev/metrics-path"]; ok && v != "" {
		return v
	}
	if backend == metrics.BackendNIM {
		return "/v1/metrics"
	}
	if backend == metrics.BackendTRTLLM {
		return "/prometheus/metrics"
	}
	return "/metrics"
}

// countGPURequests sums nvidia.com/gpu resource requests across all containers.
func countGPURequests(pod *corev1.Pod) int64 {
	gpuResource := corev1.ResourceName("nvidia.com/gpu")
	var total int64
	for _, c := range pod.Spec.Containers {
		if q, ok := c.Resources.Requests[gpuResource]; ok {
			total += q.ScaledValue(resource.Scale(0))
		}
	}
	return total
}

// DiscoverDCGMPods searches for DCGM exporter pods across well-known namespaces
// (gpu-operator, monitoring, kube-system) and returns a FetchFunc per pod.
// DCGM exporters run as DaemonSets — one per GPU node — so we must scrape all
// of them to get a complete picture of the cluster's GPUs.
func (d *KubernetesDiscoverer) DiscoverDCGMPods(ctx context.Context) ([]func(ctx context.Context) (string, error), error) {
	dcgmNamespaces := []string{"gpu-operator", "monitoring", "kube-system"}
	if d.namespace != "" {
		dcgmNamespaces = append([]string{d.namespace}, dcgmNamespaces...)
	}

	dcgmSelectors := []string{
		"app=nvidia-dcgm-exporter",
		"app.kubernetes.io/name=dcgm-exporter",
	}

	var fetchFuncs []func(ctx context.Context) (string, error)
	seen := make(map[string]struct{}) // dedup by pod name

	for _, ns := range dcgmNamespaces {
		for _, sel := range dcgmSelectors {
			podList, err := d.clientset.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{
				LabelSelector: sel,
			})
			if err != nil {
				continue
			}
			for i := range podList.Items {
				pod := &podList.Items[i]
				if pod.Status.Phase != corev1.PodRunning || !isPodReady(pod) {
					continue
				}
				if _, dup := seen[pod.Name]; dup {
					continue
				}
				seen[pod.Name] = struct{}{}

				port := resolveDCGMPort(pod)
				dcgmPod := DiscoveredPod{
					Name:        pod.Name,
					Namespace:   pod.Namespace,
					MetricsPort: port,
					MetricsPath: "/metrics",
				}
				fetchFuncs = append(fetchFuncs, func(ctx context.Context) (string, error) {
					return d.ScrapeMetrics(ctx, dcgmPod)
				})
			}
		}
	}

	if len(fetchFuncs) == 0 {
		return nil, fmt.Errorf("no DCGM exporter pods found")
	}
	return fetchFuncs, nil
}

// resolveDCGMPort extracts the metrics port from a DCGM exporter pod spec.
func resolveDCGMPort(pod *corev1.Pod) int {
	for _, c := range pod.Spec.Containers {
		for _, p := range c.Ports {
			if p.Name == "metrics" || p.ContainerPort == 9400 {
				return int(p.ContainerPort)
			}
		}
	}
	return 9400
}

// parsePort converts a string to a port number, returning 0 on failure.
func parsePort(s string) int {
	var port int
	_, err := fmt.Sscanf(s, "%d", &port)
	if err != nil || port <= 0 || port > 65535 {
		return 0
	}
	return port
}
