package proxy

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/UnitVectorY-Labs/localmodelproxy/internal/auth"
	"github.com/UnitVectorY-Labs/localmodelproxy/internal/config"
)

func testConfig(models []config.Model) *config.Config {
	var backends []config.BackendConfig
	if len(models) > 0 {
		backends = []config.BackendConfig{{
			Name:    "test",
			Type:    "gcp_openai",
			BaseURL: "http://unused",
			Auth:    config.AuthConfig{Type: "none"},
			Models:  config.BackendModels{Models: models},
		}}
	}
	return &config.Config{
		Server:   config.ServerConfig{Host: "127.0.0.1", Port: 8080},
		Backends: backends,
	}
}

func mustNew(t *testing.T, opts Options) *Proxy {
	t.Helper()
	p, err := New(context.Background(), opts)
	if err != nil {
		t.Fatalf("proxy.New returned error: %v", err)
	}
	return p
}

type failingTokenProvider struct{}

func (failingTokenProvider) Token(context.Context) (string, error) {
	return "", errors.New("token exchange failed")
}

func TestModelsFromConfig(t *testing.T) {
	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "google/gemini-2.5-flash"}}),
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       NewMetrics(),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 1 || body.Data[0].ID != "google/gemini-2.5-flash" {
		t.Fatalf("unexpected models response: %s", rec.Body.String())
	}
}

func TestModelsEmpty(t *testing.T) {
	handler := mustNew(t, Options{
		Config:        testConfig(nil),
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       NewMetrics(),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	var body struct {
		Data []any `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 0 {
		t.Fatalf("expected empty data array, got: %s", rec.Body.String())
	}
}

func TestDiscoverModelsCallsUpstreamWithAuthenticationAndPreservesDetails(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer diagnostic-token" {
			t.Errorf("unexpected authorization %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"object":"list","data":[{"id":"z-model","owned_by":"owner"},{"id":"a-model","custom":{"size":7}}]}`)
	}))
	defer upstream.Close()

	cfg := testConfig([]config.Model{{ID: "a-model"}})
	cfg.Backends[0].BaseURL = upstream.URL + "/v1"
	handler := mustNew(t, Options{Config: cfg, TokenProvider: StaticTokenProvider("diagnostic-token"), Metrics: NewMetrics()})
	results := handler.DiscoverModels(context.Background())

	if len(results) != 1 || results[0].Err != nil {
		t.Fatalf("unexpected discovery result: %#v", results)
	}
	if results[0].StatusCode != http.StatusOK || len(results[0].Models) != 2 {
		t.Fatalf("unexpected discovery response: %#v", results[0])
	}
	if results[0].Models[0].ID != "a-model" || !strings.Contains(string(results[0].Models[0].Details), `"size":7`) {
		t.Fatalf("model details were not sorted/preserved: %#v", results[0].Models)
	}
}

func TestDiscoverModelsReportsBackendHTTPError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not available", http.StatusServiceUnavailable)
	}))
	defer upstream.Close()
	cfg := testConfig([]config.Model{{ID: "missing"}})
	cfg.Backends[0].BaseURL = upstream.URL + "/v1"
	handler := mustNew(t, Options{Config: cfg, TokenProvider: StaticTokenProvider("token"), Metrics: NewMetrics()})

	results := handler.DiscoverModels(context.Background())
	if len(results) != 1 || results[0].Err == nil || results[0].Err.Error() != "HTTP 503 Service Unavailable" {
		t.Fatalf("expected per-backend HTTP error, got %#v", results)
	}
}

func TestDiscoverModelsSkipsDisabledBackends(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer upstream.Close()

	disabled := false
	cfg := testConfig([]config.Model{{ID: "model"}})
	cfg.Backends[0].BaseURL = upstream.URL + "/v1"
	cfg.Backends[0].ModelDiscovery = &disabled
	handler := mustNew(t, Options{Config: cfg, TokenProvider: StaticTokenProvider("token"), Metrics: NewMetrics()})

	results := handler.DiscoverModels(context.Background())
	if len(results) != 1 || !results[0].Skipped || results[0].Err != nil || called {
		t.Fatalf("expected disabled discovery without an upstream request, got %#v (called=%t)", results, called)
	}
}

func TestChatCompletionsMissingModel(t *testing.T) {
	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "google/gemini-2.5-flash"}}),
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       NewMetrics(),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"messages":[]}`))
	req.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("expected JSON error body: %v, got: %s", err, rec.Body.String())
	}
	if body.Error.Code != "invalid_request" {
		t.Fatalf("unexpected error code: %s", body.Error.Code)
	}
}

func TestChatCompletionsModelNotFound(t *testing.T) {
	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "google/gemini-2.5-flash"}}),
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       NewMetrics(),
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"unknown-model"}`))
	req.Header.Set("Content-Type", "application/json")

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("expected JSON error body: %v, got: %s", err, rec.Body.String())
	}
	if body.Error.Code != "model_not_found" {
		t.Fatalf("unexpected error code: %s", body.Error.Code)
	}
}

func TestNonChatEndpointRejected(t *testing.T) {
	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "google/gemini-2.5-flash"}}),
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       NewMetrics(),
	})

	for _, path := range []string{"/v1/responses", "/v1/completions", "/v2/chat/completions"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"google/gemini-2.5-flash"}`))
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("path %s: expected 404, got %d", path, rec.Code)
		}
	}
}

func TestForwardStripsV1AndAuthorization(t *testing.T) {
	var seenPath string
	var seenAuth string
	var seenBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.String()
		seenAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &seenBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"google/gemini-3.1-flash-lite-preview","usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer upstream.Close()

	metrics := NewMetrics()
	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "gemini-3.1-flash-lite-preview"}}),
		TokenProvider: StaticTokenProvider("replacement"),
		Metrics:       metrics,
		UpstreamBase:  upstream.URL,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions?x=1", strings.NewReader(`{"model":"gemini-3.1-flash-lite-preview"}`))
	req.Header.Set("Authorization", "Bearer caller")
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if seenPath != "/chat/completions?x=1" {
		t.Fatalf("unexpected upstream path: %s", seenPath)
	}
	if seenAuth != "Bearer replacement" {
		t.Fatalf("unexpected auth header: %s", seenAuth)
	}
	if seenBody["model"] != "google/gemini-3.1-flash-lite-preview" {
		t.Fatalf("unexpected body: %#v", seenBody)
	}
	snapshot := metrics.Snapshot()
	if snapshot.TotalTokens != 3 || snapshot.Models["gemini-3.1-flash-lite-preview"].Requests != 1 {
		t.Fatalf("usage was not aggregated: %#v", snapshot)
	}
}

func TestForwardAllowsBackendInsecureSkipVerify(t *testing.T) {
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"tls-model","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	cfg := testConfig([]config.Model{{ID: "tls-model"}})
	cfg.Backends[0].Type = "openai_compatible"
	cfg.Backends[0].BaseURL = upstream.URL
	cfg.Backends[0].InsecureSkipVerify = true

	handler := mustNew(t, Options{
		Config:        cfg,
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       NewMetrics(),
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"tls-model"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestForwardPreservesCallerUserAgentOnly(t *testing.T) {
	var seenUserAgents []string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenUserAgents = append(seenUserAgents, r.Header.Get("User-Agent"))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"google/gemini","usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "google/gemini"}}),
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       NewMetrics(),
		UpstreamBase:  upstream.URL,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"google/gemini"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"google/gemini"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "original-app/1.0")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	if len(seenUserAgents) != 2 {
		t.Fatalf("expected two upstream requests, got %d", len(seenUserAgents))
	}
	if seenUserAgents[0] != "" {
		t.Fatalf("expected no synthetic user-agent, got %q", seenUserAgents[0])
	}
	if seenUserAgents[1] != "original-app/1.0" {
		t.Fatalf("expected caller user-agent to pass through, got %q", seenUserAgents[1])
	}
}

func TestRecentRequestsOnlyIncludeModelCallsIncludingFailures(t *testing.T) {
	metrics := NewMetrics()
	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "google/gemini"}}),
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       metrics,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected models status: %d", rec.Code)
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"unknown"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("unexpected chat status: %d", rec.Code)
	}

	snapshot := metrics.Snapshot()
	if snapshot.TotalRequests != 2 || snapshot.ModelRequests != 1 {
		t.Fatalf("unexpected request totals: %#v", snapshot)
	}
	if _, ok := snapshot.Models["unknown"]; ok {
		t.Fatalf("unknown failed model should not be added to model stats: %#v", snapshot.Models)
	}
	if len(snapshot.Recent) != 1 {
		t.Fatalf("expected one recent model request, got %#v", snapshot.Recent)
	}
	if snapshot.Recent[0].Path != "/v1/chat/completions" || snapshot.Recent[0].Model != "unknown" || snapshot.Recent[0].StatusCode != http.StatusBadRequest {
		t.Fatalf("unexpected recent request: %#v", snapshot.Recent[0])
	}
	if snapshot.Recent[0].Sequence != 1 {
		t.Fatalf("expected first model request sequence to be 1, got %d", snapshot.Recent[0].Sequence)
	}
}

func TestRecentRequestSequenceIsMonotonic(t *testing.T) {
	metrics := NewMetrics()
	for range 12 {
		record := metrics.Begin(http.MethodPost, "/v1/chat/completions")
		record.Model = "model"
		record.StatusCode = http.StatusOK
		metrics.Finish(record)
	}

	snapshot := metrics.Snapshot()
	if snapshot.ModelRequests != 12 {
		t.Fatalf("expected 12 model requests, got %d", snapshot.ModelRequests)
	}
	if len(snapshot.Recent) != 12 {
		t.Fatalf("expected 12 recent requests, got %d", len(snapshot.Recent))
	}
	if snapshot.Recent[0].Sequence != 12 || snapshot.Recent[1].Sequence != 11 {
		t.Fatalf("expected newest requests first with monotonic sequence, got %#v", snapshot.Recent[:2])
	}
}

func TestCostAggregatesPerRequestModelAndTotal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"priced","usage":{"prompt_tokens":1000,"completion_tokens":2000,"total_tokens":3000,"prompt_tokens_details":{"cached_tokens":250}}}`))
	}))
	defer upstream.Close()

	cfg := testConfig([]config.Model{{
		ID:   "priced",
		Cost: config.ModelCost{InputPerMillion: 1, OutputPerMillion: 2, CachePerMillion: 0.5},
	}})
	metrics := NewMetrics()
	handler := mustNew(t, Options{
		Config:        cfg,
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       metrics,
		UpstreamBase:  upstream.URL,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"priced"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}

	snapshot := metrics.Snapshot()
	want := 750.0/1_000_000 + 2*2000.0/1_000_000 + 0.5*250.0/1_000_000
	if math.Abs(snapshot.TotalCostUSD-want) > 0.0000001 {
		t.Fatalf("unexpected total cost: got %.10f want %.10f", snapshot.TotalCostUSD, want)
	}
	if math.Abs(snapshot.Models["priced"].CostUSD-want) > 0.0000001 {
		t.Fatalf("unexpected model cost: %#v want %.10f", snapshot.Models["priced"], want)
	}
	if got := snapshot.Recent[0].CostUSD; math.Abs(got-want) > 0.0000001 {
		t.Fatalf("unexpected request cost: got %.10f want %.10f", got, want)
	}
	if snapshot.InputTokens != 750 || snapshot.CachedTokens != 250 {
		t.Fatalf("expected cached tokens to be split out from input: %#v", snapshot)
	}
}

func TestForwardSetsRewrittenContentLength(t *testing.T) {
	var seenContentLength int64
	var seenBody []byte
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenContentLength = r.ContentLength
		seenBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"upstream/heavy-model-v2","usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer upstream.Close()

	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "my-local-model", UpstreamID: "upstream/heavy-model-v2"}}),
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       NewMetrics(),
		UpstreamBase:  upstream.URL,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"my-local-model"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Length", "999")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if seenContentLength != int64(len(seenBody)) {
		t.Fatalf("upstream ContentLength = %d, want %d for body %s", seenContentLength, len(seenBody), seenBody)
	}
	if !strings.Contains(string(seenBody), `"model":"upstream/heavy-model-v2"`) {
		t.Fatalf("request body was not rewritten upstream: %s", seenBody)
	}
}

func TestCopyRequestHeadersSkipsManagedHeaders(t *testing.T) {
	src := http.Header{}
	src.Set("Authorization", "Bearer caller")
	src.Set("Host", "example.test")
	src.Set("Accept-Encoding", "br")
	src.Set("Content-Length", "999")
	src.Set("Content-Type", "application/json")

	dst := http.Header{}
	copyRequestHeaders(dst, src)

	for _, key := range []string{"Authorization", "Host", "Accept-Encoding", "Content-Length"} {
		if got := dst.Get(key); got != "" {
			t.Fatalf("expected %s to be skipped, got %q", key, got)
		}
	}
	if got := dst.Get("Content-Type"); got != "application/json" {
		t.Fatalf("expected Content-Type to be copied, got %q", got)
	}
}

func TestForwardHandlesGzippedJSONWhenCallerSendsAcceptEncoding(t *testing.T) {
	var seenAcceptEncoding string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAcceptEncoding = r.Header.Get("Accept-Encoding")
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		_, _ = gz.Write([]byte(`{"model":"upstream/heavy-model-v2","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":2,"completion_tokens":3,"total_tokens":5}}`))
		_ = gz.Close()
	}))
	defer upstream.Close()

	metrics := NewMetrics()
	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "my-local-model", UpstreamID: "upstream/heavy-model-v2"}}),
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       metrics,
		UpstreamBase:  upstream.URL,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"my-local-model"}`))
	req.Header.Set("Accept-Encoding", "br")
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	if seenAcceptEncoding == "br" {
		t.Fatalf("caller Accept-Encoding was forwarded upstream")
	}
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("proxy response should not advertise gzip after transport decompression")
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("proxy did not return plain JSON: %v body=%q", err, rec.Body.String())
	}
	if body.Model != "my-local-model" {
		t.Fatalf("expected rewritten response model %q, got %q", "my-local-model", body.Model)
	}
	snapshot := metrics.Snapshot()
	if snapshot.TotalTokens != 5 || snapshot.Models["my-local-model"].Requests != 1 {
		t.Fatalf("usage was not aggregated from decompressed body: %#v", snapshot)
	}
}

func TestStreamingFlushesAndAggregatesUsage(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"model\":\"google/gemini\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		flusher.Flush()
		time.Sleep(50 * time.Millisecond)
		_, _ = w.Write([]byte("data: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n"))
		flusher.Flush()
	}))
	defer upstream.Close()

	metrics := NewMetrics()
	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "google/gemini"}}),
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       metrics,
		UpstreamBase:  upstream.URL,
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"google/gemini"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	buf := make([]byte, 16)
	n, err := resp.Body.Read(buf)
	if err != nil {
		t.Fatalf("expected streamed bytes: %v", err)
	}
	if !strings.Contains(string(buf[:n]), "data:") {
		t.Fatalf("expected first streaming chunk, got %q", string(buf[:n]))
	}
	_, _ = io.ReadAll(resp.Body)

	snapshot := metrics.Snapshot()
	if snapshot.TotalTokens != 5 {
		t.Fatalf("usage was not aggregated: %#v", snapshot)
	}
}

func TestFileLogRecordsRequestAndStreamingPayloads(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"model\":\"google/gemini\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"usage\":{\"prompt_tokens\":2,\"completion_tokens\":3,\"total_tokens\":5}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	var logs bytes.Buffer
	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "google/gemini"}}),
		TokenProvider: StaticTokenProvider("very-secret-token"),
		Metrics:       NewMetrics(),
		UpstreamBase:  upstream.URL,
		LogOutput:     &logs,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"google/gemini","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	got := logs.String()
	assertLogTimestamps(t, got)
	for _, want := range []string{
		`request method=POST path=/v1/chat/completions model="google/gemini" backend=test token=very-secret-token`,
		"payload:\n{\n  \"model\": \"google/gemini\",\n  \"stream\": true\n}",
		`response_stream model="google/gemini" backend=test status=200`,
		`"content": "hi"`,
		`"prompt_tokens": 2`,
		`request_done method=POST path=/v1/chat/completions model="google/gemini" status=200 backend=test`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("file logs missing %q in:\n%s", want, got)
		}
	}
}

func TestFileLogRecordsPreForwardFailures(t *testing.T) {
	tests := []struct {
		name          string
		config        *config.Config
		tokenProvider auth.TokenProvider
		body          string
		wantStatus    int
		wantLog       string
	}{
		{
			name:          "unknown model",
			config:        testConfig([]config.Model{{ID: "google/gemini"}}),
			tokenProvider: StaticTokenProvider("token"),
			body:          `{"model":"unknown-model"}`,
			wantStatus:    http.StatusBadRequest,
			wantLog:       "request_failed method=POST path=/v1/chat/completions model=\"unknown-model\" status=400 error=\"model not found\"\npayload:\n{\n  \"model\": \"unknown-model\"\n}",
		},
		{
			name:          "token failure",
			config:        testConfig([]config.Model{{ID: "google/gemini"}}),
			tokenProvider: failingTokenProvider{},
			body:          `{"model":"google/gemini"}`,
			wantStatus:    http.StatusUnauthorized,
			wantLog:       "request_failed method=POST path=/v1/chat/completions model=\"google/gemini\" backend=test status=401 error=\"token exchange failed\"\npayload:\n{\n  \"model\": \"google/gemini\"\n}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logs bytes.Buffer
			handler := mustNew(t, Options{
				Config:        tt.config,
				TokenProvider: tt.tokenProvider,
				Metrics:       NewMetrics(),
				UpstreamBase:  "http://unused",
				LogOutput:     &logs,
			})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("unexpected status: got %d want %d body=%s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !strings.Contains(logs.String(), tt.wantLog) {
				t.Fatalf("file logs missing %q in:\n%s", tt.wantLog, logs.String())
			}
			assertLogTimestamps(t, logs.String())
		})
	}
}

func assertLogTimestamps(t *testing.T, logs string) {
	t.Helper()
	timestamp := regexp.MustCompile(`(?m)^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2}) `)
	if !timestamp.MatchString(logs) {
		t.Fatalf("logs missing RFC3339 timestamp prefix:\n%s", logs)
	}
}

func TestNonStreamingResponseModelRewritten(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"model":"upstream/heavy-model-v2","choices":[{"message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
	}))
	defer upstream.Close()

	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "my-local-model", UpstreamID: "upstream/heavy-model-v2"}}),
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       NewMetrics(),
		UpstreamBase:  upstream.URL,
	})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"my-local-model"}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("could not parse response body: %v, body=%s", err, rec.Body.String())
	}
	if body.Model != "my-local-model" {
		t.Fatalf("expected model %q in response, got %q", "my-local-model", body.Model)
	}
}

func TestStreamingResponseModelRewritten(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"model\":\"upstream/heavy-model-v2\",\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		flusher.Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		flusher.Flush()
	}))
	defer upstream.Close()

	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "my-local-model", UpstreamID: "upstream/heavy-model-v2"}}),
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       NewMetrics(),
		UpstreamBase:  upstream.URL,
	})

	server := httptest.NewServer(handler)
	defer server.Close()

	resp, err := http.Post(server.URL+"/v1/chat/completions", "application/json", strings.NewReader(`{"model":"my-local-model"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}

	lines := strings.Split(string(body), "\n")
	found := false
	for _, line := range lines {
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]
		if data == "[DONE]" {
			continue
		}
		var chunk struct {
			Model string `json:"model"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Model != "my-local-model" {
			t.Fatalf("expected model %q in SSE chunk, got %q (line: %s)", "my-local-model", chunk.Model, line)
		}
		found = true
	}
	if !found {
		t.Fatalf("no SSE data chunk with model field found in response:\n%s", body)
	}
}

func TestStreamingThoughtSignatureReinjected(t *testing.T) {
	var requestCount int
	var secondBody map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		body, _ := io.ReadAll(r.Body)
		if requestCount == 2 {
			_ = json.Unmarshal(body, &secondBody)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requestCount == 1 {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"function\":{\"name\":\"bash\",\"arguments\":\"{}\"},\"extra_content\":{\"google\":{\"thought_signature\":\"sig-1\"}}}]}}]}\n\n"))
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer upstream.Close()

	handler := mustNew(t, Options{
		Config:        testConfig([]config.Model{{ID: "google/gemini"}}),
		TokenProvider: StaticTokenProvider("token"),
		Metrics:       NewMetrics(),
		UpstreamBase:  upstream.URL,
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"google/gemini","stream":true}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("first request status: %d body=%s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"google/gemini","messages":[{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"bash","arguments":"{}"}}]}]}`))
	req.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("second request status: %d body=%s", rec.Code, rec.Body.String())
	}

	messages := secondBody["messages"].([]any)
	assistant := messages[0].(map[string]any)
	toolCalls := assistant["tool_calls"].([]any)
	signature := thoughtSignature(toolCalls[0].(map[string]any))
	if signature != "sig-1" {
		t.Fatalf("thought signature was not reinjected, got %q in %#v", signature, secondBody)
	}
}
