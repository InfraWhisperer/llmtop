package ui

import (
	"fmt"
	"strings"

	"github.com/InfraWhisperer/llmtop/internal/metrics"
)

// Model-grouped view column widths (fixed columns).
const (
	colMGBackend  = 8
	colMGWorkers  = 12
	colMGTok      = 8
	colMGAvgKV    = 8
	colMGQueue    = 7
	colMGRunning  = 8
	colMGAvgTTFT  = 10
	colMGAvgHit   = 8
)

// mgFixedWidth is the sum of all fixed model-group columns plus inter-column spaces and left margin.
// Columns: BACKEND(8) WORKERS(12) TOK/S(8) AVG KV%(8) QUEUE(7) RUNNING(8) AVG TTFT(10) AVG HIT%(8)
// Spaces between 8 cols = 7, plus 2-char left margin = 9 total overhead.
var mgFixedWidth = colMGBackend + colMGWorkers + colMGTok + colMGAvgKV + colMGQueue + colMGRunning + colMGAvgTTFT + colMGAvgHit + 9

// ModelSortColumn represents a column that can be sorted in the model-grouped view.
type ModelSortColumn int

const (
	ModelSortNone     ModelSortColumn = iota
	ModelSortName
	ModelSortTokPerSec
	ModelSortAvgKV
	ModelSortQueue
	ModelSortRunning
	ModelSortAvgTTFT
)

var modelSortCycle = []ModelSortColumn{
	ModelSortNone,
	ModelSortName,
	ModelSortTokPerSec,
	ModelSortAvgKV,
	ModelSortQueue,
	ModelSortRunning,
	ModelSortAvgTTFT,
}

// ModelSortColumnName returns a human-readable name for the model sort column.
func ModelSortColumnName(s ModelSortColumn) string {
	switch s {
	case ModelSortName:
		return "Model"
	case ModelSortTokPerSec:
		return "Tok/s"
	case ModelSortAvgKV:
		return "Avg KV%"
	case ModelSortQueue:
		return "Queue"
	case ModelSortRunning:
		return "Running"
	case ModelSortAvgTTFT:
		return "Avg TTFT"
	default:
		return "-"
	}
}

// mgModelWidth computes the flex MODEL column width given terminal width.
func mgModelWidth(termWidth int) int {
	w := termWidth - mgFixedWidth
	if w < 12 {
		w = 12
	}
	return w
}

// RenderModelHeader renders the fleet header for the model-grouped view.
func RenderModelHeader(groups []metrics.ModelGroup, version string, intervalSec int, width int) string {
	title := StyleHeaderTitle.Render(" llmtop " + version + " — Models")

	totalWorkers := 0
	for _, g := range groups {
		totalWorkers += g.Workers
	}
	countStr := StyleHeaderStat.Render(
		fmt.Sprintf("%d models across %d workers", len(groups), totalWorkers),
	)

	var totalTok float64
	for _, g := range groups {
		totalTok += g.TotalTokPerSec
	}
	tokStr := StyleHeaderValue.Render(fmt.Sprintf("%.0f tok/s", totalTok))

	interval := StyleHeaderStat.Render(fmt.Sprintf("↻ %ds", intervalSec))
	dot := StyleHeaderDot.Render(" · ")

	parts := []string{title, dot, countStr, dot, tokStr, dot, interval}

	var header strings.Builder
	for _, p := range parts {
		header.WriteString(p)
	}

	return renderHeaderBar(header.String(), width)
}

// RenderModelTable renders the model-grouped metrics table.
func RenderModelTable(groups []metrics.ModelGroup, selectedIdx int, sortCol ModelSortColumn, width int) string {
	var sb strings.Builder
	modelW := mgModelWidth(width)

	header := renderModelTableHeader(sortCol, modelW)
	sb.WriteString(header)
	sb.WriteString("\n")

	sep := StyleTableSeparator.Render(strings.Repeat("─", max(width-2, 80)))
	sb.WriteString("  " + sep)
	sb.WriteString("\n")

	for i, g := range groups {
		row := renderModelGroupRow(g, i == selectedIdx, modelW)
		sb.WriteString(row)
		sb.WriteString("\n")
	}

	return sb.String()
}

func renderModelTableHeader(sortCol ModelSortColumn, modelW int) string {
	cols := []struct {
		name  string
		width int
		col   ModelSortColumn
	}{
		{"MODEL", modelW, ModelSortName},
		{"BACKEND", colMGBackend, ModelSortNone},
		{"WORKERS", colMGWorkers, ModelSortNone},
		{"TOK/S", colMGTok, ModelSortTokPerSec},
		{"AVG KV%", colMGAvgKV, ModelSortAvgKV},
		{"QUEUE", colMGQueue, ModelSortQueue},
		{"RUNNING", colMGRunning, ModelSortRunning},
		{"AVG TTFT", colMGAvgTTFT, ModelSortAvgTTFT},
		{"AVG HIT%", colMGAvgHit, ModelSortNone},
	}

	var parts []string
	for _, c := range cols {
		text := padRight(c.name, c.width)
		if c.col != ModelSortNone && c.col == sortCol {
			parts = append(parts, StyleSortIndicator.Render(text))
		} else {
			parts = append(parts, StyleTableHeader.Render(text))
		}
	}
	return "  " + strings.Join(parts, " ")
}

func renderModelGroupRow(g metrics.ModelGroup, selected bool, modelW int) string {
	modelStr := padRight(truncate(g.ModelName, modelW), modelW)
	backendPlain := padRight(string(g.Backend), colMGBackend)
	workersStr := padRight(fmt.Sprintf("%d/%d", g.OnlineWorkers, g.Workers), colMGWorkers)
	tokPlain := formatTokPerSec(g.TotalTokPerSec, colMGTok)
	kvPlain := formatKVCache(g.AvgKVCachePct, colMGAvgKV)
	queuePlain := formatQueue(g.TotalQueue, colMGQueue)
	runningStr := padRight(fmt.Sprintf("%d", g.TotalRunning), colMGRunning)
	ttftPlain := formatTTFT(g.AvgTTFTP99, colMGAvgTTFT)
	hitPlain := formatHitRate(g.AvgHitRate, colMGAvgHit)

	if selected {
		plain := "  " + modelStr + " " + backendPlain + " " +
			workersStr + " " + tokPlain + " " + kvPlain + " " +
			queuePlain + " " + runningStr + " " + ttftPlain + " " +
			hitPlain
		return StyleTableRowSelected.Render(plain)
	}

	return "  " + modelStr + " " +
		backendStyle(g.Backend).Render(backendPlain) + " " +
		workersStr + " " +
		tokPerSecStyle(g.TotalTokPerSec, tokPlain) + " " +
		kvCacheStyle(g.AvgKVCachePct, kvPlain) + " " +
		queueStyle(g.TotalQueue, queuePlain) + " " +
		runningStr + " " +
		ttftStyle(g.AvgTTFTP99, ttftPlain) + " " +
		hitRateStyle(g.AvgHitRate, hitPlain)
}
