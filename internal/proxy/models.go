package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"sync"
)

// DiscoveredModel is one model entry returned by an upstream /models endpoint.
// Details retains the complete response object so diagnostics are not limited to
// the standard OpenAI fields.
type DiscoveredModel struct {
	ID      string
	Details json.RawMessage
}

// ModelDiscovery describes the result of querying one configured backend.
type ModelDiscovery struct {
	Backend    string
	URL        string
	StatusCode int
	Models     []DiscoveredModel
	Err        error
	Skipped    bool
}

// DiscoverModels queries every configured backend's OpenAI-compatible /models
// endpoint using the same HTTP and authentication configuration as proxy calls.
// A failed backend is returned alongside successful results so the TUI can show
// a complete diagnostic picture.
func (p *Proxy) DiscoverModels(ctx context.Context) []ModelDiscovery {
	results := make([]ModelDiscovery, len(p.backends))
	var wg sync.WaitGroup
	for i, backend := range p.backends {
		if !backend.cfg.ModelDiscoveryEnabled() {
			results[i] = ModelDiscovery{Backend: backend.cfg.Name, Skipped: true}
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = p.discoverBackendModels(ctx, backend)
		}()
	}
	wg.Wait()
	return results
}

func (p *Proxy) discoverBackendModels(ctx context.Context, backend *resolvedBackend) ModelDiscovery {
	result := ModelDiscovery{Backend: backend.cfg.Name}
	modelsURL, err := buildUpstreamURL(backend.base, &url.URL{Path: "/v1/models"})
	if err != nil {
		result.Err = fmt.Errorf("build models URL: %w", err)
		return result
	}
	result.URL = modelsURL

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsURL, nil)
	if err != nil {
		result.Err = fmt.Errorf("create models request: %w", err)
		return result
	}
	req.Header.Set("Accept", "application/json")
	token, err := backend.tokens.Token(ctx)
	if err != nil {
		result.Err = fmt.Errorf("authenticate: %w", err)
		return result
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := backend.client.Do(req)
	if err != nil {
		result.Err = fmt.Errorf("request models: %w", err)
		return result
	}
	defer resp.Body.Close()
	result.StatusCode = resp.StatusCode
	body, err := io.ReadAll(io.LimitReader(resp.Body, captureLimit+1))
	if err != nil {
		result.Err = fmt.Errorf("read models response: %w", err)
		return result
	}
	if len(body) > captureLimit {
		result.Err = fmt.Errorf("models response exceeds %d byte limit", captureLimit)
		return result
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		result.Err = fmt.Errorf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		return result
	}

	var response struct {
		Data []json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		result.Err = fmt.Errorf("parse models response: %w", err)
		return result
	}
	for _, raw := range response.Data {
		var entry struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(raw, &entry); err != nil {
			result.Err = fmt.Errorf("parse model entry: %w", err)
			return result
		}
		if entry.ID == "" {
			result.Err = fmt.Errorf("models response contains an entry without an id")
			return result
		}
		result.Models = append(result.Models, DiscoveredModel{ID: entry.ID, Details: raw})
	}
	sort.Slice(result.Models, func(i, j int) bool { return result.Models[i].ID < result.Models[j].ID })
	return result
}
