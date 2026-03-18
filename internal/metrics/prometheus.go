// Package metrics provides a hand-rolled Prometheus text exposition format parser.
// No external library is used; this implements just enough to parse gauge and histogram metrics.
package metrics

import (
	"bufio"
	"math"
	"sort"
	"strconv"
	"strings"
)

// MetricType represents a Prometheus metric type.
type MetricType int

const (
	MetricTypeUnknown   MetricType = iota
	MetricTypeGauge
	MetricTypeCounter
	MetricTypeHistogram
	MetricTypeSummary
)

// Sample represents a single Prometheus metric sample.
type Sample struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// HistogramBucket represents a single bucket in a histogram.
type HistogramBucket struct {
	UpperBound float64
	Count      float64
}

// ParsedMetrics holds all parsed data from a Prometheus text format response.
type ParsedMetrics struct {
	Samples    []Sample
	Histograms map[string]*ParsedHistogram
}

// ParsedHistogram holds the raw histogram data.
type ParsedHistogram struct {
	Name    string
	Labels  map[string]string
	Buckets []HistogramBucket
	Count   float64
	Sum     float64
}

// ParseText parses the Prometheus text exposition format from a string.
func ParseText(text string) *ParsedMetrics {
	pm := &ParsedMetrics{
		Histograms: make(map[string]*ParsedHistogram),
	}

	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			// Skip comments and empty lines
			continue
		}

		sample, ok := parseSampleLine(line)
		if !ok {
			continue
		}

		// Check if this is a histogram component
		if strings.HasSuffix(sample.Name, "_bucket") {
			baseName := strings.TrimSuffix(sample.Name, "_bucket")
			key := histogramKey(baseName, sample.Labels)
			h := getOrCreateHistogram(pm, key, baseName, sample.Labels)
			if le, ok := sample.Labels["le"]; ok {
				ub, err := strconv.ParseFloat(le, 64)
				if err == nil {
					h.Buckets = append(h.Buckets, HistogramBucket{
						UpperBound: ub,
						Count:      sample.Value,
					})
				}
			}
		} else if strings.HasSuffix(sample.Name, "_count") {
			baseName := strings.TrimSuffix(sample.Name, "_count")
			// Filter out le label for histogram lookup
			labelsWithoutLE := filterLabel(sample.Labels, "le")
			key := histogramKey(baseName, labelsWithoutLE)
			h := getOrCreateHistogram(pm, key, baseName, labelsWithoutLE)
			h.Count = sample.Value
		} else if strings.HasSuffix(sample.Name, "_sum") {
			baseName := strings.TrimSuffix(sample.Name, "_sum")
			labelsWithoutLE := filterLabel(sample.Labels, "le")
			key := histogramKey(baseName, labelsWithoutLE)
			h := getOrCreateHistogram(pm, key, baseName, labelsWithoutLE)
			h.Sum = sample.Value
		} else {
			pm.Samples = append(pm.Samples, sample)
		}
	}

	return pm
}

// GetGauge returns the value of a named gauge metric, optionally filtering by label key=value pairs.
// Returns (value, true) if found, (0, false) if not found.
func (pm *ParsedMetrics) GetGauge(name string, labelFilter map[string]string) (float64, bool) {
	for _, s := range pm.Samples {
		if s.Name == name && matchLabels(s.Labels, labelFilter) {
			return s.Value, true
		}
	}
	return 0, false
}

// GetGaugeAny returns the first matching gauge value.
func (pm *ParsedMetrics) GetGaugeAny(name string) (float64, map[string]string, bool) {
	for _, s := range pm.Samples {
		if s.Name == name {
			return s.Value, s.Labels, true
		}
	}
	return 0, nil, false
}

// GetHistogramQuantile returns the estimated quantile (0.0-1.0) for a histogram metric.
func (pm *ParsedMetrics) GetHistogramQuantile(name string, labelFilter map[string]string, quantile float64) (float64, bool) {
	for key, h := range pm.Histograms {
		_ = key
		if h.Name == name && matchLabels(h.Labels, labelFilter) {
			return estimateQuantile(h.Buckets, h.Count, quantile), true
		}
	}
	return 0, false
}

// GetHistogramQuantileAny returns the estimated quantile for a histogram metric (any labels).
func (pm *ParsedMetrics) GetHistogramQuantileAny(name string, quantile float64) (float64, bool) {
	for _, h := range pm.Histograms {
		if h.Name == name {
			return estimateQuantile(h.Buckets, h.Count, quantile), true
		}
	}
	return 0, false
}

// GetHistogramSumAny returns the _sum value for a histogram metric (any labels).
// Useful for computing rates from cumulative counters like token counts.
func (pm *ParsedMetrics) GetHistogramSumAny(name string) (float64, bool) {
	for _, h := range pm.Histograms {
		if h.Name == name {
			return h.Sum, true
		}
	}
	return 0, false
}

// estimateQuantile estimates a quantile value from histogram buckets using linear interpolation.
func estimateQuantile(buckets []HistogramBucket, totalCount float64, quantile float64) float64 {
	if len(buckets) == 0 || totalCount == 0 {
		return 0
	}

	// Sort buckets by upper bound
	sorted := make([]HistogramBucket, len(buckets))
	copy(sorted, buckets)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].UpperBound < sorted[j].UpperBound
	})

	target := quantile * totalCount

	for i, b := range sorted {
		if b.Count >= target {
			if i == 0 {
				return b.UpperBound * (target / b.Count)
			}
			prev := sorted[i-1]
			// Linear interpolation between prev upper bound and current upper bound
			prevCount := prev.Count
			bucketCount := b.Count - prevCount
			if bucketCount <= 0 {
				return prev.UpperBound
			}
			fraction := (target - prevCount) / bucketCount
			lower := prev.UpperBound
			if math.IsInf(lower, 1) {
				lower = 0
			}
			upper := b.UpperBound
			if math.IsInf(upper, 1) {
				// Use last finite upper bound
				upper = prev.UpperBound * 2
			}
			return lower + fraction*(upper-lower)
		}
	}

	// Return last finite upper bound
	for i := len(sorted) - 1; i >= 0; i-- {
		if !math.IsInf(sorted[i].UpperBound, 1) {
			return sorted[i].UpperBound
		}
	}
	return 0
}

// parseSampleLine parses a single metric line: metric_name{labels} value [timestamp]
func parseSampleLine(line string) (Sample, bool) {
	s := Sample{Labels: make(map[string]string)}

	// Split off timestamp (optional)
	// Format: name{labels} value [timestamp]
	// or: name value [timestamp]

	// Find the last space-separated value (the metric value)
	// Labels may contain spaces inside quotes, so we need to be careful
	var nameAndLabels, valueStr string

	// Find where labels end: look for } or first space if no labels
	if idx := strings.Index(line, "{"); idx >= 0 {
		closeIdx := strings.Index(line, "}")
		if closeIdx < 0 {
			return s, false
		}
		rest := strings.TrimSpace(line[closeIdx+1:])
		parts := strings.Fields(rest)
		if len(parts) == 0 {
			return s, false
		}
		valueStr = parts[0]
		nameAndLabels = line[:closeIdx+1]
	} else {
		parts := strings.Fields(line)
		if len(parts) < 2 {
			return s, false
		}
		nameAndLabels = parts[0]
		valueStr = parts[1]
	}

	// Parse value
	val, err := strconv.ParseFloat(valueStr, 64)
	if err != nil {
		return s, false
	}
	s.Value = val

	// Parse name and labels
	if idx := strings.Index(nameAndLabels, "{"); idx >= 0 {
		s.Name = nameAndLabels[:idx]
		labelStr := nameAndLabels[idx+1 : len(nameAndLabels)-1]
		s.Labels = parseLabels(labelStr)
	} else {
		s.Name = nameAndLabels
	}

	s.Name = strings.TrimSpace(s.Name)
	return s, s.Name != ""
}

// parseLabels parses a comma-separated label string: key="value",key2="value2"
func parseLabels(s string) map[string]string {
	labels := make(map[string]string)
	if s == "" {
		return labels
	}

	// Simple state machine parser
	i := 0
	for i < len(s) {
		// Skip whitespace
		for i < len(s) && (s[i] == ' ' || s[i] == ',') {
			i++
		}
		if i >= len(s) {
			break
		}

		// Read key
		keyStart := i
		for i < len(s) && s[i] != '=' {
			i++
		}
		if i >= len(s) {
			break
		}
		key := strings.TrimSpace(s[keyStart:i])
		i++ // skip '='

		// Read value (quoted)
		if i >= len(s) {
			break
		}
		if s[i] == '"' {
			i++ // skip opening quote
			valStart := i
			for i < len(s) && s[i] != '"' {
				if s[i] == '\\' {
					i++ // skip escape
				}
				i++
			}
			val := s[valStart:i]
			if i < len(s) {
				i++ // skip closing quote
			}
			labels[key] = val
		} else {
			// Unquoted value
			valStart := i
			for i < len(s) && s[i] != ',' {
				i++
			}
			labels[key] = strings.TrimSpace(s[valStart:i])
		}
	}

	return labels
}

// matchLabels returns true if all filterLabels key=value pairs match the given label map.
func matchLabels(labels, filter map[string]string) bool {
	for k, v := range filter {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// histogramKey generates a unique key for a histogram based on name and labels (excluding 'le').
func histogramKey(name string, labels map[string]string) string {
	var parts []string
	for k, v := range labels {
		if k != "le" {
			parts = append(parts, k+"="+v)
		}
	}
	sort.Strings(parts)
	return name + "{" + strings.Join(parts, ",") + "}"
}

// filterLabel returns a copy of labels without the specified key.
func filterLabel(labels map[string]string, key string) map[string]string {
	result := make(map[string]string, len(labels))
	for k, v := range labels {
		if k != key {
			result[k] = v
		}
	}
	return result
}

// getOrCreateHistogram retrieves or creates a ParsedHistogram entry.
func getOrCreateHistogram(pm *ParsedMetrics, key, name string, labels map[string]string) *ParsedHistogram {
	if h, ok := pm.Histograms[key]; ok {
		return h
	}
	h := &ParsedHistogram{
		Name:   name,
		Labels: filterLabel(labels, "le"),
	}
	pm.Histograms[key] = h
	return h
}
