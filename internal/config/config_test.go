package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsAndEnvFallback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("GOOGLE_CLOUD_PROJECT", "env-project")
	t.Setenv("GOOGLE_CLOUD_LOCATION", "")
	t.Setenv("GOOGLE_CLOUD_REGION", "")
	t.Setenv("CLOUDSDK_COMPUTE_REGION", "")
	t.Setenv("LOCALMODELPROXY_CONFIG", "")

	cfg, err := Load(Flags{})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Server.Host != DefaultHost || cfg.Server.Port != DefaultPort {
		t.Fatalf("unexpected server defaults: %#v", cfg.Server)
	}
	if cfg.Vertex.Project != "env-project" {
		t.Fatalf("unexpected project: %s", cfg.Vertex.Project)
	}
	if cfg.Vertex.Location != DefaultLocation {
		t.Fatalf("unexpected location: %s", cfg.Vertex.Location)
	}
}

func TestLoadConfigAndFlagOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
server:
  host: 127.0.0.1
  port: 9000
vertex:
  project: yaml-project
  location: us-central1
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
		Project:    "flag-project",
		UIMode:     "jsonl",
	})
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Server.Port != 9100 || cfg.Vertex.Project != "flag-project" || cfg.UI.Mode != "jsonl" {
		t.Fatalf("flags did not override yaml: %#v", cfg)
	}
	if len(cfg.Models) != 1 || cfg.Models[0].ID != "google/gemini-2.5-flash" {
		t.Fatalf("unexpected models: %#v", cfg.Models)
	}
}

func TestLoadDefaultDotfileConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("LOCALMODELPROXY_CONFIG", "")

	path := filepath.Join(home, ".localmodelproxy")
	content := []byte(`
vertex:
  project: dotfile-project
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
	if cfg.Vertex.Project != "dotfile-project" {
		t.Fatalf("unexpected project: %s", cfg.Vertex.Project)
	}
	if len(cfg.Models) != 1 || cfg.Models[0].ID != "gemini-3.1-flash-lite-preview" {
		t.Fatalf("unexpected models: %#v", cfg.Models)
	}
}

func TestRejectsNonLoopback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	_, err := Load(Flags{Host: "0.0.0.0", Project: "p"})
	if !errors.Is(err, ErrUsage) {
		t.Fatalf("expected usage error, got %v", err)
	}
}

func TestVertexBaseURL(t *testing.T) {
	global := Config{Vertex: VertexConfig{Project: "p", Location: "global"}}
	if got := global.VertexBaseURL(); got != "https://aiplatform.googleapis.com/v1/projects/p/locations/global/endpoints/openapi" {
		t.Fatalf("unexpected global url: %s", got)
	}

	regional := Config{Vertex: VertexConfig{Project: "p", Location: "us-central1"}}
	if got := regional.VertexBaseURL(); got != "https://us-central1-aiplatform.googleapis.com/v1/projects/p/locations/us-central1/endpoints/openapi" {
		t.Fatalf("unexpected regional url: %s", got)
	}
}
