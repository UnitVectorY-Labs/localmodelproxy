package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

var ErrUsage = errors.New("usage error")

const (
	DefaultHost     = "127.0.0.1"
	DefaultPort     = 8080
	DefaultLocation = "global"
	DefaultUIMode   = "auto"
)

type Flags struct {
	ConfigPath string
	Host       string
	Port       int
	UIMode     string
	Verbose    bool
}

type Config struct {
	Server     ServerConfig `yaml:"server"`
	Vertex     VertexConfig `yaml:"-"`
	Models     []Model      `yaml:"models"`
	UI         UIConfig     `yaml:"ui"`
	Verbose    bool         `yaml:"verbose"`
	SourcePath string       `yaml:"-"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

type VertexConfig struct {
	Project  string
	Location string
}

type Model struct {
	ID         string `yaml:"id"`
	UpstreamID string `yaml:"upstream_id"`
}

type UIConfig struct {
	Mode string `yaml:"mode"`
}

func Load(flags Flags) (*Config, error) {
	cfg := Config{
		Server: ServerConfig{
			Host: DefaultHost,
			Port: DefaultPort,
		},
		Vertex: VertexConfig{
			Location: DefaultLocation,
		},
		UI: UIConfig{
			Mode: DefaultUIMode,
		},
	}

	path := resolveConfigPath(flags.ConfigPath)
	if path != "" {
		if err := loadYAML(path, &cfg); err != nil {
			return nil, err
		}
		cfg.SourcePath = path
	}

	if flags.Host != "" {
		cfg.Server.Host = flags.Host
	}
	if flags.Port != 0 {
		cfg.Server.Port = flags.Port
	}
	if flags.UIMode != "" {
		cfg.UI.Mode = flags.UIMode
	}
	if flags.Verbose {
		cfg.Verbose = true
	}

	if cfg.Vertex.Project == "" {
		cfg.Vertex.Project = firstEnv("GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT")
	}
	if cfg.Vertex.Location == "" {
		cfg.Vertex.Location = firstEnv("GOOGLE_CLOUD_LOCATION", "GOOGLE_CLOUD_REGION", "CLOUDSDK_COMPUTE_REGION")
	}
	if cfg.Vertex.Location == "" {
		cfg.Vertex.Location = DefaultLocation
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if c.Server.Host == "" {
		return usage("server host cannot be empty")
	}
	if c.Server.Port < 1 || c.Server.Port > 65535 {
		return usage("server port must be between 1 and 65535")
	}
	if !IsLoopbackHost(c.Server.Host) {
		return usage(fmt.Sprintf("refusing to bind to non-loopback host %q", c.Server.Host))
	}
	switch c.UI.Mode {
	case "", "auto", "tui", "plain", "jsonl":
	default:
		return usage("ui mode must be one of auto, tui, plain, jsonl")
	}
	if c.UI.Mode == "" {
		c.UI.Mode = DefaultUIMode
	}
	for _, model := range c.Models {
		if strings.TrimSpace(model.ID) == "" {
			return usage("models entries must include a non-empty id")
		}
	}
	return nil
}

func (c *Config) Address() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

func (c *Config) UpstreamModelID(id string) string {
	for _, model := range c.Models {
		if model.ID == id {
			if model.UpstreamID != "" {
				return model.UpstreamID
			}
			if strings.Contains(id, "/") {
				return id
			}
			return "google/" + id
		}
	}
	return id
}

func (c *Config) LocalModelID(upstreamID string) string {
	for _, model := range c.Models {
		resolved := model.UpstreamID
		if resolved == "" {
			if strings.Contains(model.ID, "/") {
				resolved = model.ID
			} else {
				resolved = "google/" + model.ID
			}
		}
		if resolved == upstreamID {
			return model.ID
		}
	}
	return upstreamID
}

func (c *Config) VertexBaseURL() string {
	if c.Vertex.Location == "global" {
		return fmt.Sprintf("https://aiplatform.googleapis.com/v1/projects/%s/locations/%s/endpoints/openapi", c.Vertex.Project, c.Vertex.Location)
	}
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/endpoints/openapi", c.Vertex.Location, c.Vertex.Project, c.Vertex.Location)
}

func IsLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func resolveConfigPath(flagPath string) string {
	if flagPath != "" {
		return flagPath
	}
	if envPath := os.Getenv("LOCALMODELPROXY_CONFIG"); envPath != "" {
		return envPath
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	path := filepath.Join(home, ".localmodelproxy")
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		return path
	}
	return ""
}

func loadYAML(path string, cfg *Config) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("failed to read config %s: %w", path, err)
	}
	if err := yaml.Unmarshal(content, cfg); err != nil {
		return fmt.Errorf("failed to parse config %s: %w", path, err)
	}
	return nil
}

func firstEnv(names ...string) string {
	for _, name := range names {
		if value := os.Getenv(name); value != "" {
			return value
		}
	}
	return ""
}

func usage(message string) error {
	return fmt.Errorf("%w: %s", ErrUsage, message)
}
