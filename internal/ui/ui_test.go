package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/UnitVectorY-Labs/localmodelproxy/internal/config"
	"github.com/UnitVectorY-Labs/localmodelproxy/internal/proxy"
	tea "github.com/charmbracelet/bubbletea"
)

func TestMouseClickSelectsTabAndModel(t *testing.T) {
	m := testTUIModel()
	lines := m.hitTestLines()

	updated, _ := m.Update(tea.MouseMsg{
		X:      strings.Index(lines[findLine(t, lines, "[Stats]")], "[Test]") + 1,
		Y:      screenY(findLine(t, lines, "[Stats]")),
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	m = updated.(tuiModel)
	if m.activeTab != tabTest {
		t.Fatalf("expected Test tab after click, got %d", m.activeTab)
	}

	lines = m.hitTestLines()
	modelY := screenY(findLine(t, lines, "alpha"))
	modelRowX := tableLineWidth(fitColumns(visibleColumns(testModelTableColumns(false)), m.tableWidth())) - 1
	updated, _ = m.Update(tea.MouseMsg{
		X:      modelRowX,
		Y:      modelY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	m = updated.(tuiModel)
	if got := m.allModels[m.modelCursor]; got != "alpha" {
		t.Fatalf("expected alpha model after click, got %q", got)
	}
}

func TestMouseClickTestTabDoesNotStartSelectedModelTest(t *testing.T) {
	m := testTUIModel()
	m.modelCursor = 1
	lines := m.hitTestLines()

	updated, cmd := m.Update(tea.MouseMsg{
		X:      strings.Index(lines[findLine(t, lines, "[Stats]")], "[Test]") + 1,
		Y:      screenY(findLine(t, lines, "[Stats]")),
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	m = updated.(tuiModel)
	if m.activeTab != tabTest {
		t.Fatalf("expected Test tab after click, got %d", m.activeTab)
	}
	if m.testRunning {
		t.Fatal("did not expect testRunning after clicking Test tab")
	}
	if cmd != nil {
		t.Fatal("did not expect test command after clicking Test tab")
	}
}

func TestReconcileModelsCombinesConfigAndDiscovery(t *testing.T) {
	cfg := &config.Config{Backends: []config.BackendConfig{{
		Name: "backend", Models: config.BackendModels{Models: []config.Model{
			{ID: "local-valid", UpstreamID: "upstream-valid"},
			{ID: "local-missing"},
		}},
	}}}
	results := []proxy.ModelDiscovery{{Backend: "backend", StatusCode: 200, Models: []proxy.DiscoveredModel{
		{ID: "upstream-valid", Details: []byte(`{"id":"upstream-valid","owned_by":"test"}`)},
		{ID: "unconfigured", Details: []byte(`{"id":"unconfigured"}`)},
	}}}

	rows := reconcileModels(cfg, results)
	if len(rows) != 3 {
		t.Fatalf("expected three combined rows, got %#v", rows)
	}
	statuses := map[string]string{}
	for _, row := range rows {
		statuses[row.upstreamID] = row.status
	}
	if statuses["upstream-valid"] != "MATCH" || statuses["local-missing"] != "MISSING" || statuses["unconfigured"] != "UNCONFIGURED" {
		t.Fatalf("unexpected reconciliation statuses: %#v", statuses)
	}
}

func TestReconcileModelsUsesUnknownWhenDiscoveryFails(t *testing.T) {
	cfg := &config.Config{Backends: []config.BackendConfig{{
		Name: "backend", Models: config.BackendModels{Models: []config.Model{{ID: "model"}}},
	}}}
	rows := reconcileModels(cfg, []proxy.ModelDiscovery{{Backend: "backend", Err: fmt.Errorf("unavailable")}})
	if len(rows) != 1 || rows[0].status != "UNKNOWN" || rows[0].rowStyle != "yellow" {
		t.Fatalf("failed discovery must not report a model missing: %#v", rows)
	}
}

func TestModelsViewShowsDisabledDiscoveryWithoutError(t *testing.T) {
	m := testTUIModel()
	m.activeTab = tabModels
	m.discoveryLoaded = true
	m.discoveryResults = []proxy.ModelDiscovery{{Backend: "google", Skipped: true}}
	m.diagnosticModels = reconcileModels(m.cfg, m.discoveryResults)

	view := m.viewModels(m.tableWidth())
	if !strings.Contains(view, "model discovery disabled by configuration") {
		t.Fatalf("expected disabled-discovery message, got:\n%s", view)
	}
}

func TestModelsTabOpensDetailsAndReturns(t *testing.T) {
	m := testTUIModel()
	m.activeTab = tabModels
	m.discoveryLoaded = true
	m.diagnosticModels = []diagnosticModel{{upstreamID: "alpha", details: `{"id":"alpha"}`}}

	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = updated.(tuiModel)
	if !m.diagnosticDetail || !strings.Contains(m.View(), "Model Details") {
		t.Fatal("expected model details page")
	}
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = updated.(tuiModel)
	if m.diagnosticDetail {
		t.Fatal("expected escape to return to model list")
	}
}

func TestModelsDiagnosticUsesColorOrStatusColumn(t *testing.T) {
	m := testTUIModel()
	m.activeTab = tabModels
	m.discoveryLoaded = true
	m.diagnosticModels = []diagnosticModel{{backend: "backend", localID: "model", upstreamID: "model", status: "MATCH", rowStyle: "green"}}

	m.color = true
	colored := stripANSIEscapes(m.viewModels(m.tableWidth()))
	if strings.Contains(colored, "Status") || strings.Contains(colored, "Green:") {
		t.Fatalf("colored view should use color without color-name labels:\n%s", colored)
	}
	if !strings.Contains(colored, "config + response") {
		t.Fatalf("colored view should retain a descriptive legend:\n%s", colored)
	}

	m.color = false
	plain := m.viewModels(m.tableWidth())
	if !strings.Contains(plain, "Status") || !strings.Contains(plain, "MATCH") {
		t.Fatalf("plain view should add a textual status column:\n%s", plain)
	}
}

func TestMouseClickTestModelRowStartsClickedModelTest(t *testing.T) {
	m := testTUIModel()
	m.activeTab = tabTest
	m.modelCursor = 1
	lines := m.hitTestLines()

	updated, cmd := m.Update(tea.MouseMsg{
		X:      3,
		Y:      screenY(findLine(t, lines, "beta")),
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	m = updated.(tuiModel)
	if got := m.allModels[m.modelCursor]; got != "beta" {
		t.Fatalf("expected beta model after click, got %q", got)
	}
	if !m.testRunning {
		t.Fatal("expected testRunning after clicking Test tab model row")
	}
	if cmd == nil {
		t.Fatal("expected test command after clicking Test tab model row")
	}
}

func TestMouseClickSelectsStatsTotal(t *testing.T) {
	m := testTUIModel()
	m.modelCursor = 1

	lines := m.hitTestLines()
	totalY := screenY(findLastLine(t, lines, "Total"))
	totalRowX := tableLineWidth(fitColumns(visibleColumns(modelTableColumns(false)), m.tableWidth())) - 1
	updated, _ := m.Update(tea.MouseMsg{
		X:      totalRowX,
		Y:      totalY,
		Action: tea.MouseActionPress,
		Button: tea.MouseButtonLeft,
	})
	m = updated.(tuiModel)
	if got := m.allModels[m.modelCursor]; got != "Total" {
		t.Fatalf("expected Total after click, got %q", got)
	}
}

func TestMouseMoveSetsHoverTarget(t *testing.T) {
	m := testTUIModel()
	lines := m.hitTestLines()
	tabY := screenY(findLine(t, lines, "[Stats]"))

	updated, _ := m.Update(tea.MouseMsg{
		X:      1,
		Y:      tabY,
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonNone,
	})
	m = updated.(tuiModel)
	if m.hover.kind != hoverTab || m.hover.tab != tabStats {
		t.Fatalf("expected Stats tab hover, got %#v", m.hover)
	}

	modelY := screenY(findLine(t, lines, "alpha"))
	updated, _ = m.Update(tea.MouseMsg{
		X:      3,
		Y:      modelY,
		Action: tea.MouseActionMotion,
		Button: tea.MouseButtonNone,
	})
	m = updated.(tuiModel)
	if m.hover.kind != hoverModel || m.hover.model != "alpha" {
		t.Fatalf("expected alpha model hover, got %#v", m.hover)
	}
}

func TestRecentRowsUseDashesForMissingUsageValues(t *testing.T) {
	rows := recentRowsFiltered(proxy.Snapshot{
		Recent: []proxy.RequestRecord{
			{Sequence: 1, Model: "alpha"},
		},
	}, 10, true, "")

	if len(rows) != 1 {
		t.Fatalf("expected one recent row, got %d", len(rows))
	}
	for _, col := range []int{3, 4, 5, 6, 7, 8} {
		if rows[0][col] != "-" {
			t.Fatalf("expected dash at column %d, got %q in row %#v", col, rows[0][col], rows[0])
		}
	}
}

func TestStyledCellRendersDashWithDashStyle(t *testing.T) {
	got := styledCell(true, withStyle("bold", "-"), 6, true, "")
	want := style(true, "dash").Render(cell("-", 6, true))
	if got != want {
		t.Fatalf("expected dash style override, got %q want %q", got, want)
	}
}

func testTUIModel() tuiModel {
	cfg := &config.Config{
		Server: config.ServerConfig{Host: "127.0.0.1", Port: 8080},
		Backends: []config.BackendConfig{
			{
				Name: "test",
				Type: "openai_compatible",
				Models: config.BackendModels{Models: []config.Model{
					{ID: "beta"},
					{ID: "alpha"},
				}},
			},
		},
	}
	m := tuiModel{
		cfg:         cfg,
		metrics:     proxy.NewMetrics(),
		width:       100,
		height:      40,
		allModels:   []string{"Total", "alpha", "beta"},
		testResults: make(map[string]string),
	}
	return m
}

func findLine(t *testing.T, lines []string, text string) int {
	t.Helper()
	for i, line := range lines {
		if strings.Contains(line, text) {
			return i
		}
	}
	t.Fatalf("could not find line containing %q in:\n%s", text, strings.Join(lines, "\n"))
	return -1
}

func screenY(renderedLine int) int {
	return renderedLine - screenTopPadding
}

func findLastLine(t *testing.T, lines []string, text string) int {
	t.Helper()
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.Contains(lines[i], text) {
			return i
		}
	}
	t.Fatalf("could not find line containing %q in:\n%s", text, strings.Join(lines, "\n"))
	return -1
}
