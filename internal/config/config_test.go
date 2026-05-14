package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsNoConfig(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("LOCALMODELPROXY_CONFIG", "")

	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Server.Host != DefaultHost || cfg.Server.Port != DefaultPort {
		t.Fatalf("unexpected server defaults: %#v", cfg.Server)
	}
	if len(cfg.Backends) != 0 {
		t.Fatalf("expected no backends, got: %#v", cfg.Backends)
	}
}

func TestLoadConfigAndFlagOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
server:
  host: 127.0.0.1
  port: 9000
backends:
  - name: local
    type: openai_compatible
    base_url: http://127.0.0.1:11434/v1
    auth:
      type: none
    models:
      - id: google/gemini-2.5-flash
ui:
  mode: plain
`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Flags{
		ConfigPath: path,
		Port:       9100,
		UIMode:     "jsonl",
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Server.Port != 9100 || cfg.UI.Mode != "jsonl" {
		t.Fatalf("flags did not override yaml: %#v", cfg)
	}
	all := cfg.AllModels()
	if len(all) != 1 || all[0].ID != "google/gemini-2.5-flash" {
		t.Fatalf("unexpected models: %#v", all)
	}
}

func TestLoadBackendsGCPOpenAI(t *testing.T) {
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "")
	t.Setenv("GOOGLE_CLOUD_REGION", "")
	t.Setenv("CLOUDSDK_COMPUTE_REGION", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
backends:
  - name: vertex
    type: gcp_openai
    project: my-project
    auth:
      type: google_adc
    models:
      - id: gemini-3.1-flash-lite-preview
`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if len(cfg.Backends) != 1 {
		t.Fatalf("expected 1 backend, got %d", len(cfg.Backends))
	}
	bc := cfg.Backends[0]
	if bc.Project != "my-project" {
		t.Fatalf("unexpected project: %s", bc.Project)
	}
	if bc.Location != DefaultLocation {
		t.Fatalf("unexpected location: %s", bc.Location)
	}
	all := cfg.AllModels()
	if len(all) != 1 || all[0].ID != "gemini-3.1-flash-lite-preview" {
		t.Fatalf("unexpected models: %#v", all)
	}
}

func TestLoadDefaultDotfileConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOCALMODELPROXY_CONFIG", "")

	path := filepath.Join(home, ".localmodelproxy")
	content := []byte(`
backends:
  - name: local
    type: openai_compatible
    base_url: http://127.0.0.1:11434/v1
    auth:
      type: none
    models:
      - id: gemini-3.1-flash-lite-preview
`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	all := cfg.AllModels()
	if len(all) != 1 || all[0].ID != "gemini-3.1-flash-lite-preview" {
		t.Fatalf("unexpected models: %#v", all)
	}
}

func TestRejectsNonLoopback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := Load(Flags{Host: "0.0.0.0"})
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("expected usage error, got %v", err)
	}
}

func TestLoadsWithoutProject(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("CLOUDSDK_CORE_PROJECT", "")
	t.Setenv("LOCALMODELPROXY_CONFIG", "")

	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatalf("expected no error when project is not set, got: %v", err)
	}
	if len(cfg.Backends) != 0 {
		t.Fatalf("expected no backends, got: %#v", cfg.Backends)
	}
}

func TestVertexBaseURL(t *testing.T) {
	global := BackendConfig{Type: "gcp_openai", Project: "p", Location: "global"}
	if got := global.VertexBaseURL(); got != "https://aiplatform.googleapis.com/v1/projects/p/locations/global/endpoints/openapi" {
		t.Fatalf("unexpected global url: %s", got)
	}

	regional := BackendConfig{Type: "gcp_openai", Project: "p", Location: "us-central1"}
	if got := regional.VertexBaseURL(); got != "https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/endpoints/openapi" {
		t.Fatalf("unexpected regional url: %s", got)
	}
}

func TestBackendForModelExactMatch(t *testing.T) {
	cfg := &Config{
		Backends: []BackendConfig{
			{
				Name:   "a",
				Type:   "openai_compatible",
				Auth:   AuthConfig{Type: "none"},
				Models: BackendModels{Models: []Model{{ID: "model-a"}}},
			},
			{
				Name:   "b",
				Type:   "openai_compatible",
				Auth:   AuthConfig{Type: "none"},
				Models: BackendModels{All: true},
			},
		},
	}
	if got := cfg.BackendForModel("model-a"); got == nil || got.Name != "a" {
		t.Fatalf("expected backend a, got %v", got)
	}
	if got := cfg.BackendForModel("anything-else"); got == nil || got.Name != "b" {
		t.Fatalf("expected fallback backend b, got %v", got)
	}
	if got := cfg.BackendForModel("unknown"); got == nil || got.Name != "b" {
		t.Fatalf("expected fallback backend b for unknown model, got %v", got)
	}
}

func TestModelsAll(t *testing.T) {
	cfg := &Config{
		Backends: []BackendConfig{
			{
				Name:   "pass",
				Type:   "openai_compatible",
				Auth:   AuthConfig{Type: "none"},
				Models: BackendModels{All: true},
			},
		},
	}
	// "models: all" backends are excluded from AllModels().
	if all := cfg.AllModels(); len(all) != 0 {
		t.Fatalf("expected no explicit models, got: %#v", all)
	}
	// But BackendForModel still routes to it.
	if got := cfg.BackendForModel("anything"); got == nil || got.Name != "pass" {
		t.Fatalf("expected pass backend, got %v", got)
	}
}

func TestUpstreamModelIDGCPOpenAI(t *testing.T) {
	bc := &BackendConfig{
		Type: "gcp_openai",
		Models: BackendModels{
			Models: []Model{
				{ID: "gemini-flash"},
				{ID: "google/gemini-pro"},
				{ID: "custom", UpstreamID: "vendor/custom-v2"},
			},
		},
	}
	if got := bc.UpstreamModelID("gemini-flash"); got != "google/gemini-flash" {
		t.Fatalf("expected google/ prefix, got %s", got)
	}
	if got := bc.UpstreamModelID("google/gemini-pro"); got != "google/gemini-pro" {
		t.Fatalf("expected pass-through, got %s", got)
	}
	if got := bc.UpstreamModelID("custom"); got != "vendor/custom-v2" {
		t.Fatalf("expected upstream_id, got %s", got)
	}
}

func TestUpstreamModelIDOpenAICompatible(t *testing.T) {
	bc := &BackendConfig{
		Type: "openai_compatible",
		Models: BackendModels{
			Models: []Model{{ID: "qwen-coder"}},
		},
	}
	if got := bc.UpstreamModelID("qwen-coder"); got != "qwen-coder" {
		t.Fatalf("expected pass-through, got %s", got)
	}
}

func TestValidateMissingBackendName(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Host: DefaultHost, Port: DefaultPort},
		Backends: []BackendConfig{
			{Name: "", Type: "openai_compatible", BaseURL: "http://x", Auth: AuthConfig{Type: "none"}},
		},
		UI: UIConfig{Mode: DefaultUIMode},
	}
	if err := cfg.Validate(); !errors.Is(err, ErrUsage) {
		t.Fatalf("expected usage error, got %v", err)
	}
}

func TestValidateDuplicateBackendName(t *testing.T) {
	cfg := Config{
		Server: ServerConfig{Host: DefaultHost, Port: DefaultPort},
		Backends: []BackendConfig{
			{Name: "dup", Type: "openai_compatible", BaseURL: "http://x", Auth: AuthConfig{Type: "none"}},
			{Name: "dup", Type: "openai_compatible", BaseURL: "http://y", Auth: AuthConfig{Type: "none"}},
		},
		UI: UIConfig{Mode: DefaultUIMode},
	}
	if err := cfg.Validate(); !errors.Is(err, ErrUsage) {
		t.Fatalf("expected usage error, got %v", err)
	}
}

func TestEnvVarExpansion(t *testing.T) {
	t.Setenv("MY_TOKEN", "secret-value")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
backends:
  - name: bearer-test
    type: openai_compatible
    base_url: http://127.0.0.1:1234/v1
    auth:
      type: bearer
      token: ${MY_TOKEN}
    models:
      - id: test-model
`)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Flags{ConfigPath: path})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Backends[0].Auth.Token != "secret-value" {
		t.Fatalf("env var not expanded: %s", cfg.Backends[0].Auth.Token)
	}
}
