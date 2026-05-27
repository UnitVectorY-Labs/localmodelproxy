package ui

import (
	"bufio"
	"context"
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
	input   context.CancelFunc
}

func Start(ctx context.Context, shutdown context.CancelFunc, cfg *config.Config, metrics *proxy.Metrics, out, errOut *os.File) *Renderer {
	mode := resolveMode(out)
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
		model := tuiModel{cfg: cfg, metrics: metrics, shutdown: shutdown, color: colorEnabled(), testResults: make(map[string]string), allModels: []string{"Total"}}
		renderer.program = tea.NewProgram(model, tea.WithOutput(out), tea.WithContext(tuiCtx), tea.WithAltScreen())
		go func() {
			_, _ = renderer.program.Run()
		}()
	case "plain":
		fmt.Fprintf(errOut, "localmodelproxy listening on http://%s/v1 config=%s\n", cfg.Address(), configSource(cfg))
		for _, model := range cfg.AllModels() {
			fmt.Fprintf(errOut, "model %s\n", model.ID)
		}
		renderer.input = startPlainLineListener(ctx, shutdown, os.Stdin)
	}
	return renderer
}

func (r *Renderer) Stop() {
	if r.input != nil {
		r.input()
	}
	if r.program != nil {
		// Send quit first so Bubble Tea can cleanly restore the terminal
		// (alt screen, cursor, raw mode) before we cancel the context.
		r.program.Quit()
		r.program.Wait()
	}
	if r.cancel != nil {
		r.cancel()
	}
}

func (r *Renderer) FinalSummary() {
	snapshot := r.metrics.Snapshot()
	if r.mode != "tui" {
		fmt.Fprintf(r.errOut, "summary requests=%d successes=%d failures=%d input_tokens=%d output_tokens=%d thinking_tokens=%d cached_tokens=%d total_tokens=%d",
			snapshot.TotalRequests, snapshot.Successes, snapshot.Failures, snapshot.InputTokens, snapshot.OutputTokens, snapshot.ThinkingTokens, snapshot.CachedTokens, snapshot.TotalTokens)
		if r.cfg.HasModelCosts() || snapshot.TotalCostUSD > 0 {
			fmt.Fprintf(r.errOut, " total_cost_usd=%.6f", snapshot.TotalCostUSD)
		}
		fmt.Fprintln(r.errOut)
	}
}

func resolveMode(out *os.File) string {
	if term.IsTerminal(int(out.Fd())) {
		return "tui"
	}
	return "plain"
}

func startPlainLineListener(ctx context.Context, shutdown context.CancelFunc, in *os.File) context.CancelFunc {
	if shutdown == nil || in == nil || !term.IsTerminal(int(in.Fd())) {
		return nil
	}

	listenCtx, cancel := context.WithCancel(ctx)
	lines := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(in)
		for scanner.Scan() {
			select {
			case lines <- scanner.Text():
			case <-listenCtx.Done():
				return
			}
		}
		close(lines)
	}()
	go func() {
		defer cancel()
		for {
			select {
			case <-listenCtx.Done():
				return
			case line, ok := <-lines:
				if !ok {
					return
				}
				switch strings.TrimSpace(line) {
				case "q", "Q":
					shutdown()
					return
				}
			}
		}
	}()
	return cancel
}

type tickMsg time.Time

// testResultMsg carries the result of a test request back to the TUI.
type testResultMsg struct {
	model   string
	content string
	err     error
}

const (
	tabStats = 0
	tabTest  = 1
)

type tuiModel struct {
	cfg      *config.Config
	metrics  *proxy.Metrics
	shutdown context.CancelFunc
	color    bool
	width    int
	height   int

	// Tab navigation
	activeTab int

	// Shared model selection (used by both Stats and Test tabs)
	modelCursor int
	allModels   []string // ["Total", model1, model2, ...]

	// Test tab state
	testRunning bool
	testResults map[string]string // model -> result or error
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
		case "tab", "right", "l":
			if m.cfg.UI.TestEnabled() {
				m.activeTab = (m.activeTab + 1) % 2
			}
			return m, nil
		case "shift+tab", "left", "h":
			if m.cfg.UI.TestEnabled() {
				m.activeTab = (m.activeTab - 1 + 2) % 2
			}
			return m, nil
		case "up", "k":
			if len(m.allModels) > 0 {
				m.modelCursor--
				if m.modelCursor < 0 {
					m.modelCursor = len(m.allModels) - 1
				}
			}
			return m, nil
		case "down", "j":
			if len(m.allModels) > 0 {
				m.modelCursor++
				if m.modelCursor >= len(m.allModels) {
					m.modelCursor = 0
				}
			}
			return m, nil
		case "enter":
			if m.activeTab == tabTest && len(m.allModels) > 0 && !m.testRunning && m.modelCursor > 0 {
				m.testRunning = true
				model := m.allModels[m.modelCursor]
				delete(m.testResults, model)
				return m, m.runTest(model)
			}
			return m, nil
		default:
			return m, nil
		}
	case testResultMsg:
		m.testRunning = false
		if msg.err != nil {
			m.testResults[msg.model] = "Error: " + msg.err.Error()
		} else {
			m.testResults[msg.model] = msg.content
		}
		return m, nil
	case tickMsg:
		newAllModels := m.buildStatsModelList()
		if m.modelCursor >= len(newAllModels) {
			m.modelCursor = 0
		}
		m.allModels = newAllModels
		return m, tick()
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil
	}
	return m, nil
}

func (m tuiModel) buildStatsModelList() []string {
	names := m.buildModelList()
	result := make([]string, 0, len(names)+1)
	result = append(result, "Total")
	result = append(result, names...)
	return result
}

func (m tuiModel) buildModelList() []string {
	modelNames := make(map[string]struct{})
	for _, model := range m.cfg.AllModels() {
		modelNames[model.ID] = struct{}{}
	}
	// Also include models seen in metrics
	snapshot := m.metrics.Snapshot()
	for name := range snapshot.Models {
		modelNames[name] = struct{}{}
	}
	names := make([]string, 0, len(modelNames))
	for name := range modelNames {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (m tuiModel) runTest(model string) tea.Cmd {
	addr := m.cfg.Address()
	systemMsg := m.cfg.UI.TestSystemMsg()
	userMsg := m.cfg.UI.TestUserMsg()
	return func() tea.Msg {
		return executeTest(addr, model, systemMsg, userMsg)
	}
}

func (m tuiModel) View() string {
	tableWidth := m.tableWidth()
	muted := style(m.color, "muted")
	value := style(m.color, "value")

	// --- Header ---
	var header strings.Builder
	header.WriteString(renderLogo(m.color, tableWidth))
	header.WriteByte('\n')

	endpoint := "http://" + m.cfg.Address() + "/v1"
	configSrc := configSource(m.cfg)
	oneLinerLen := len("Endpoint ") + len(endpoint) + len("  Config ") + len(configSrc)
	if tableWidth > 0 && oneLinerLen <= tableWidth {
		fmt.Fprintf(&header, "%s %s  %s %s\n\n",
			muted.Render("Endpoint"), value.Render(endpoint),
			muted.Render("Config"), value.Render(configSrc),
		)
	} else {
		fmt.Fprintf(&header, "%s %s\n%s %s\n\n",
			muted.Render("Endpoint"), value.Render(endpoint),
			muted.Render("Config "), value.Render(configSrc),
		)
	}

	if m.cfg.UI.TestEnabled() {
		header.WriteString(m.renderTabBar())
		header.WriteByte('\n')
		header.WriteByte('\n')
	}

	// --- Main content ---
	var content string
	switch m.activeTab {
	case tabStats:
		content = m.viewStats(tableWidth)
	case tabTest:
		content = m.viewTest(tableWidth)
	}

	// --- Footer ---
	var footer strings.Builder
	if m.cfg.UI.TestEnabled() {
		footer.WriteString(muted.Render("←/→: switch view • "))
	}
	footer.WriteString(muted.Render("↑/↓: select • "))
	if m.activeTab == tabTest {
		footer.WriteString(muted.Render("Enter: test • "))
	}
	footer.WriteString(muted.Render("Ctrl-C or q to stop"))

	var b strings.Builder
	b.WriteString(header.String())
	b.WriteString(content)

	// Pin footer to bottom of terminal
	if m.height > 0 {
		used := strings.Count(header.String(), "\n") + strings.Count(content, "\n") + 2 // +2: blank line + footer
		if pad := m.height - used; pad > 0 {
			b.WriteString(strings.Repeat("\n", pad))
		}
	}
	b.WriteString("\n")
	b.WriteString(footer.String())
	b.WriteByte('\n')
	return b.String()
}

func (m tuiModel) renderTabBar() string {
	activeStyle := style(m.color, "tab-active")
	inactiveStyle := style(m.color, "tab-inactive")

	tabs := []string{"Stats", "Test"}
	parts := make([]string, len(tabs))
	for i, name := range tabs {
		if i == m.activeTab {
			parts[i] = activeStyle.Render("[" + name + "]")
		} else {
			parts[i] = inactiveStyle.Render("[" + name + "]")
		}
	}
	return strings.Join(parts, "  ")
}

func (m tuiModel) viewStats(tableWidth int) string {
	s := m.metrics.Snapshot()
	var b strings.Builder
	showCosts := m.cfg.HasModelCosts() || s.TotalCostUSD > 0

	// statsCursor==0 means "Total" (all models); cursor>0 means a specific model.
	var selectedModel string
	if m.modelCursor < len(m.allModels) {
		selectedModel = m.allModels[m.modelCursor]
	}

	b.WriteString(renderTable(m.color,
		tableWidth,
		modelTableColumns(showCosts),
		modelRows(m.cfg, s, showCosts, selectedModel),
	))
	b.WriteString("\n")

	if m.cfg.UI.RecentRequests > 0 {
		b.WriteString(sectionTitle(m.color, "Recent Requests"))
		b.WriteByte('\n')

		// filterModel is empty for Total (show all), or the specific model name
		filterModel := ""
		if selectedModel != "Total" {
			filterModel = selectedModel
		}

		b.WriteString(renderTable(m.color,
			tableWidth,
			[]column{
				{name: "#", width: requestNumberWidth(s), right: true, style: "orange"},
				{name: "Model", width: 36, flex: true},
				{name: "Latency", width: 10, right: true},
				{name: "Input", width: 10, right: true},
				{name: "Output", width: 10, right: true},
				{name: "Think", width: 8, right: true},
				{name: "Cached", width: 8, right: true},
				{name: "Total", width: 10, right: true},
				{name: "Cost", width: 10, right: true, hidden: !showCosts, style: "green"},
			},
			recentRowsFiltered(s, m.cfg.UI.RecentRequests, showCosts, filterModel),
		))
	}
	return b.String()
}

func (m tuiModel) viewTest(tableWidth int) string {
	var b strings.Builder
	s := m.metrics.Snapshot()
	showCosts := m.cfg.HasModelCosts() || s.TotalCostUSD > 0

	var selectedTestModel string
	if m.modelCursor < len(m.allModels) && m.allModels[m.modelCursor] != "Total" {
		selectedTestModel = m.allModels[m.modelCursor]
	}

	b.WriteString(renderTable(m.color,
		tableWidth,
		testModelTableColumns(showCosts),
		testModelRows(m.cfg, s, showCosts, m.allModels[m.modelCursor]),
	))

	b.WriteByte('\n')
	if m.testRunning {
		b.WriteString(style(m.color, "orange").Render("⏳ Sending test request..."))
		b.WriteByte('\n')
	}

	if selectedTestModel != "" {
		if result, ok := m.testResults[selectedTestModel]; ok {
			b.WriteString(sectionTitle(m.color, "Response from "+selectedTestModel))
			b.WriteByte('\n')
			wrapped := wrapText(result, tableWidth)
			if strings.HasPrefix(result, "Error:") {
				b.WriteString(style(m.color, "red").Render(wrapped))
			} else {
				b.WriteString(style(m.color, "ok").Render(wrapped))
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

func wrapText(text string, width int) string {
	if width <= 0 {
		width = 80
	}
	if len(text) <= width {
		return text
	}
	var b strings.Builder
	for len(text) > width {
		b.WriteString(text[:width])
		b.WriteByte('\n')
		text = text[width:]
	}
	b.WriteString(text)
	return b.String()
}

func (m tuiModel) tableWidth() int {
	if m.width <= 0 {
		return 100
	}
	if m.width < 40 {
		return m.width
	}
	return m.width - 1
}

func renderLogo(color bool, width int) string {
	if width > 0 && width < 78 {
		return style(color, "logo").Render("+-----------------+\n| localmodelproxy |\n+-----------------+")
	}
	lines := []string{
		" _                 _                 _      _",
		"| | ___   ___ __ _| |_ __ ___   ___ | | ___| |_ __  _ __ _____  ___   _",
		"| |/ _ \\ / __/ _` | | '_ ` _ \\ / _ \\| |/ _ \\ | '_ \\| '__/ _ \\ \\/ / | | |",
		"| | (_) | (_| (_| | | | | | | | (_) | |  __/ | |_) | | | (_) >  <| |_| |",
		"|_|\\___/ \\___\\__,_|_|_| |_| |_|\\___/|_|\\___|_| .__/|_|  \\___/_/\\_\\\\__, |",
		"                                             |_|                   |___/",
	}
	return style(color, "logo").Render(strings.Join(lines, "\n"))
}

type column struct {
	name   string
	width  int
	right  bool
	flex   bool
	hidden bool
	style  string
}

func renderTable(color bool, targetWidth int, columns []column, rows [][]string) string {
	var b strings.Builder
	headerStyle := style(color, "header")
	lineStyle := style(color, "line")
	muted := style(color, "muted")

	columns = visibleColumns(columns)
	columns = fitColumns(columns, targetWidth)

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

	if len(rows) == 0 {
		b.WriteString(muted.Render(cell("No requests yet", lineWidth, false)))
		b.WriteByte('\n')
		return b.String()
	}
	for _, row := range rows {
		if isSeparatorRow(row) {
			b.WriteString(lineStyle.Render(strings.Repeat("-", lineWidth)))
			b.WriteByte('\n')
			continue
		}
		values := make([]string, len(columns))
		for i, col := range columns {
			value := ""
			if i < len(row) {
				value = row[i]
			}
			values[i] = styledCell(color, value, col.width, col.right, col.style)
		}
		b.WriteString(strings.Join(values, "  "))
		b.WriteByte('\n')
	}
	return b.String()
}

func visibleColumns(columns []column) []column {
	out := make([]column, 0, len(columns))
	for _, col := range columns {
		if !col.hidden {
			out = append(out, col)
		}
	}
	return out
}

func fitColumns(columns []column, targetWidth int) []column {
	if targetWidth <= 0 {
		return columns
	}
	width := tableLineWidth(columns)
	if width <= targetWidth {
		for i := range columns {
			if columns[i].flex {
				columns[i].width += targetWidth - width
				break
			}
		}
		return columns
	}
	for width > targetWidth {
		flexIndex := -1
		for i, col := range columns {
			if col.flex {
				flexIndex = i
				break
			}
		}
		if flexIndex == -1 || columns[flexIndex].width <= 12 {
			break
		}
		columns[flexIndex].width--
		width--
	}
	for width > targetWidth && len(columns) > 3 {
		columns = columns[:len(columns)-1]
		width = tableLineWidth(columns)
	}
	return columns
}

func tableLineWidth(columns []column) int {
	width := 0
	for i, col := range columns {
		width += col.width
		if i > 0 {
			width += 2
		}
	}
	return width
}

func testModelTableColumns(showCosts bool) []column {
	return []column{
		{name: " ", width: 1},
		{name: "Model", width: 36, flex: true},
		{name: "Latency", width: 10, right: true},
		{name: "Input", width: 10, right: true},
		{name: "Output", width: 10, right: true},
		{name: "Think", width: 8, right: true},
		{name: "Cached", width: 8, right: true},
		{name: "Total", width: 10, right: true},
		{name: "Cost", width: 10, right: true, hidden: !showCosts, style: "green"},
	}
}

// testModelRows builds rows for the Test tab: one row per model showing the
// most recent request's stats for that model (no Total/separator row).
func testModelRows(cfg *config.Config, snapshot proxy.Snapshot, showCosts bool, selectedModel string) [][]string {
	modelNames := make(map[string]struct{})
	for _, model := range cfg.AllModels() {
		modelNames[model.ID] = struct{}{}
	}
	names := make([]string, 0, len(modelNames))
	for name := range modelNames {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		names = append(names, "")
	}

	// Index most recent record per model
	latestPerModel := make(map[string]proxy.RequestRecord)
	for i := len(snapshot.Recent) - 1; i >= 0; i-- {
		rec := snapshot.Recent[i]
		latestPerModel[rec.Model] = rec
	}

	indicator := func(name string) string {
		if name == selectedModel {
			return withStyle("selected", ">")
		}
		return " "
	}
	modelName := func(name string) string {
		if name == selectedModel {
			return withStyle("orange", name)
		}
		return name
	}

	rows := make([][]string, 0, len(names))
	for _, name := range names {
		rec, hasRec := latestPerModel[name]
		latency := "-"
		if hasRec && rec.Duration > 0 {
			latency = rec.Duration.Round(time.Millisecond).String()
			if requestFailed(rec) {
				latency = withStyle("red", latency)
			}
		}
		rows = append(rows, []string{
			indicator(name),
			modelName(name),
			latency,
			formatIntDash(rec.Usage.InputTokens),
			formatIntDash(rec.Usage.OutputTokens),
			formatIntDash(rec.Usage.ThinkingTokens),
			formatIntDash(rec.Usage.CachedTokens),
			formatIntDash(rec.Usage.TotalTokens),
			formatCostDashIfEnabled(rec.CostUSD, showCosts),
		})
	}
	return rows
}

func modelTableColumns(showCosts bool) []column {
	return []column{
		{name: " ", width: 1},
		{name: "Model", width: 36, flex: true},
		{name: "Req", width: 6, right: true},
		{name: "Input", width: 10, right: true},
		{name: "Output", width: 10, right: true},
		{name: "Think", width: 8, right: true},
		{name: "Cached", width: 8, right: true},
		{name: "Total", width: 10, right: true},
		{name: "Cost", width: 10, right: true, hidden: !showCosts, style: "green"},
	}
}

func modelRows(cfg *config.Config, snapshot proxy.Snapshot, showCosts bool, selectedModel string) [][]string {
	modelNames := make(map[string]struct{})
	for _, model := range cfg.AllModels() {
		modelNames[model.ID] = struct{}{}
	}

	names := make([]string, 0, len(modelNames))
	for name := range modelNames {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		names = append(names, "")
	}

	indicator := func(name string) string {
		if name == selectedModel {
			return withStyle("selected", ">")
		}
		return " "
	}

	modelName := func(name string) string {
		if name == selectedModel {
			return withStyle("orange", name)
		}
		return name
	}

	totalLabel := func() string {
		if selectedModel == "Total" {
			return withStyle("orange", "Total")
		}
		return withStyle("bold", "Total")
	}

	rows := make([][]string, 0, len(names))
	for _, name := range names {
		stats := snapshot.Models[name]
		rows = append(rows, []string{
			indicator(name),
			modelName(name),
			formatIntDash(stats.Requests),
			formatIntDash(stats.InputTokens),
			formatIntDash(stats.OutputTokens),
			formatIntDash(stats.ThinkingTokens),
			formatIntDash(stats.CachedTokens),
			formatIntDash(stats.TotalTokens),
			formatCostDashIfEnabled(stats.CostUSD, showCosts),
		})
	}
	rows = append(rows, separatorRow())
	rows = append(rows, []string{
		indicator("Total"),
		totalLabel(),
		withStyle("bold", formatIntDash(snapshot.ModelRequests)),
		withStyle("bold", formatIntDash(snapshot.InputTokens)),
		withStyle("bold", formatIntDash(snapshot.OutputTokens)),
		withStyle("bold", formatIntDash(snapshot.ThinkingTokens)),
		withStyle("bold", formatIntDash(snapshot.CachedTokens)),
		withStyle("bold", formatIntDash(snapshot.TotalTokens)),
		withStyle("green-bold", formatCostDashIfEnabled(snapshot.TotalCostUSD, showCosts)),
	})
	return rows
}

func recentRows(snapshot proxy.Snapshot, limit int, showCosts bool) [][]string {
	return recentRowsFiltered(snapshot, limit, showCosts, "")
}

func recentRowsFiltered(snapshot proxy.Snapshot, limit int, showCosts bool, filterModel string) [][]string {
	recent := snapshot.Recent
	if filterModel != "" {
		filtered := make([]proxy.RequestRecord, 0, len(recent))
		for _, rec := range recent {
			if rec.Model == filterModel {
				filtered = append(filtered, rec)
			}
		}
		recent = filtered
	}
	if len(recent) > limit {
		recent = recent[:limit]
	}
	rows := make([][]string, 0, len(recent))
	for _, rec := range recent {
		latency := rec.Duration.Round(time.Millisecond).String()
		if requestFailed(rec) {
			latency = withStyle("red", latency)
		}
		row := []string{
			formatInt(rec.Sequence),
			rec.Model,
			latency,
			formatInt(rec.Usage.InputTokens),
			formatInt(rec.Usage.OutputTokens),
			formatInt(rec.Usage.ThinkingTokens),
			formatInt(rec.Usage.CachedTokens),
			formatInt(rec.Usage.TotalTokens),
		}
		if showCosts {
			row = append(row, formatCost(rec.CostUSD))
		}
		rows = append(rows, row)
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
		case "bold", "green-bold", "logo", "title", "section", "header":
			return base.Bold(true)
		default:
			return base
		}
	}
	switch name {
	case "logo":
		return base.Bold(true).Foreground(lipgloss.Color("208"))
	case "orange":
		return base.Foreground(lipgloss.Color("208"))
	case "tab-active":
		return base.Bold(true).Background(lipgloss.Color("208")).Foreground(lipgloss.Color("0"))
	case "tab-inactive":
		return base.Foreground(lipgloss.Color("208"))
	case "selected":
		return base.Bold(true).Foreground(lipgloss.Color("208"))
	case "green":
		return base.Foreground(lipgloss.Color("42"))
	case "green-bold":
		return base.Bold(true).Foreground(lipgloss.Color("42"))
	case "red":
		return base.Foreground(lipgloss.Color("203"))
	case "bold":
		return base.Bold(true)
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
	case "ok":
		return base.Foreground(lipgloss.Color("42"))
	case "bad":
		return base.Foreground(lipgloss.Color("203"))
	default:
		return base
	}
}

func requestNumberWidth(snapshot proxy.Snapshot) int {
	width := len(fmt.Sprintf("%d", snapshot.ModelRequests))
	if width < 3 {
		return 3
	}
	return width
}

func cell(value string, width int, right bool) string {
	value = truncate(value, width)
	if right {
		return fmt.Sprintf("%*s", width, value)
	}
	return fmt.Sprintf("%-*s", width, value)
}

func styledCell(color bool, value string, width int, right bool, styleName string) string {
	if inlineStyle, stripped, ok := splitInlineStyle(value); ok {
		styleName = inlineStyle
		value = stripped
	}
	rendered := cell(value, width, right)
	if strings.TrimSpace(value) == "" || styleName == "" {
		return rendered
	}
	return style(color, styleName).Render(rendered)
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

func formatIntDash(value int64) string {
	if value == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", value)
}

func formatCostDashIfEnabled(value float64, enabled bool) string {
	if !enabled {
		return ""
	}
	if value == 0 {
		return "-"
	}
	return formatCost(value)
}

func formatCost(value float64) string {
	if value == 0 {
		return ""
	}
	if value < 0.01 {
		return fmt.Sprintf("$%.4f", value)
	}
	return fmt.Sprintf("$%.2f", value)
}

func withStyle(styleName, value string) string {
	return "\x00" + styleName + "\x00" + value
}

func separatorRow() []string {
	return []string{"\x00separator\x00"}
}

func isSeparatorRow(row []string) bool {
	return len(row) == 1 && row[0] == "\x00separator\x00"
}

func splitInlineStyle(value string) (string, string, bool) {
	if !strings.HasPrefix(value, "\x00") {
		return "", value, false
	}
	parts := strings.SplitN(value[1:], "\x00", 2)
	if len(parts) != 2 {
		return "", value, false
	}
	return parts[0], parts[1], true
}

func requestFailed(rec proxy.RequestRecord) bool {
	return rec.StatusCode < 200 || rec.StatusCode >= 400 || rec.Error != ""
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
