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
	colRole    = 8
	colBackend = 8
	colKV      = 6
	colKVBar   = 6
	colQueue   = 6
	colRun     = 5
	colTTFT    = 10
	colITL     = 10
	colHit     = 6
	colTok     = 7
)

// fixedWidth is the sum of all fixed columns + inter-column spaces + left margin + dot.
// Layout: "  " (2) + dot (1) + " " (1) + role + " " + ep + " " + backend + " " + model + " " + kv + " " + kvbar + " " + queue + " " + run + " " + ttft + " " + itl + " " + hit + " " + tok
// Overhead: 2 (margin) + 1 (dot) + 1 (space) + 11 (inter-column spaces) = 15
var fixedWidth = colRole + colBackend + colKV + colKVBar + colQueue + colRun + colTTFT + colITL + colHit + colTok + 15

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

// tableRow represents either a data row (worker) or a separator row.
type tableRow struct {
	worker    *metrics.WorkerMetrics // nil for separator rows
	separator string                 // non-empty for separator rows
	dataIdx   int                    // index into the original workers slice (for selection)
}

// BuildTableRows groups workers into pool sections and returns a flat list of rows
// with separator headers. Returns (rows, mapping from display index to data index).
func BuildTableRows(workers []*metrics.WorkerMetrics, filterBackend metrics.Backend) []tableRow {
	var prefill, decode, errWorkers, mono []*metrics.WorkerMetrics

	for _, w := range workers {
		if filterBackend != "" && w.Backend != filterBackend && filterBackend != metrics.BackendUnknown {
			continue
		}
		if !w.Online {
			errWorkers = append(errWorkers, w)
			continue
		}
		switch w.Role {
		case "prefill":
			prefill = append(prefill, w)
		case "decode":
			decode = append(decode, w)
		default:
			mono = append(mono, w)
		}
	}

	hasPools := len(prefill) > 0 || len(decode) > 0
	var rows []tableRow
	dataIdx := 0

	if hasPools {
		if len(prefill) > 0 {
			rows = append(rows, tableRow{separator: fmt.Sprintf("── PREFILL POOL (%d workers) ──", len(prefill))})
			for _, w := range prefill {
				rows = append(rows, tableRow{worker: w, dataIdx: dataIdx})
				dataIdx++
			}
		}
		if len(decode) > 0 {
			rows = append(rows, tableRow{separator: fmt.Sprintf("── DECODE POOL (%d workers) ──", len(decode))})
			for _, w := range decode {
				rows = append(rows, tableRow{worker: w, dataIdx: dataIdx})
				dataIdx++
			}
		}
		if len(mono) > 0 {
			rows = append(rows, tableRow{separator: fmt.Sprintf("── MONO (%d workers) ──", len(mono))})
			for _, w := range mono {
				rows = append(rows, tableRow{worker: w, dataIdx: dataIdx})
				dataIdx++
			}
		}
	} else {
		// No prefill/decode roles detected — render flat list without pool headers
		for _, w := range mono {
			rows = append(rows, tableRow{worker: w, dataIdx: dataIdx})
			dataIdx++
		}
	}

	// Offline workers render inline at the bottom without a section header
	for _, w := range errWorkers {
		rows = append(rows, tableRow{worker: w, dataIdx: dataIdx})
		dataIdx++
	}

	return rows
}

// DataRowCount returns the number of selectable (non-separator) rows.
func DataRowCount(rows []tableRow) int {
	n := 0
	for _, r := range rows {
		if r.worker != nil {
			n++
		}
	}
	return n
}

// NextDataRow returns the next selectable row index after moving in the given direction.
// direction: +1 for down, -1 for up. currentDataIdx is the current selected data index.
func NextDataRow(rows []tableRow, currentDataIdx, direction int) int {
	// Find current display index
	displayIdx := -1
	for i, r := range rows {
		if r.worker != nil && r.dataIdx == currentDataIdx {
			displayIdx = i
			break
		}
	}
	if displayIdx < 0 {
		return currentDataIdx
	}

	// Move in direction, skipping separators
	for i := displayIdx + direction; i >= 0 && i < len(rows); i += direction {
		if rows[i].worker != nil {
			return rows[i].dataIdx
		}
	}
	return currentDataIdx
}

// gutterWidth is the fixed left-gutter column width for the V3 anomaly halo
// and V8 bookmark star. Pre-allocated to 2 chars from V3's ship date even
// when V8 hasn't shipped — see spec §8 open Q2 — to avoid a column shift
// when V8 lands. The gutter lives between the status dot and the role
// badge: [halo][bookmark].
const gutterWidth = 2

// RenderTable renders the worker metrics table with pool grouping.
//
// visibleRows is the maximum number of body rows (data + separator) the
// caller wants rendered after the header/separator pair. viewportOffset is
// the index of the first row in the buildTableRows slice to render. The
// caller is responsible for clamping viewportOffset; RenderTable defends
// against out-of-bound values but does not adjust selection.
//
// anomaly (V3) supplies per-worker sigma values for the gutter halo
// glyph; nil disables the halo (gutter slot 1 renders blank).
// bookmarks (V8) supplies per-worker bookmark state for the gutter star;
// nil disables the star (gutter slot 2 renders blank). Both may be nil
// simultaneously — gutter is then 2 spaces of dead column.
func RenderTable(workers []*metrics.WorkerMetrics, selectedIdx int, sortCol SortColumn, filterBackend metrics.Backend, width, visibleRows, viewportOffset int, anomaly AnomalyGetter, bookmarks *BookmarkStore, k8sCtx string) string {
	var sb strings.Builder
	epW, modelW := flexWidths(width)

	header := renderTableHeader(sortCol, epW, modelW)
	sb.WriteString(header)
	sb.WriteString("\n")

	sep := StyleTableSeparator.Render(strings.Repeat("-", max(width-4, 80)))
	sb.WriteString("    " + sep)
	sb.WriteString("\n")

	rows := BuildTableRows(workers, filterBackend)
	total := len(rows)

	// visibleRows <= 0 means caller hasn't sized yet — render all rows.
	if visibleRows <= 0 {
		visibleRows = total
	}

	start, end := ClampViewport(total, visibleRows, viewportOffset)

	for _, r := range rows[start:end] {
		if r.separator != "" {
			sb.WriteString("  ")
			sb.WriteString(StylePoolSeparator.Render(r.separator))
			sb.WriteString("\n")
			continue
		}
		row := renderWorkerRowWithGutter(r.worker, r.dataIdx == selectedIdx, epW, modelW, anomaly, bookmarks, k8sCtx)
		sb.WriteString(row)
		sb.WriteString("\n")
	}

	return sb.String()
}

// renderGutter returns the 2-character gutter prefix:
//
//	[halo glyph or space][bookmark star or space]
//
// Halo: red dot for |sigma|>3, yellow dot for |sigma|>2, dim cyan dot for
// sigma<-2, blank otherwise. Bookmark: yellow star when bookmarked.
func renderGutter(w *metrics.WorkerMetrics, anomaly AnomalyGetter, bookmarks *BookmarkStore, k8sCtx string) string {
	halo := " "
	if anomaly != nil && w != nil {
		sigma, _ := anomaly.MaxSigma(w.Endpoint, w)
		abs := sigma
		if abs < 0 {
			abs = -abs
		}
		switch {
		case abs > 3:
			halo = StyleMetricBad.Render("●")
		case abs > 2:
			halo = StyleMetricWarn.Render("●")
		case sigma < -2:
			halo = StyleMetricNA.Render("●")
		}
	}
	star := " "
	if bookmarks != nil && w != nil && bookmarks.IsBookmarked(k8sCtx, w.Endpoint) {
		star = StyleSortIndicator.Render("★")
	}
	return halo + star
}

// ClampViewport returns the [start, end) row slice indices for a viewport of
// visibleRows rows, given total rows and a desired offset. The result always
// satisfies 0 <= start <= end <= total and end-start <= visibleRows.
//
// When the requested window extends past the end of the row list, start is
// pulled back so that exactly visibleRows are shown (the spec's "End"-style
// snap behavior). When total <= visibleRows, the entire range is returned.
func ClampViewport(total, visibleRows, offset int) (int, int) {
	if total <= 0 || visibleRows <= 0 {
		return 0, 0
	}
	if total <= visibleRows {
		return 0, total
	}
	maxOffset := total - visibleRows
	if offset < 0 {
		offset = 0
	}
	if offset > maxOffset {
		offset = maxOffset
	}
	return offset, offset + visibleRows
}

// DisplayIndexOf returns the display row index (separator-counting) of the
// data row with dataIdx == target, or -1 if absent.
func DisplayIndexOf(rows []tableRow, target int) int {
	for i, r := range rows {
		if r.worker != nil && r.dataIdx == target {
			return i
		}
	}
	return -1
}

// FirstDataRowAt returns the dataIdx of the first data row at or after the
// given display index, or -1 if none. Used to land the cursor on a data row
// after PgDn/PgUp/Home/End jumps. When displayIdx is past the end of the
// row list, returns -1 (callers must not assume a wrap-around).
func FirstDataRowAt(rows []tableRow, displayIdx int) int {
	if displayIdx < 0 {
		displayIdx = 0
	}
	if displayIdx >= len(rows) {
		return -1
	}
	for i := displayIdx; i < len(rows); i++ {
		if rows[i].worker != nil {
			return rows[i].dataIdx
		}
	}
	return -1
}

// LastDataRowAtOrBefore returns the dataIdx of the last data row at or
// before the given display index, or -1 if none.
func LastDataRowAtOrBefore(rows []tableRow, displayIdx int) int {
	if displayIdx >= len(rows) {
		displayIdx = len(rows) - 1
	}
	for i := displayIdx; i >= 0; i-- {
		if rows[i].worker != nil {
			return rows[i].dataIdx
		}
	}
	return -1
}

func renderTableHeader(sortCol SortColumn, epW, modelW int) string {
	cols := []struct {
		name  string
		width int
		col   SortColumn
	}{
		{"ROLE", colRole, SortNone},
		{"WORKER", epW, SortNone},
		{"BACKEND", colBackend, SortNone},
		{"MODEL", modelW, SortNone},
		{"KV%", colKV, SortKVCache},
		{"KV BAR", colKVBar, SortNone},
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
	// 4-char left margin + 3-char gutter slot ("  " margin + " " after dot
	// + 2-char gutter [halo][bookmark] + " " before role) keeps the column
	// positions aligned with renderWorkerRowInner above.
	return "    " + strings.Repeat(" ", gutterWidth) + " " + strings.Join(parts, " ")
}

// renderWorkerRowWithGutter wraps renderWorkerRow with the V3/V8 2-char
// gutter prefix between the status dot and the role badge.
func renderWorkerRowWithGutter(w *metrics.WorkerMetrics, selected bool, epW, modelW int, anomaly AnomalyGetter, bookmarks *BookmarkStore, k8sCtx string) string {
	gutter := renderGutter(w, anomaly, bookmarks, k8sCtx)
	return renderWorkerRowInner(w, selected, epW, modelW, gutter)
}

func renderWorkerRowInner(w *metrics.WorkerMetrics, selected bool, epW, modelW int, gutter string) string {
	var dot string
	if w.Online {
		dot = StyleDotOnline.Render("●")
	} else {
		dot = StyleDotOffline.Render("○")
	}

	// Role badge
	role := w.Role
	if role == "" {
		role = "mono"
	}
	rolePlain := padRight(role, colRole)

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
		backendStr := padRight(string(w.Backend), colBackend)
		row := "  " + dot + " " + gutter + " " + rolePlain + " " + epStr + " " + backendStr + " " + modelStr + " " +
			padRight("Err", colKV) + " " + padRight("─", colKVBar) + " " + padRight("Err", colQueue) + " " + padRight("Err", colRun) + " " +
			padRight("Err", colTTFT) + " " + padRight("Err", colITL) + " " +
			padRight("Err", colHit) + " " + padRight("Err", colTok)
		if selected {
			return StyleTableRowSelected.Render(row)
		}
		return StyleTableRowOffline.Render(row)
	}

	backendPlain := padRight(string(w.Backend), colBackend)
	kvPlain := formatKVCache(w.KVCacheUsagePct, colKV)
	kvBar := RenderKVBar(w.KVCacheUsagePct, colKVBar)
	queuePlain := formatQueue(w.RequestsWaiting, colQueue)
	runPlain := padRight(fmt.Sprintf("%d", w.RequestsRunning), colRun)
	ttftPlain := formatTTFT(w.TTFT_P99, colTTFT)
	itlPlain := formatTTFT(w.ITL_P99, colITL)
	hitPlain := formatHitRate(w.CacheHitRatePct, colHit)
	tokPlain := formatTokPerSec(w.GenTokPerSec+w.PromptTokPerSec, colTok)

	if selected {
		plain := "  " + dot + " " + gutter + " " + rolePlain + " " + epStr + " " + backendPlain + " " + modelStr + " " +
			kvPlain + " " + kvBar + " " + queuePlain + " " + runPlain + " " + ttftPlain + " " + itlPlain + " " +
			hitPlain + " " + tokPlain
		return StyleTableRowSelected.Render(plain)
	}

	return "  " + dot + " " + gutter + " " +
		RoleStyle(role).Render(rolePlain) + " " +
		epStr + " " +
		backendStyle(w.Backend).Render(backendPlain) + " " +
		modelStr + " " +
		kvCacheStyle(w.KVCacheUsagePct, kvPlain) + " " +
		kvBar + " " +
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
	case metrics.BackendOllama:
		return StyleBadgeOllama
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
