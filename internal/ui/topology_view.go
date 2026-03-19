package ui

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/InfraWhisperer/llmtop/internal/metrics"
	"github.com/InfraWhisperer/llmtop/pkg/config"
)

// WorkerRole identifies whether a worker serves the prefill or decode stage
// in a disaggregated serving pipeline.
type WorkerRole int

const (
	RoleUnknown WorkerRole = iota
	RolePrefill
	RoleDecode
)

// TopologyNode wraps a worker with its resolved role.
type TopologyNode struct {
	Worker *metrics.WorkerMetrics
	Role   WorkerRole
	// FeedsTo lists endpoints this prefill node feeds into.
	FeedsTo []string
}

// TopologyGraph is the fully resolved DAG of prefill → decode workers,
// grouped by model.
type TopologyGraph struct {
	// ModelGroups maps model name → list of topology nodes.
	ModelGroups map[string][]TopologyNode
	// Edges maps prefill endpoint → list of decode endpoints it feeds.
	Edges map[string][]string
	// Summary stats
	AvgCacheHitRate float64
	Detected        bool
}

// TopologyConfig from cluster.yaml defines explicit prefill/decode topology.
type TopologyConfig = config.TopologyEntry

// BuildTopology constructs the disaggregated pipeline graph from workers and
// optional explicit topology config. When no explicit config is provided,
// it auto-detects roles from labels, naming conventions, and metric profiles.
func BuildTopology(workers []*metrics.WorkerMetrics, topoConfig []config.TopologyEntry) TopologyGraph {
	g := TopologyGraph{
		ModelGroups: make(map[string][]TopologyNode),
		Edges:       make(map[string][]string),
	}

	if len(workers) == 0 {
		return g
	}

	// Index workers by endpoint for fast lookup.
	byEndpoint := make(map[string]*metrics.WorkerMetrics, len(workers))
	for _, w := range workers {
		byEndpoint[w.Endpoint] = w
	}

	// Phase 1: Explicit config takes priority.
	if len(topoConfig) > 0 {
		g.Detected = true
		for _, entry := range topoConfig {
			role := parseRole(entry.Role)
			for _, ep := range entry.Endpoints {
				w := byEndpoint[ep]
				if w == nil {
					continue
				}
				node := TopologyNode{Worker: w, Role: role, FeedsTo: entry.Feeds}
				model := w.ModelName
				if model == "" {
					model = "Unknown"
				}
				g.ModelGroups[model] = append(g.ModelGroups[model], node)
				if role == RolePrefill && len(entry.Feeds) > 0 {
					g.Edges[ep] = entry.Feeds
				}
			}
		}
		g.AvgCacheHitRate = computeAvgCacheHit(workers)
		return g
	}

	// Phase 2: Auto-detection from labels/names, then metric heuristics.
	roles := make(map[string]WorkerRole, len(workers))

	// 2a: Label/name-based detection
	for _, w := range workers {
		if r := detectRoleFromLabels(w); r != RoleUnknown {
			roles[w.Endpoint] = r
		}
	}

	// 2b: Metric-based heuristic for unresolved workers.
	// Group by model first to compare within the same model.
	byModel := make(map[string][]*metrics.WorkerMetrics)
	for _, w := range workers {
		if !w.Online {
			continue
		}
		model := w.ModelName
		if model == "" {
			model = "Unknown"
		}
		byModel[model] = append(byModel[model], w)
	}

	for _, modelWorkers := range byModel {
		inferRolesFromMetrics(modelWorkers, roles)
	}

	// Count how many workers have a role assigned.
	prefillCount, decodeCount := 0, 0
	for _, r := range roles {
		if r == RolePrefill {
			prefillCount++
		} else if r == RoleDecode {
			decodeCount++
		}
	}

	// Only mark as detected if we found both prefill and decode workers.
	g.Detected = prefillCount > 0 && decodeCount > 0

	if !g.Detected {
		return g
	}

	// Build graph: every prefill feeds every decode within the same model.
	for model, modelWorkers := range byModel {
		var prefills, decodes []string
		for _, w := range modelWorkers {
			role := roles[w.Endpoint]
			node := TopologyNode{Worker: w, Role: role}
			g.ModelGroups[model] = append(g.ModelGroups[model], node)
			if role == RolePrefill {
				prefills = append(prefills, w.Endpoint)
			} else if role == RoleDecode {
				decodes = append(decodes, w.Endpoint)
			}
		}
		// Assume full mesh: every prefill feeds every decode.
		for _, p := range prefills {
			g.Edges[p] = decodes
		}
	}

	// Add offline/unresolved workers.
	for _, w := range workers {
		if _, ok := roles[w.Endpoint]; ok {
			continue
		}
		if !w.Online {
			model := w.ModelName
			if model == "" {
				model = "Unknown"
			}
			g.ModelGroups[model] = append(g.ModelGroups[model], TopologyNode{Worker: w, Role: RoleUnknown})
		}
	}

	g.AvgCacheHitRate = computeAvgCacheHit(workers)
	return g
}

// detectRoleFromLabels checks worker labels and naming conventions for
// prefill/decode role hints. Checks:
//   - llmtop.dev/role annotation value in the label (e.g. "prefill", "decode")
//   - Pod name patterns: contains "prefill" or "decode"
func detectRoleFromLabels(w *metrics.WorkerMetrics) WorkerRole {
	lower := strings.ToLower(w.Label)
	ep := strings.ToLower(w.Endpoint)

	for _, s := range []string{lower, ep} {
		if strings.Contains(s, "prefill") {
			return RolePrefill
		}
		if strings.Contains(s, "decode") {
			return RoleDecode
		}
	}
	return RoleUnknown
}

// inferRolesFromMetrics uses metric heuristics to classify workers within a
// single model group. Signals:
//   - Prefill: high KV%, high queue depth, zero or near-zero generation tok/s
//   - Decode: lower KV%, lower queue, has generation tok/s
func inferRolesFromMetrics(workers []*metrics.WorkerMetrics, roles map[string]WorkerRole) {
	if len(workers) < 2 {
		return
	}

	// Skip if all workers already have roles.
	unresolved := 0
	for _, w := range workers {
		if roles[w.Endpoint] == RoleUnknown {
			unresolved++
		}
	}
	if unresolved == 0 {
		return
	}

	// Compute averages for classification.
	var totalGenTok, totalKV float64
	var count int
	for _, w := range workers {
		totalGenTok += w.GenTokPerSec
		totalKV += w.KVCacheUsagePct
		count++
	}
	if count == 0 {
		return
	}
	avgGenTok := totalGenTok / float64(count)
	avgKV := totalKV / float64(count)

	// Need variance in the metrics to separate roles.
	// If all workers look identical, don't guess.
	var kvVariance float64
	for _, w := range workers {
		diff := w.KVCacheUsagePct - avgKV
		kvVariance += diff * diff
	}
	kvVariance /= float64(count)
	kvStdDev := math.Sqrt(kvVariance)

	// Require at least 10% KV std deviation or clear gen tok/s split.
	hasGenTokSplit := false
	for _, w := range workers {
		if w.GenTokPerSec < 1.0 && avgGenTok > 10.0 {
			hasGenTokSplit = true
			break
		}
	}

	if kvStdDev < 10 && !hasGenTokSplit {
		return
	}

	for _, w := range workers {
		if roles[w.Endpoint] != RoleUnknown {
			continue
		}

		// Zero generation tok/s is the strongest prefill signal.
		if w.GenTokPerSec < 1.0 && avgGenTok > 10.0 {
			roles[w.Endpoint] = RolePrefill
			continue
		}

		// High KV + high queue → prefill, lower KV + has gen tok/s → decode
		if w.KVCacheUsagePct > avgKV && w.RequestsWaiting > 5 && w.GenTokPerSec < avgGenTok*0.5 {
			roles[w.Endpoint] = RolePrefill
		} else if w.GenTokPerSec > avgGenTok*0.5 {
			roles[w.Endpoint] = RoleDecode
		}
	}
}

func parseRole(s string) WorkerRole {
	switch strings.ToLower(s) {
	case "prefill":
		return RolePrefill
	case "decode":
		return RoleDecode
	default:
		return RoleUnknown
	}
}

func computeAvgCacheHit(workers []*metrics.WorkerMetrics) float64 {
	var sum float64
	var count int
	for _, w := range workers {
		if w.Online && w.CacheHitRatePct > 0 {
			sum += w.CacheHitRatePct
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return sum / float64(count)
}

// RenderTopology renders the full topology view with ASCII DAG and status bar.
func RenderTopology(g TopologyGraph, selectedIdx int, width, height int) string {
	var sb strings.Builder

	if !g.Detected {
		msg := "No prefill/decode topology detected.\nAll workers appear to be running unified serving."
		return lipgloss.PlaceHorizontal(width, lipgloss.Center,
			lipgloss.PlaceVertical(height, lipgloss.Center,
				StyleHelpOverlay.Render(
					StyleHelpTitle.Render("Topology View")+"\n\n"+
						StyleDetailLabel.Render(msg)),
			),
		)
	}

	// Header
	sb.WriteString(StyleHeaderTitle.Render("Topology View — Disaggregated Prefill/Decode Pipeline") + "\n\n")

	// Sort model names for deterministic rendering.
	models := make([]string, 0, len(g.ModelGroups))
	for m := range g.ModelGroups {
		models = append(models, m)
	}
	sort.Strings(models)

	nodeIdx := 0
	for _, model := range models {
		nodes := g.ModelGroups[model]
		sb.WriteString(StyleDetailSection.Render("  Model: "+model) + "\n\n")

		// Separate prefill and decode nodes.
		var prefills, decodes []TopologyNode
		for _, n := range nodes {
			switch n.Role {
			case RolePrefill:
				prefills = append(prefills, n)
			case RoleDecode:
				decodes = append(decodes, n)
			}
		}

		// Render rows: pair prefill[i] with decode[i], extras get
		// edges drawn to first/last.
		maxRows := len(prefills)
		if len(decodes) > maxRows {
			maxRows = len(decodes)
		}

		for row := 0; row < maxRows; row++ {
			leftBox := ""
			rightBox := ""
			edgeStr := "          "

			if row < len(prefills) {
				leftBox = renderNodeBox(prefills[row], nodeIdx == selectedIdx, width/2-8)
				nodeIdx++
			}
			if row < len(decodes) {
				rightBox = renderNodeBox(decodes[row], nodeIdx == selectedIdx, width/2-8)
				nodeIdx++
			}

			if leftBox != "" && rightBox != "" {
				edgeStr = " ────▶ "
			} else if leftBox != "" {
				edgeStr = " ────▶ "
			}

			// Render side by side.
			leftLines := strings.Split(leftBox, "\n")
			rightLines := strings.Split(rightBox, "\n")
			maxLines := len(leftLines)
			if len(rightLines) > maxLines {
				maxLines = len(rightLines)
			}

			leftWidth := width/2 - 8
			for i := 0; i < maxLines; i++ {
				left := ""
				if i < len(leftLines) {
					left = leftLines[i]
				}
				right := ""
				if i < len(rightLines) {
					right = rightLines[i]
				}
				edge := "       "
				if i == 1 && leftBox != "" && rightBox != "" {
					edge = edgeStr
				}

				// Pad left to fixed width.
				leftVisible := lipgloss.Width(left)
				if leftVisible < leftWidth {
					left += strings.Repeat(" ", leftWidth-leftVisible)
				}

				sb.WriteString("  " + left + edge + right + "\n")
			}
			sb.WriteString("\n")
		}
	}

	// Summary bar
	summaryParts := []string{}
	if g.AvgCacheHitRate > 0 {
		summaryParts = append(summaryParts,
			StyleHeaderStat.Render("LMCache: ")+StyleHeaderValue.Render(fmt.Sprintf("%.0f%% hit rate", g.AvgCacheHitRate)))
	}
	if len(summaryParts) > 0 {
		sb.WriteString("\n  " + strings.Join(summaryParts, StyleHeaderDot.Render(" · ")) + "\n")
	}

	return sb.String()
}

// renderNodeBox renders a single topology node as a bordered box with health
// color coding, KV bar graph, and key metrics.
func renderNodeBox(n TopologyNode, selected bool, maxWidth int) string {
	w := n.Worker
	if w == nil {
		return ""
	}

	// Pick a display name.
	name := w.Label
	if name == "" {
		name = strings.TrimPrefix(w.Endpoint, "http://")
		name = strings.TrimPrefix(name, "k8s://")
	}

	roleTag := "?"
	if n.Role == RolePrefill {
		roleTag = "P"
	} else if n.Role == RoleDecode {
		roleTag = "D"
	}

	header := fmt.Sprintf("%s [%s] (%s)", name, roleTag, w.Backend)

	// KV bar graph: 16 chars wide
	barWidth := 16
	kvFilled := int(w.KVCacheUsagePct / 100.0 * float64(barWidth))
	if kvFilled > barWidth {
		kvFilled = barWidth
	}
	kvEmpty := barWidth - kvFilled
	kvBar := strings.Repeat("█", kvFilled) + strings.Repeat("░", kvEmpty)

	statsLine := fmt.Sprintf("KV: %.0f%% Q: %d", w.KVCacheUsagePct, w.RequestsWaiting)
	tokLine := fmt.Sprintf("Tok/s: %.0f", w.PromptTokPerSec+w.GenTokPerSec)

	// Health-based border color.
	borderColor := colorGreen
	if w.KVCacheUsagePct >= 85 || w.RequestsWaiting >= 20 {
		borderColor = colorRed
	} else if w.KVCacheUsagePct >= 70 || w.RequestsWaiting >= 10 {
		borderColor = colorYellow
	}
	if !w.Online {
		borderColor = colorGray
	}

	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor).
		Padding(0, 1).
		Width(maxWidth)

	if selected {
		style = style.BorderForeground(colorCyan).Bold(true)
	}

	// Color the KV bar.
	kvStyle := KVCacheStyle(w.KVCacheUsagePct)

	content := StyleHeaderValue.Render(header) + "\n" +
		StyleDetailLabel.Render(statsLine) + "\n" +
		kvStyle.Render(kvBar) + "\n" +
		StyleDetailLabel.Render(tokLine)

	return style.Render(content)
}

// FlattenTopologyNodes returns all topology nodes in display order
// (prefill then decode per model, sorted by model name).
func FlattenTopologyNodes(g TopologyGraph) []*metrics.WorkerMetrics {
	models := make([]string, 0, len(g.ModelGroups))
	for m := range g.ModelGroups {
		models = append(models, m)
	}
	sort.Strings(models)

	var result []*metrics.WorkerMetrics
	for _, model := range models {
		nodes := g.ModelGroups[model]
		// Prefills first, then decodes.
		for _, n := range nodes {
			if n.Role == RolePrefill {
				result = append(result, n.Worker)
			}
		}
		for _, n := range nodes {
			if n.Role == RoleDecode {
				result = append(result, n.Worker)
			}
		}
		for _, n := range nodes {
			if n.Role == RoleUnknown {
				result = append(result, n.Worker)
			}
		}
	}
	return result
}
