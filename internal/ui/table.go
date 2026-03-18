package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// Fixed column widths for numeric/compact columns
const (
	colBackend = 8
	colKV      = 6
	colQueue   = 6
	colRun     = 5
	colTTFT    = 10
	colITL     = 10
	colHit     = 6
	colTok     = 7
)

// fixedWidth is the sum of all fixed columns + inter-column spaces + left margin + dot.
// Layout: "  " (2) + dot (1) + " " (1) + ep + " " + backend + " " + model + " " + kv + " " + queue + " " + run + " " + ttft + " " + itl + " " + hit + " " + tok
// Overhead: 2 (margin) + 1 (dot) + 1 (space) + 9 (inter-column spaces) = 13
var fixedWidth = colBackend + colKV + colQueue + colRun + colTTFT + colITL + colHit + colTok + 13

// flexWidths computes dynamic ENDPOINT and MODEL column widths from terminal width.
func flexWidths(termWidth int) (epW, modelW int) {
	avail := termWidth - fixedWidth
	if avail < 30 {
		avail = 30
	}
	epW = avail * 55 / 100
	modelW = avail - epW
	if epW < 15 {
		epW = 15
	}
	if modelW < 10 {
		modelW = 10
	}
	return epW, modelW
}

// SortColumn represents a column that can be sorted.
type SortColumn int

const (
	SortNone    SortColumn = iota
	SortKVCache
	SortQueue
	SortTTFT
	SortHitRate
	SortTokPerSec
)

// SortColumnName returns a human-readable name for the sort column.
func SortColumnName(s SortColumn) string {
	switch s {
	case SortKVCache:
		return "KV%"
	case SortQueue:
		return "Queue"
	case SortTTFT:
		return "TTFT P99"
	case SortHitRate:
		return "Hit%"
	case SortTokPerSec:
		return "Tok/s"
	default:
		return "-"
	}
}

// RenderTable renders the worker metrics table.
func RenderTable(workers []*metrics.WorkerMetrics, selectedIdx int, sortCol SortColumn, filterBackend metrics.Backend, width int) string {
	var sb strings.Builder
	epW, modelW := flexWidths(width)

	header := renderTableHeader(sortCol, epW, modelW)
	sb.WriteString(header)
	sb.WriteString("\n")

	sep := StyleTableSeparator.Render(strings.Repeat("-", max(width-4, 80)))
	sb.WriteString("    " + sep)
	sb.WriteString("\n")

	for i, w := range workers {
		if filterBackend != "" && w.Backend != filterBackend && filterBackend != metrics.BackendUnknown {
			continue
		}
		row := renderWorkerRow(w, i == selectedIdx, epW, modelW)
		sb.WriteString(row)
		sb.WriteString("\n")
	}

	return sb.String()
}

func renderTableHeader(sortCol SortColumn, epW, modelW int) string {
	cols := []struct {
		name  string
		width int
		col   SortColumn
	}{
		{"ENDPOINT", epW, SortNone},
		{"BACKEND", colBackend, SortNone},
		{"MODEL", modelW, SortNone},
		{"KV%", colKV, SortKVCache},
		{"QUEUE", colQueue, SortQueue},
		{"RUN", colRun, SortNone},
		{"TTFT P99", colTTFT, SortTTFT},
		{"ITL P99", colITL, SortNone},
		{"HIT%", colHit, SortHitRate},
		{"TOK/S", colTok, SortTokPerSec},
	}

	var parts []string
	for _, c := range cols {
		text := padRight(c.name, c.width)
		if c.col != SortNone && c.col == sortCol {
			parts = append(parts, StyleSortIndicator.Render(text))
		} else {
			parts = append(parts, StyleTableHeader.Render(text))
		}
	}
	// 4-char prefix: "  " (margin) + "  " (dot placeholder) to align with data rows
	return "    " + strings.Join(parts, " ")
}

func renderWorkerRow(w *metrics.WorkerMetrics, selected bool, epW, modelW int) string {
	var dot string
	if w.Online {
		dot = StyleDotOnline.Render("●")
	} else {
		dot = StyleDotOffline.Render("○")
	}

	// Endpoint display: K8s workers show pod name, others show URL
	var ep string
	if strings.HasPrefix(w.Endpoint, "k8s://") {
		ep = w.Label
		if ep == "" {
			ep = strings.TrimPrefix(w.Endpoint, "k8s://")
		}
	} else {
		ep = strings.TrimPrefix(w.Endpoint, "http://")
		ep = strings.TrimPrefix(ep, "https://")
		if w.Label != "" {
			ep = ep + " (" + w.Label + ")"
		}
	}
	epStr := padRight(truncate(ep, epW), epW)

	model := w.ModelName
	if model == "" {
		model = "-"
	}
	modelStr := padRight(truncate(model, modelW), modelW)

	if !w.Online {
		// Offline row: all metric columns show "Err", no per-cell styling needed
		backendStr := padRight(string(w.Backend), colBackend)
		row := "  " + dot + " " + epStr + " " + backendStr + " " + modelStr + " " +
			padRight("Err", colKV) + " " + padRight("Err", colQueue) + " " + padRight("Err", colRun) + " " +
			padRight("Err", colTTFT) + " " + padRight("Err", colITL) + " " +
			padRight("Err", colHit) + " " + padRight("Err", colTok)
		if selected {
			return StyleTableRowSelected.Render(row)
		}
		return StyleTableRowOffline.Render(row)
	}

	// Format all values as plain padded strings first — style applied at the end
	backendPlain := padRight(string(w.Backend), colBackend)
	kvPlain := formatKVCache(w.KVCacheUsagePct, colKV)
	queuePlain := formatQueue(w.RequestsWaiting, colQueue)
	runPlain := padRight(fmt.Sprintf("%d", w.RequestsRunning), colRun)
	ttftPlain := formatTTFT(w.TTFT_P99, colTTFT)
	itlPlain := formatTTFT(w.ITL_P99, colITL)
	hitPlain := formatHitRate(w.CacheHitRatePct, colHit)
	tokPlain := formatTokPerSec(w.GenTokPerSec+w.PromptTokPerSec, colTok)

	if selected {
		plain := "  " + dot + " " + epStr + " " + backendPlain + " " + modelStr + " " +
			kvPlain + " " + queuePlain + " " + runPlain + " " + ttftPlain + " " + itlPlain + " " +
			hitPlain + " " + tokPlain
		return StyleTableRowSelected.Render(plain)
	}

	// Unselected: apply per-cell color styles
	return "  " + dot + " " + epStr + " " +
		backendStyle(w.Backend).Render(backendPlain) + " " +
		modelStr + " " +
		kvCacheStyle(w.KVCacheUsagePct, kvPlain) + " " +
		queueStyle(w.RequestsWaiting, queuePlain) + " " +
		runPlain + " " +
		ttftStyle(w.TTFT_P99, ttftPlain) + " " +
		ttftStyle(w.ITL_P99, itlPlain) + " " +
		hitRateStyle(w.CacheHitRatePct, hitPlain) + " " +
		tokPerSecStyle(w.GenTokPerSec+w.PromptTokPerSec, tokPlain)
}

// --- Plain formatting functions (no ANSI, just padded strings) ---

func formatKVCache(pct float64, width int) string {
	if pct == 0 {
		return padRight("-", width)
	}
	return padRight(fmt.Sprintf("%.0f%%", pct), width)
}

func formatQueue(n int, width int) string {
	return padRight(fmt.Sprintf("%d", n), width)
}

func formatTTFT(ms float64, width int) string {
	if ms == 0 {
		return padRight("-", width)
	}
	return padRight(fmt.Sprintf("%.0fms", ms), width)
}

func formatHitRate(pct float64, width int) string {
	if pct == 0 {
		return padRight("-", width)
	}
	return padRight(fmt.Sprintf("%.0f%%", pct), width)
}

func formatTokPerSec(tok float64, width int) string {
	if tok == 0 {
		return padRight("-", width)
	}
	return padRight(fmt.Sprintf("%.0f", tok), width)
}

// --- Style application functions (take plain string, return styled) ---

func backendStyle(b metrics.Backend) lipgloss.Style {
	switch b {
	case metrics.BackendVLLM:
		return StyleBadgeVLLM
	case metrics.BackendSGLang:
		return StyleBadgeSGLang
	case metrics.BackendLMCache:
		return StyleBadgeLMCache
	case metrics.BackendNIM:
		return StyleBadgeNIM
	case metrics.BackendTGI:
		return StyleBadgeTGI
	case metrics.BackendTRTLLM:
		return StyleBadgeTRTLLM
	case metrics.BackendTriton:
		return StyleBadgeTriton
	case metrics.BackendLlamaCpp:
		return StyleBadgeLlamaCpp
	case metrics.BackendLiteLLM:
		return StyleBadgeLiteLLM
	default:
		return StyleBadgeUnknown
	}
}

func kvCacheStyle(pct float64, plain string) string {
	if pct == 0 {
		return StyleMetricNA.Render(plain)
	}
	return KVCacheStyle(pct).Render(plain)
}

func queueStyle(n int, plain string) string {
	if n == 0 {
		return StyleMetricGood.Render(plain)
	}
	return QueueStyle(n).Render(plain)
}

func ttftStyle(ms float64, plain string) string {
	if ms == 0 {
		return StyleMetricNA.Render(plain)
	}
	return TTFTStyle(ms).Render(plain)
}

func hitRateStyle(pct float64, plain string) string {
	if pct == 0 {
		return StyleMetricNA.Render(plain)
	}
	return StyleMetricGood.Render(plain)
}

func tokPerSecStyle(tok float64, plain string) string {
	if tok == 0 {
		return StyleMetricNA.Render(plain)
	}
	return StyleMetricGood.Render(plain)
}

// --- Width-correct string utilities ---

// padRight pads a string with spaces to the given display width.
// Uses runewidth for correct handling of multi-byte characters (em-dash, CJK, etc).
func padRight(s string, n int) string {
	w := runewidth.StringWidth(s)
	if w >= n {
		return s
	}
	return s + strings.Repeat(" ", n-w)
}

// truncate truncates a string to fit within n display columns.
// Uses runewidth for correct handling of multi-byte characters.
func truncate(s string, n int) string {
	w := runewidth.StringWidth(s)
	if w <= n {
		return s
	}
	if n > 3 {
		return runewidth.Truncate(s, n-3, "") + "..."
	}
	return runewidth.Truncate(s, n, "")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
