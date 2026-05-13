package proxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/UnitVectorY-Labs/localmodelproxy/internal/auth"
	"github.com/UnitVectorY-Labs/localmodelproxy/internal/config"
	"github.com/UnitVectorY-Labs/localmodelproxy/internal/usage"
)

const captureLimit = 8 * 1024 * 1024

type Options struct {
	Config        *config.Config
	TokenProvider auth.TokenProvider
	Metrics       *Metrics
	HTTPClient    *http.Client
	UpstreamBase  string
}

type Proxy struct {
	cfg          *config.Config
	tokens       auth.TokenProvider
	metrics      *Metrics
	client       *http.Client
	upstreamBase string
}

func New(opts Options) *Proxy {
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	base := opts.UpstreamBase
	if base == "" {
		base = opts.Config.VertexBaseURL()
	}
	return &Proxy{
		cfg:          opts.Config,
		tokens:       opts.TokenProvider,
		metrics:      opts.Metrics,
		client:       client,
		upstreamBase: strings.TrimRight(base, "/"),
	}
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
		return
	}

	record := p.metrics.Begin(r.Method, r.URL.Path)
	defer p.metrics.Finish(record)

	if r.URL.Path == "/v1/models" && r.Method == http.MethodGet && len(p.cfg.Models) > 0 {
		writeModels(w, p.cfg.Models)
		record.StatusCode = http.StatusOK
		return
	}

	if !strings.HasPrefix(r.URL.Path, "/v1/") {
		http.Error(w, "not found", http.StatusNotFound)
		record.StatusCode = http.StatusNotFound
		record.Error = "not found"
		return
	}

	if r.URL.Path == "/v1/models" && r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		record.StatusCode = http.StatusMethodNotAllowed
		record.Error = "method not allowed"
		return
	}

	if err := p.forward(w, r, record); err != nil {
		if r.URL.Path == "/v1/models" {
			record.StatusCode = http.StatusServiceUnavailable
			record.Error = err.Error()
			http.Error(w, "model list is not configured and upstream /models could not be reached; add models to ~/.localmodelproxy or verify Vertex AI access", http.StatusServiceUnavailable)
			return
		}
		if record.StatusCode == 0 {
			record.StatusCode = http.StatusBadGateway
		}
		record.Error = err.Error()
		http.Error(w, err.Error(), record.StatusCode)
	}
}

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, record *RequestRecord) error {
	token, err := p.tokens.Token(r.Context())
	if err != nil {
		record.StatusCode = http.StatusUnauthorized
		return err
	}

	body, localModel, err := p.prepareRequestBody(r)
	if err != nil {
		record.StatusCode = http.StatusBadRequest
		return err
	}
	record.Model = localModel
	upstreamURL, err := p.upstreamURL(r.URL)
	if err != nil {
		record.StatusCode = http.StatusBadGateway
		return err
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, upstreamURL, body)
	if err != nil {
		record.StatusCode = http.StatusBadGateway
		return err
	}
	copyRequestHeaders(req.Header, r.Header)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := p.client.Do(req)
	if err != nil {
		record.StatusCode = http.StatusBadGateway
		return err
	}
	defer resp.Body.Close()

	record.StatusCode = resp.StatusCode
	copyResponseHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	responseCapture := &limitedBuffer{limit: captureLimit}
	_, copyErr := streamCopy(w, resp.Body, responseCapture)
	if parsedUsage, model, ok := parseUsage(resp.Header, responseCapture.Bytes()); ok {
		record.Usage = parsedUsage
		if model != "" {
			record.Model = p.cfg.LocalModelID(model)
		}
	}
	if copyErr != nil {
		record.Error = copyErr.Error()
		return copyErr
	}
	return nil
}

func (p *Proxy) prepareRequestBody(r *http.Request) (io.ReadCloser, string, error) {
	if r.Body == nil {
		return io.NopCloser(bytes.NewReader(nil)), "", nil
	}
	defer r.Body.Close()

	body, err := readLimited(r.Body, captureLimit)
	if err != nil {
		return nil, "", err
	}

	localModel := usage.ParseModel(body)
	if shouldRewriteModel(r) && localModel != "" {
		rewritten, changed, err := p.rewriteModel(body, localModel)
		if err != nil {
			return nil, localModel, err
		}
		if changed {
			body = rewritten
			r.ContentLength = int64(len(body))
		}
	}

	return io.NopCloser(bytes.NewReader(body)), localModel, nil
}

func shouldRewriteModel(r *http.Request) bool {
	if r.Method != http.MethodPost {
		return false
	}
	return r.URL.Path == "/v1/chat/completions" || r.URL.Path == "/v1/responses"
}

func (p *Proxy) rewriteModel(body []byte, localModel string) ([]byte, bool, error) {
	upstreamModel := p.cfg.UpstreamModelID(localModel)
	if upstreamModel == localModel {
		return body, false, nil
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, false, fmt.Errorf("failed to parse JSON request body for model rewrite: %w", err)
	}
	payload["model"] = upstreamModel
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return nil, false, fmt.Errorf("failed to rewrite request model: %w", err)
	}
	return rewritten, true, nil
}

func readLimited(r io.Reader, limit int) ([]byte, error) {
	var buf bytes.Buffer
	limited := io.LimitReader(r, int64(limit)+1)
	if _, err := io.Copy(&buf, limited); err != nil {
		return nil, err
	}
	if buf.Len() > limit {
		return nil, fmt.Errorf("request body exceeds %d byte proxy capture limit", limit)
	}
	return buf.Bytes(), nil
}

func (p *Proxy) upstreamURL(in *url.URL) (string, error) {
	path := strings.TrimPrefix(in.EscapedPath(), "/v1")
	if path == "" {
		path = "/"
	}
	parsed, err := url.Parse(p.upstreamBase + path)
	if err != nil {
		return "", err
	}
	parsed.RawQuery = in.RawQuery
	return parsed.String(), nil
}

func writeModels(w http.ResponseWriter, models []config.Model) {
	type modelEntry struct {
		ID      string `json:"id"`
		Object  string `json:"object"`
		Created int64  `json:"created"`
		OwnedBy string `json:"owned_by"`
	}
	response := struct {
		Object string       `json:"object"`
		Data   []modelEntry `json:"data"`
	}{
		Object: "list",
	}
	for _, model := range models {
		response.Data = append(response.Data, modelEntry{
			ID:      model.ID,
			Object:  "model",
			OwnedBy: "google",
		})
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(response)
}

func copyRequestHeaders(dst, src http.Header) {
	for key, values := range src {
		if strings.EqualFold(key, "Authorization") || strings.EqualFold(key, "Host") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyResponseHeaders(dst, src http.Header) {
	for key, values := range src {
		if strings.EqualFold(key, "Content-Length") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func parseUsage(headers http.Header, body []byte) (usage.TokenUsage, string, bool) {
	contentType := headers.Get("Content-Type")
	if strings.Contains(contentType, "text/event-stream") {
		return usage.ParseSSE(body)
	}
	return usage.ParseJSON(body)
}

func streamCopy(w http.ResponseWriter, src io.Reader, capture *limitedBuffer) (int64, error) {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		nr, er := src.Read(buf)
		if nr > 0 {
			chunk := buf[:nr]
			if _, err := w.Write(chunk); err != nil {
				return written, err
			}
			_, _ = capture.Write(chunk)
			written += int64(nr)
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
		}
		if er != nil {
			if er == io.EOF {
				return written, nil
			}
			return written, er
		}
	}
}

type captureReadCloser struct {
	source io.ReadCloser
	buffer *limitedBuffer
}

func newCaptureReadCloser(source io.ReadCloser, limit int) *captureReadCloser {
	if source == nil {
		source = io.NopCloser(bytes.NewReader(nil))
	}
	return &captureReadCloser{
		source: source,
		buffer: &limitedBuffer{limit: limit},
	}
}

func (r *captureReadCloser) Read(p []byte) (int, error) {
	n, err := r.source.Read(p)
	if n > 0 {
		_, _ = r.buffer.Write(p[:n])
	}
	return n, err
}

func (r *captureReadCloser) Close() error {
	return r.source.Close()
}

func (r *captureReadCloser) Bytes() []byte {
	return r.buffer.Bytes()
}

type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	originalLen := len(p)
	if b.limit <= 0 || b.buf.Len() >= b.limit {
		return originalLen, nil
	}
	remaining := b.limit - b.buf.Len()
	if len(p) > remaining {
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return originalLen, nil
}

func (b *limitedBuffer) Bytes() []byte {
	return b.buf.Bytes()
}

type StaticTokenProvider string

func (p StaticTokenProvider) Token(ctx context.Context) (string, error) {
	if p == "" {
		return "", fmt.Errorf("empty test token")
	}
	return string(p), nil
}
