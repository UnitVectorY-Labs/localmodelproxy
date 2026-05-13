package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/UnitVectorY-Labs/localmodelproxy/internal/config"
)

func testConfig(models []config.Model) *config.Config {
	return &config.Config{
		Server: config.ServerConfig{Host: "127.0.0.1", Port: 8080},
		Vertex: config.VertexConfig{Project: "p", Location: "global"},
		Models: models,
		UI:     config.UIConfig{Mode: "plain"},
	}
}

func TestModelsFromConfig(t *testing.T) {
	handler := New(Options{
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
	handler := New(Options{
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

func TestChatCompletionsMissingModel(t *testing.T) {
	handler := New(Options{
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
	handler := New(Options{
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
	handler := New(Options{
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
	handler := New(Options{
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
	handler := New(Options{
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
