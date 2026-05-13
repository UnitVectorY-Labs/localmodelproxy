package ui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/UnitVectorY-Labs/localmodelproxy/internal/config"
	"github.com/UnitVectorY-Labs/localmodelproxy/internal/proxy"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

type Renderer struct {
	cfg     *config.Config
	metrics *proxy.Metrics
	mode    string
	errOut  io.Writer
	program *tea.Program
	cancel  context.CancelFunc
}

func Start(ctx context.Context, shutdown context.CancelFunc, cfg *config.Config, metrics *proxy.Metrics, out, errOut *os.File) *Renderer {
	mode := resolveMode(cfg, out)
	renderer := &Renderer{
		cfg:     cfg,
		metrics: metrics,
		mode:    mode,
		errOut:  errOut,
	}

	switch mode {
	case "tui":
		tuiCtx, cancel := context.WithCancel(ctx)
		renderer.cancel = cancel
		model := tuiModel{cfg: cfg, metrics: metrics, shutdown: shutdown, color: colorEnabled()}
		renderer.program = tea.NewProgram(model, tea.WithOutput(out), tea.WithContext(tuiCtx))
		go func() {
			_, _ = renderer.program.Run()
		}()
	case "plain", "jsonl":
		fmt.Fprintf(errOut, "localmodelproxy listening on http://%s/v1 project=%s location=%s config=%s\n", cfg.Address(), cfg.Vertex.Project, cfg.Vertex.Location, configSource(cfg))
		for _, model := range cfg.Models {
			fmt.Fprintf(errOut, "model %s\n", model.ID)
		}
	}
	return renderer
}

func (r *Renderer) Stop() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.program != nil {
		r.program.Quit()
	}
}

func (r *Renderer) FinalSummary() {
	snapshot := r.metrics.Snapshot()
	if r.mode == "jsonl" {
		_ = json.NewEncoder(r.errOut).Encode(map[string]any{
			"event":           "summary",
			"requests":        snapshot.TotalRequests,
			"successes":       snapshot.Successes,
			"failures":        snapshot.Failures,
			"input_tokens":    snapshot.InputTokens,
			"output_tokens":   snapshot.OutputTokens,
			"thinking_tokens": snapshot.ThinkingTokens,
			"cached_tokens":   snapshot.CachedTokens,
			"total_tokens":    snapshot.TotalTokens,
		})
		return
	}
	if r.mode != "tui" {
		fmt.Fprintf(r.errOut, "summary requests=%d successes=%d failures=%d input_tokens=%d output_tokens=%d thinking_tokens=%d cached_tokens=%d total_tokens=%d\n",
			snapshot.TotalRequests, snapshot.Successes, snapshot.Failures, snapshot.InputTokens, snapshot.OutputTokens, snapshot.ThinkingTokens, snapshot.CachedTokens, snapshot.TotalTokens)
	}
}

func resolveMode(cfg *config.Config, out *os.File) string {
	if cfg.Verbose {
		if cfg.UI.Mode == "jsonl" {
			return "jsonl"
		}
		return "plain"
	}
	if cfg.UI.Mode == "" || cfg.UI.Mode == "auto" {
		if term.IsTerminal(int(out.Fd())) {
			return "tui"
		}
		return "plain"
	}
	return cfg.UI.Mode
}

type tickMsg time.Time

type tuiModel struct {
	cfg      *config.Config
	metrics  *proxy.Metrics
	shutdown context.CancelFunc
	color    bool
}

func (m tuiModel) Init() tea.Cmd {
	return tick()
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			if m.shutdown != nil {
				m.shutdown()
			}
			return m, tea.Quit
		default:
			return m, nil
		}
	case tickMsg:
		return m, tick()
	}
	return m, nil
}

func (m tuiModel) View() string {
	s := m.metrics.Snapshot()
	var b strings.Builder

	title := style(m.color, "title").Render("localmodelproxy")
	value := style(m.color, "value")
	muted := style(m.color, "muted")

	fmt.Fprintf(&b, "%s %s\n", title, value.Render("http://"+m.cfg.Address()+"/v1"))
	fmt.Fprintf(&b, "%s %s\n", muted.Render("Config "), value.Render(configSource(m.cfg)))
	fmt.Fprintf(&b, "%s %s   %s %s\n\n",
		muted.Render("Project"), value.Render(m.cfg.Vertex.Project),
		muted.Render("Location"), value.Render(m.cfg.Vertex.Location))

	b.WriteString(sectionTitle(m.color, "Token Totals"))
	b.WriteByte('\n')
	b.WriteString(renderTable(m.color,
		[]column{
			{name: "Input", width: 10, right: true},
			{name: "Output", width: 10, right: true},
			{name: "Thinking", width: 10, right: true},
			{name: "Cached", width: 10, right: true},
			{name: "Total", width: 10, right: true},
		},
		[][]string{{
			formatInt(s.InputTokens),
			formatInt(s.OutputTokens),
			formatInt(s.ThinkingTokens),
			formatInt(s.CachedTokens),
			formatInt(s.TotalTokens),
		}},
	))
	b.WriteString("\n")

	b.WriteString(sectionTitle(m.color, "Models"))
	b.WriteByte('\n')
	b.WriteString(renderTable(m.color,
		[]column{
			{name: "Model", width: 36},
			{name: "Req", width: 6, right: true},
			{name: "Input", width: 10, right: true},
			{name: "Output", width: 10, right: true},
			{name: "Think", width: 8, right: true},
			{name: "Cached", width: 8, right: true},
			{name: "Total", width: 10, right: true},
		},
		modelRows(m.cfg, s),
	))
	b.WriteString("\n")

	b.WriteString(sectionTitle(m.color, "Recent Requests"))
	b.WriteByte('\n')
	b.WriteString(renderTable(m.color,
		[]column{
			{name: "#", width: 3, right: true},
			{name: "Model", width: 36},
			{name: "Status", width: 7, right: true},
			{name: "Input", width: 10, right: true},
			{name: "Output", width: 10, right: true},
			{name: "Think", width: 8, right: true},
			{name: "Cached", width: 8, right: true},
			{name: "Total", width: 10, right: true},
			{name: "Latency", width: 10, right: true},
		},
		recentRows(s),
	))

	b.WriteString("\n")
	b.WriteString(muted.Render("Ctrl-C or q to stop"))
	b.WriteByte('\n')
	return b.String()
}

type column struct {
	name  string
	width int
	right bool
}

func renderTable(color bool, columns []column, rows [][]string) string {
	var b strings.Builder
	headerStyle := style(color, "header")
	lineStyle := style(color, "line")

	header := make([]string, len(columns))
	for i, col := range columns {
		header[i] = cell(col.name, col.width, col.right)
	}
	b.WriteString(headerStyle.Render(strings.Join(header, "  ")))
	b.WriteByte('\n')

	lineWidth := 0
	for i, col := range columns {
		lineWidth += col.width
		if i > 0 {
			lineWidth += 2
		}
	}
	b.WriteString(lineStyle.Render(strings.Repeat("-", lineWidth)))
	b.WriteByte('\n')

	for _, row := range rows {
		values := make([]string, len(columns))
		for i, col := range columns {
			value := ""
			if i < len(row) {
				value = row[i]
			}
			values[i] = cell(value, col.width, col.right)
		}
		b.WriteString(strings.Join(values, "  "))
		b.WriteByte('\n')
	}
	return b.String()
}

func modelRows(cfg *config.Config, snapshot proxy.Snapshot) [][]string {
	modelNames := make(map[string]struct{})
	for _, model := range cfg.Models {
		modelNames[model.ID] = struct{}{}
	}
	for model := range snapshot.Models {
		modelNames[model] = struct{}{}
	}

	names := make([]string, 0, len(modelNames))
	for name := range modelNames {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		names = append(names, "")
	}

	rows := make([][]string, 0, len(names))
	for _, name := range names {
		stats := snapshot.Models[name]
		rows = append(rows, []string{
			name,
			formatInt(stats.Requests),
			formatInt(stats.InputTokens),
			formatInt(stats.OutputTokens),
			formatInt(stats.ThinkingTokens),
			formatInt(stats.CachedTokens),
			formatInt(stats.TotalTokens),
		})
	}
	return rows
}

func recentRows(snapshot proxy.Snapshot) [][]string {
	rows := make([][]string, 0, len(snapshot.Recent))
	for i, rec := range snapshot.Recent {
		rows = append(rows, []string{
			fmt.Sprintf("%d", i+1),
			rec.Model,
			formatStatus(rec.StatusCode),
			formatInt(rec.Usage.InputTokens),
			formatInt(rec.Usage.OutputTokens),
			formatInt(rec.Usage.ThinkingTokens),
			formatInt(rec.Usage.CachedTokens),
			formatInt(rec.Usage.TotalTokens),
			rec.Duration.Round(time.Millisecond).String(),
		})
	}
	return rows
}

func sectionTitle(color bool, text string) string {
	return style(color, "section").Render(text)
}

func style(color bool, name string) lipgloss.Style {
	base := lipgloss.NewStyle()
	if !color {
		switch name {
		case "title", "section", "header":
			return base.Bold(true)
		default:
			return base
		}
	}
	switch name {
	case "title":
		return base.Bold(true).Foreground(lipgloss.Color("81"))
	case "section":
		return base.Bold(true).Foreground(lipgloss.Color("117"))
	case "header":
		return base.Bold(true).Foreground(lipgloss.Color("250"))
	case "line":
		return base.Foreground(lipgloss.Color("238"))
	case "muted":
		return base.Foreground(lipgloss.Color("244"))
	case "value":
		return base.Foreground(lipgloss.Color("229"))
	default:
		return base
	}
}

func cell(value string, width int, right bool) string {
	value = truncate(value, width)
	if right {
		return fmt.Sprintf("%*s", width, value)
	}
	return fmt.Sprintf("%-*s", width, value)
}

func truncate(value string, width int) string {
	if len(value) <= width {
		return value
	}
	if width <= 1 {
		return value[:width]
	}
	return value[:width-1] + "."
}

func formatInt(value int64) string {
	if value == 0 {
		return ""
	}
	return fmt.Sprintf("%d", value)
}

func formatStatus(status int) string {
	if status == 0 {
		return ""
	}
	return fmt.Sprintf("%d", status)
}

func configSource(cfg *config.Config) string {
	if cfg.SourcePath == "" {
		return "defaults/env/flags"
	}
	return cfg.SourcePath
}

func colorEnabled() bool {
	return os.Getenv("NO_COLOR") == ""
}

func tick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}
