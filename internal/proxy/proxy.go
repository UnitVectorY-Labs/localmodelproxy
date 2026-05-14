package proxy

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/UnitVectorY-Labs/localmodelproxy/internal/auth"
	"github.com/UnitVectorY-Labs/localmodelproxy/internal/config"
	"github.com/UnitVectorY-Labs/localmodelproxy/internal/usage"
)

const captureLimit = 8 * 1024 * 1024

// Options configures a Proxy.
// TokenProvider and UpstreamBase are optional global overrides (used in tests).
// When not set, per-backend auth and URLs from Config.Backends are used.
type Options struct {
	Config        *config.Config
	TokenProvider auth.TokenProvider // optional global override
	Metrics       *Metrics
	HTTPClient    *http.Client
	UpstreamBase  string // optional global override
	Verbose       bool
	LogOutput     io.Writer
}

// resolvedBackend is an initialised backend ready to handle requests.
type resolvedBackend struct {
	cfg    config.BackendConfig
	tokens auth.TokenProvider
	base   string // trimmed base URL
}

type Proxy struct {
	cfg       *config.Config
	backends  []*resolvedBackend
	metrics   *Metrics
	client    *http.Client
	verbose   bool
	logOutput io.Writer
}

// New creates a Proxy and initialises an auth provider for each configured backend.
// If Options.TokenProvider and/or Options.UpstreamBase are set they act as global
// overrides for all backends (used in tests).
func New(ctx context.Context, opts Options) (*Proxy, error) {
	client := opts.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}
	logOut := opts.LogOutput
	if logOut == nil {
		logOut = os.Stderr
	}

	backends := make([]*resolvedBackend, 0, len(opts.Config.Backends))
	for _, bc := range opts.Config.Backends {
		// Auth: prefer global override (for tests), otherwise create from config.
		tokens := opts.TokenProvider
		if tokens == nil {
			var err error
			tokens, err = auth.NewProvider(ctx, bc.Auth)
			if err != nil {
				return nil, fmt.Errorf("backend %q: %w", bc.Name, err)
			}
		}

		// URL: prefer global override (for tests), otherwise derive from backend config.
		base := opts.UpstreamBase
		if base == "" {
			base = bc.BaseURL
			if base == "" && bc.Type == "gcp_openai" {
				base = bc.VertexBaseURL()
			}
		}

		backends = append(backends, &resolvedBackend{
			cfg:    bc,
			tokens: tokens,
			base:   strings.TrimRight(base, "/"),
		})
	}

	return &Proxy{
		cfg:       opts.Config,
		backends:  backends,
		metrics:   opts.Metrics,
		client:    client,
		verbose:   opts.Verbose,
		logOutput: logOut,
	}, nil
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

	switch r.URL.Path {
	case "/v1/models":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			record.StatusCode = http.StatusMethodNotAllowed
			record.Error = "method not allowed"
			return
		}
		writeModels(w, p.cfg.Backends)
		record.StatusCode = http.StatusOK

	case "/v1/chat/completions":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			record.StatusCode = http.StatusMethodNotAllowed
			record.Error = "method not allowed"
			return
		}
		var bodyBytes []byte
		if r.Body != nil {
			defer r.Body.Close()
			var err error
			bodyBytes, err = readLimited(r.Body, captureLimit)
			if err != nil {
				record.StatusCode = http.StatusBadRequest
				record.Error = err.Error()
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
		}
		model := usage.ParseModel(bodyBytes)
		if model == "" {
			record.StatusCode = http.StatusBadRequest
			record.Error = "model field is required"
			errResp, _ := json.Marshal(map[string]any{
				"error": map[string]any{
					"message": "model field is required",
					"type":    "invalid_request_error",
					"code":    "invalid_request",
				},
			})
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(errResp)
			return
		}
		backend := p.backendForModel(model)
		if backend == nil {
			record.StatusCode = http.StatusBadRequest
			record.Error = "model not found"
			errResp, err := json.Marshal(map[string]any{
				"error": map[string]any{
					"message": fmt.Sprintf("model %q not found", model),
					"type":    "invalid_request_error",
					"code":    "model_not_found",
				},
			})
			if err != nil {
				http.Error(w, "model not found", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write(errResp)
			return
		}
		if err := p.forward(w, r, record, backend); err != nil {
			if record.StatusCode == 0 {
				record.StatusCode = http.StatusBadGateway
			}
			record.Error = err.Error()
			http.Error(w, err.Error(), record.StatusCode)
		}

	default:
		http.Error(w, "not found", http.StatusNotFound)
		record.StatusCode = http.StatusNotFound
		record.Error = "not found"
	}
}

// backendForModel returns the resolved backend that should handle the given model,
// or nil if none is configured.
func (p *Proxy) backendForModel(model string) *resolvedBackend {
	cfgBackend := p.cfg.BackendForModel(model)
	if cfgBackend == nil {
		return nil
	}
	for _, rb := range p.backends {
		if rb.cfg.Name == cfgBackend.Name {
			return rb
		}
	}
	return nil
}

func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, record *RequestRecord, backend *resolvedBackend) error {
	token, err := backend.tokens.Token(r.Context())
	if err != nil {
		record.StatusCode = http.StatusUnauthorized
		return err
	}

	if p.verbose {
		defer func() {
			fmt.Fprintf(p.logOutput, "request method=%s path=%s model=%q status=%d backend=%s token=%s\n",
				r.Method, r.URL.Path, record.Model, record.StatusCode, backend.cfg.Name, maskToken(token))
		}()
	}

	body, localModel, err := p.prepareRequestBody(r, backend)
	if err != nil {
		record.StatusCode = http.StatusBadRequest
		return err
	}
	record.Model = localModel

	upstreamURL, err := buildUpstreamURL(backend.base, r.URL)
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
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

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
	var copyErr error
	if resp.StatusCode == http.StatusOK && record.Model != "" {
		contentType := resp.Header.Get("Content-Type")
		if strings.Contains(contentType, "text/event-stream") {
			_, copyErr = streamCopySSEWithModelRewrite(w, resp.Body, responseCapture, record.Model)
		} else {
			_, copyErr = streamCopyJSONWithModelRewrite(w, resp.Body, responseCapture, record.Model)
		}
	} else {
		_, copyErr = streamCopy(w, resp.Body, responseCapture)
	}
	if parsedUsage, model, ok := parseUsage(resp.Header, responseCapture.Bytes()); ok {
		record.Usage = parsedUsage
		if model != "" {
			record.Model = p.cfg.LocalModelID(model)
		}
	}
	if copyErr != nil {
		record.Error = copyErr.Error()
		return nil // response headers already written; cannot write an error response
	}
	return nil
}

func (p *Proxy) prepareRequestBody(r *http.Request, backend *resolvedBackend) (io.ReadCloser, string, error) {
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
		rewritten, changed, err := rewriteModel(body, localModel, &backend.cfg)
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
	return r.URL.Path == "/v1/chat/completions"
}

func rewriteModel(body []byte, localModel string, bc *config.BackendConfig) ([]byte, bool, error) {
	upstreamModel := bc.UpstreamModelID(localModel)
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

func maskToken(token string) string {
	if token == "" {
		return ""
	}
	if len(token) > 8 {
		return token[:4] + "..." + token[len(token)-4:]
	}
	return "****"
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

func buildUpstreamURL(base string, in *url.URL) (string, error) {
	path := strings.TrimPrefix(in.EscapedPath(), "/v1")
	if path == "" {
		path = "/"
	}
	parsed, err := url.Parse(base + path)
	if err != nil {
		return "", err
	}
	parsed.RawQuery = in.RawQuery
	return parsed.String(), nil
}

func writeModels(w http.ResponseWriter, backends []config.BackendConfig) {
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
		Data:   []modelEntry{},
	}
	for _, bc := range backends {
		if bc.Models.All {
			continue
		}
		for _, model := range bc.Models.Models {
			response.Data = append(response.Data, modelEntry{
				ID:      model.ID,
				Object:  "model",
				OwnedBy: bc.Name,
			})
		}
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

// rewriteJSONModel replaces the "model" field in a JSON object with localModel.
// Returns the original bytes unchanged if parsing fails or no model field exists.
func rewriteJSONModel(body []byte, localModel string) []byte {
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	if _, exists := payload["model"]; !exists {
		return body
	}
	modelJSON, err := json.Marshal(localModel)
	if err != nil {
		return body
	}
	payload["model"] = json.RawMessage(modelJSON)
	rewritten, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return rewritten
}

// streamCopyJSONWithModelRewrite reads the full JSON body, rewrites the "model"
// field to localModel, and writes the result to w.
func streamCopyJSONWithModelRewrite(w http.ResponseWriter, src io.Reader, capture *limitedBuffer, localModel string) (int64, error) {
	body, err := io.ReadAll(src)
	if err != nil {
		return 0, err
	}
	body = rewriteJSONModel(body, localModel)
	_, _ = capture.Write(body)
	n, writeErr := w.Write(body)
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	return int64(n), writeErr
}

// streamCopySSEWithModelRewrite processes an SSE stream line by line, rewriting
// the "model" field in each data payload to localModel before forwarding.
func streamCopySSEWithModelRewrite(w http.ResponseWriter, src io.Reader, capture *limitedBuffer, localModel string) (int64, error) {
	scanner := bufio.NewScanner(src)
	var written int64
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "data: ") {
			data := line[6:]
			if data != "[DONE]" {
				rewritten := rewriteJSONModel([]byte(data), localModel)
				line = "data: " + string(rewritten)
			}
		}
		lineBytes := append([]byte(line), '\n')
		_, _ = capture.Write(lineBytes)
		n, err := w.Write(lineBytes)
		written += int64(n)
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		if err != nil {
			return written, err
		}
	}
	return written, scanner.Err()
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
