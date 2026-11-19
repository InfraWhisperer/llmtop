// Package metrics provides a hand-rolled Prometheus text exposition format parser.
// No external library is used; this implements just enough to parse gauge and histogram metrics.
package metrics

import (
	"bufio"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// MetricType represents a Prometheus metric type.
type MetricType int

const (
	MetricTypeUnknown MetricType = iota
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
		if baseName, ok := strings.CutSuffix(sample.Name, "_bucket"); ok {
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
		} else if baseName, ok := strings.CutSuffix(sample.Name, "_count"); ok {
			// Filter out le label for histogram lookup
			labelsWithoutLE := filterLabel(sample.Labels, "le")
			key := histogramKey(baseName, labelsWithoutLE)
			h := getOrCreateHistogram(pm, key, baseName, labelsWithoutLE)
			h.Count = sample.Value
		} else if baseName, ok := strings.CutSuffix(sample.Name, "_sum"); ok {
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

// GetGaugeSum sums every sample matching name, optionally filtered by label
// key=value pairs. Counters exported as multiple series (e.g. per is_streaming
// or model_name label) must be summed before taking deltas: returning an
// arbitrary single series yields frozen or wrong rates (issue #15).
// Returns (sum, true) if at least one series matched, (0, false) otherwise.
func (pm *ParsedMetrics) GetGaugeSum(name string, labelFilter map[string]string) (float64, bool) {
	var sum float64
	found := false
	for _, s := range pm.Samples {
		if s.Name == name && matchLabels(s.Labels, labelFilter) {
			sum += s.Value
			found = true
		}
	}
	return sum, found
}

// GaugeSeries returns every series of a metric as a map from a canonical
// rendering of the sample's label set to its value, plus the sum over those
// series and whether any series matched. A nil keep accepts every series of the
// metric; otherwise keep(labels) selects them.
//
// Rate collectors take their deltas through this rather than GetGaugeSum: with
// the summed form, one restarted series (a vLLM engine whose counter resets to
// 0 while its siblings keep counting) can cancel the growth of all the others
// and collapse the whole rate window to zero (issue #15 follow-up).
func (pm *ParsedMetrics) GaugeSeries(name string, keep func(labels map[string]string) bool) (map[string]float64, float64, bool) {
	values := map[string]float64{}
	var sum float64
	found := false
	for _, s := range pm.Samples {
		if s.Name != name || (keep != nil && !keep(s.Labels)) {
			continue
		}
		values[seriesKey(s.Labels)] += s.Value
		sum += s.Value
		found = true
	}
	return values, sum, found
}

// GaugeSeriesFiltered is GaugeSeries with an equality label filter, mirroring
// the labelFilter argument of GetGauge/GetGaugeSum.
func (pm *ParsedMetrics) GaugeSeriesFiltered(name string, labelFilter map[string]string) (map[string]float64, float64, bool) {
	if len(labelFilter) == 0 {
		return pm.GaugeSeries(name, nil)
	}
	return pm.GaugeSeries(name, func(labels map[string]string) bool {
		return matchLabels(labels, labelFilter)
	})
}

// MergeGaugeSeries combines per-series reads of the same counter taken with
// different label filters (e.g. SGLang realtime_tokens_total selected by mode).
// Series keys are derived from the full label set, so sets from disjoint filters
// never collide; the sums add up.
func MergeGaugeSeries(a map[string]float64, aSum float64, b map[string]float64, bSum float64) (map[string]float64, float64, bool) {
	if a == nil {
		a = map[string]float64{}
	}
	found := len(a) > 0
	for k, v := range b {
		a[k] += v
		found = true
	}
	return a, aSum + bSum, found
}

// seriesKey renders a label set as a stable map key: sorted `k=v,` pairs. Two
// polls of the same series therefore key identically regardless of the order the
// exposition happened to print its labels in.
func seriesKey(labels map[string]string) string {
	if len(labels) == 0 {
		return ""
	}
	keys := make([]string, 0, len(labels))
	for k := range labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, k := range keys {
		sb.WriteString(k)
		sb.WriteByte('=')
		sb.WriteString(labels[k])
		sb.WriteByte(',')
	}
	return sb.String()
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
	for _, h := range pm.Histograms {
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

// GetHistogramBucketsAny returns the buckets for a named histogram (any labels).
// Returns (sortedBuckets, totalCount, found). Buckets are sorted by UpperBound asc.
// First match wins for histograms with multiple label sets.
func (pm *ParsedMetrics) GetHistogramBucketsAny(name string) ([]HistogramBucket, float64, bool) {
	for _, h := range pm.Histograms {
		if h.Name == name {
			out := make([]HistogramBucket, len(h.Buckets))
			copy(out, h.Buckets)
			sort.Slice(out, func(i, j int) bool {
				return out[i].UpperBound < out[j].UpperBound
			})
			return out, h.Count, true
		}
	}
	return nil, 0, false
}

// GetAllGaugesByLabel returns all samples for a metric, grouped by the value of
// labelKey. Result maps each distinct label value to the first matching sample
// value encountered. Samples missing labelKey are skipped.
func (pm *ParsedMetrics) GetAllGaugesByLabel(name, labelKey string) map[string]float64 {
	out := make(map[string]float64)
	for _, s := range pm.Samples {
		if s.Name != name {
			continue
		}
		v, ok := s.Labels[labelKey]
		if !ok {
			continue
		}
		if _, exists := out[v]; exists {
			continue
		}
		out[v] = s.Value
	}
	return out
}

// cdfBucketCountAtLatency returns the cumulative request count at a target
// latency (in seconds) by linearly interpolating across buckets sorted asc by
// UpperBound. buckets must be sorted ascending; totalCount is the histogram's
// _count.
//
// Special case: a histogram with only a +Inf bucket carries no distribution
// information — the only signal is that all observed requests completed at
// some latency. Return totalCount for any non-negative target so the resulting
// CDF is all-1.0, matching spec F2 edge case 1.
func cdfBucketCountAtLatency(buckets []HistogramBucket, totalCount, targetSec float64) float64 {
	if len(buckets) == 0 || totalCount <= 0 {
		return 0
	}
	if len(buckets) == 1 && math.IsInf(buckets[0].UpperBound, 1) {
		return totalCount
	}
	if targetSec <= 0 {
		// All finite buckets cover [0, ub]; the count below 0 is 0.
		return 0
	}
	// Walk buckets; find the first bucket whose UpperBound >= targetSec.
	var prevUB, prevCount float64
	for _, b := range buckets {
		ub := b.UpperBound
		if math.IsInf(ub, 1) {
			// +Inf bucket: cumulative count is the total. If targetSec is finite
			// and we got here, target lies between prevUB and +Inf — saturate at
			// totalCount.
			return totalCount
		}
		if ub >= targetSec {
			if b.Count <= prevCount {
				return prevCount
			}
			frac := (targetSec - prevUB) / (ub - prevUB)
			return prevCount + frac*(b.Count-prevCount)
		}
		prevUB = ub
		prevCount = b.Count
	}
	// targetSec exceeds all finite buckets — saturate.
	return totalCount
}

// ComputeCDFPoints samples 10 evenly-spaced latency thresholds in
// [0, maxLatencyMs] and returns the cumulative request fraction at each.
// buckets are histogram buckets with UpperBound in seconds; totalCount is
// the histogram _count. Returns a zero array when maxLatencyMs <= 0 or
// totalCount <= 0. Output is monotonically non-decreasing and clamped to
// [0.0, 1.0].
func ComputeCDFPoints(buckets []HistogramBucket, totalCount float64, maxLatencyMs float64) [10]float64 {
	var out [10]float64
	if maxLatencyMs <= 0 || totalCount <= 0 || len(buckets) == 0 {
		return out
	}
	sorted := make([]HistogramBucket, len(buckets))
	copy(sorted, buckets)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].UpperBound < sorted[j].UpperBound
	})
	step := maxLatencyMs / 9.0
	var prev float64
	for i := range 10 {
		latencyMs := step * float64(i)
		latencySec := latencyMs / 1000.0
		count := cdfBucketCountAtLatency(sorted, totalCount, latencySec)
		frac := count / totalCount
		if frac < 0 {
			frac = 0
		}
		if frac > 1 {
			frac = 1
		}
		// Enforce monotonic non-decreasing in the face of bucket sparseness or
		// floating-point jitter.
		if frac < prev {
			frac = prev
		}
		out[i] = frac
		prev = frac
	}
	return out
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
	for _, s := range slices.Backward(sorted) {
		if !math.IsInf(s.UpperBound, 1) {
			return s.UpperBound
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
	if strings.Contains(line, "{") {
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
