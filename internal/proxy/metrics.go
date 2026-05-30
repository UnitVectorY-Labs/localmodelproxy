package proxy

import (
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/UnitVectorY-Labs/localmodelproxy/internal/usage"
)

const maxRecent = 100

type Metrics struct {
	mu sync.RWMutex

	totalRequests  int64
	modelRequests  int64
	active         int64
	successes      int64
	failures       int64
	inputTokens    int64
	outputTokens   int64
	thinkingTokens int64
	cachedTokens   int64
	totalTokens    int64
	totalCostUSD   float64
	statusCodes    map[int]int64
	models         map[string]ModelStats
	recent         []RequestRecord
}

type ModelStats struct {
	Requests       int64
	InputTokens    int64
	OutputTokens   int64
	ThinkingTokens int64
	CachedTokens   int64
	TotalTokens    int64
	CostUSD        float64
}

type RequestRecord struct {
	Sequence   int64
	Method     string
	Path       string
	Model      string
	StatusCode int
	Duration   time.Duration
	Error      string
	Usage      usage.TokenUsage
	CostUSD    float64
	StartedAt  time.Time
}

type Snapshot struct {
	TotalRequests  int64
	ModelRequests  int64
	Active         int64
	Successes      int64
	Failures       int64
	InputTokens    int64
	OutputTokens   int64
	ThinkingTokens int64
	CachedTokens   int64
	TotalTokens    int64
	TotalCostUSD   float64
	StatusCodes    map[int]int64
	Models         map[string]ModelStats
	Recent         []RequestRecord
}

func NewMetrics() *Metrics {
	return &Metrics{
		statusCodes: make(map[int]int64),
		models:      make(map[string]ModelStats),
	}
}

func (m *Metrics) Begin(method, path string) *RequestRecord {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.totalRequests++
	m.active++
	return &RequestRecord{
		Method:    method,
		Path:      path,
		StartedAt: time.Now(),
	}
}

func (m *Metrics) Finish(record *RequestRecord) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.active--
	record.Duration = time.Since(record.StartedAt)
	if record.StatusCode >= 200 && record.StatusCode < 400 && record.Error == "" {
		m.successes++
	} else {
		m.failures++
	}
	if record.StatusCode != 0 {
		m.statusCodes[record.StatusCode]++
	}

	m.inputTokens += record.Usage.InputTokens
	m.outputTokens += record.Usage.OutputTokens
	m.thinkingTokens += record.Usage.ThinkingTokens
	m.cachedTokens += record.Usage.CachedTokens
	m.totalTokens += record.Usage.TotalTokens
	m.totalCostUSD += record.CostUSD
	if shouldAggregateModelStats(record) {
		stats := m.models[record.Model]
		stats.Requests++
		stats.InputTokens += record.Usage.InputTokens
		stats.OutputTokens += record.Usage.OutputTokens
		stats.ThinkingTokens += record.Usage.ThinkingTokens
		stats.CachedTokens += record.Usage.CachedTokens
		stats.TotalTokens += record.Usage.TotalTokens
		stats.CostUSD += record.CostUSD
		m.models[record.Model] = stats
	}

	if isModelRequest(record) {
		m.modelRequests++
		record.Sequence = m.modelRequests
		m.recent = append([]RequestRecord{*record}, m.recent...)
		if len(m.recent) > maxRecent {
			m.recent = m.recent[:maxRecent]
		}
	}
}

func (m *Metrics) Snapshot() Snapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()

	statusCodes := make(map[int]int64, len(m.statusCodes))
	maps.Copy(statusCodes, m.statusCodes)
	models := make(map[string]ModelStats, len(m.models))
	maps.Copy(models, m.models)
	recent := append([]RequestRecord(nil), m.recent...)

	return Snapshot{
		TotalRequests:  m.totalRequests,
		ModelRequests:  m.modelRequests,
		Active:         m.active,
		Successes:      m.successes,
		Failures:       m.failures,
		InputTokens:    m.inputTokens,
		OutputTokens:   m.outputTokens,
		ThinkingTokens: m.thinkingTokens,
		CachedTokens:   m.cachedTokens,
		TotalTokens:    m.totalTokens,
		TotalCostUSD:   m.totalCostUSD,
		StatusCodes:    statusCodes,
		Models:         models,
		Recent:         recent,
	}
}

func isModelRequest(record *RequestRecord) bool {
	return record.Path == "/v1/chat/completions" && record.Model != ""
}

func shouldAggregateModelStats(record *RequestRecord) bool {
	return isModelRequest(record) && record.StatusCode >= 200 && record.StatusCode < 400 && record.Error == ""
}

func (s Snapshot) SortedModelNames() []string {
	names := make([]string, 0, len(s.Models))
	for name := range s.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
