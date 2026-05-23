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
	DefaultHost             = "127.0.0.1"
	DefaultPort             = 8080
	DefaultLocation         = "global"
	DefaultUIRecentRequests = 10
	MaxUIRecentRequests     = 100
)

type Flags struct {
	ConfigPath string
}

type Config struct {
	Server     ServerConfig    `yaml:"server"`
	Backends   []BackendConfig `yaml:"backends"`
	UI         UIConfig        `yaml:"ui"`
	SourcePath string          `yaml:"-"`
}

type ServerConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

// BackendConfig describes one upstream backend.
type BackendConfig struct {
	Name               string        `yaml:"name"`
	Type               string        `yaml:"type"` // gcp_openai or openai_compatible
	BaseURL            string        `yaml:"base_url"`
	Project            string        `yaml:"project"`
	Location           string        `yaml:"location"`
	InsecureSkipVerify bool          `yaml:"insecure_skip_verify"`
	Auth               AuthConfig    `yaml:"auth"`
	Models             BackendModels `yaml:"models"`
}

// AuthConfig describes how to authenticate against a backend.
type AuthConfig struct {
	Type               string   `yaml:"type"` // none, bearer, google_adc, oauth_client_credentials
	Token              string   `yaml:"token"`
	TokenURL           string   `yaml:"token_url"`
	ClientID           string   `yaml:"client_id"`
	ClientSecret       string   `yaml:"client_secret"`
	Scopes             []string `yaml:"scopes"`
	InsecureSkipVerify bool     `yaml:"insecure_skip_verify"`
}

// BackendModels holds either "all" (pass-through) or an explicit list of models.
type BackendModels struct {
	All    bool
	Models []Model
}

// UnmarshalYAML supports both `models: all` and `models: [{id: ...}]`.
func (bm *BackendModels) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode && value.Value == "all" {
		bm.All = true
		return nil
	}
	var models []Model
	if err := value.Decode(&models); err != nil {
		return err
	}
	bm.Models = models
	return nil
}

type Model struct {
	ID         string    `yaml:"id"`
	UpstreamID string    `yaml:"upstream_id"`
	Cost       ModelCost `yaml:"cost"`
}

type ModelCost struct {
	InputPerMillion  float64 `yaml:"input_per_million"`
	OutputPerMillion float64 `yaml:"output_per_million"`
	CachePerMillion  float64 `yaml:"cache_per_million"`
}

type UIConfig struct {
	RecentRequests int `yaml:"recent_requests"`
}

func Load(flags Flags) (*Config, error) {
	cfg := Config{
		Server: ServerConfig{
			Host: DefaultHost,
			Port: DefaultPort,
		},
		UI: UIConfig{
			RecentRequests: DefaultUIRecentRequests,
		},
	}

	path := resolveConfigPath(flags.ConfigPath)
	if path != "" {
		if err := loadYAML(path, &cfg); err != nil {
			return nil, err
		}
		cfg.SourcePath = path
	}

	// Apply defaults and env-var expansion to each backend.
	for i := range cfg.Backends {
		bc := &cfg.Backends[i]

		// Expand env vars in sensitive string fields.
		bc.BaseURL = os.ExpandEnv(bc.BaseURL)
		bc.Auth.Token = os.ExpandEnv(bc.Auth.Token)
		bc.Auth.TokenURL = os.ExpandEnv(bc.Auth.TokenURL)
		bc.Auth.ClientID = os.ExpandEnv(bc.Auth.ClientID)
		bc.Auth.ClientSecret = os.ExpandEnv(bc.Auth.ClientSecret)

		// For gcp_openai, fill in project/location from env if not set.
		if bc.Type == "gcp_openai" {
			if bc.Project == "" {
				bc.Project = firstEnv("GOOGLE_CLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT")
			}
			if bc.Location == "" {
				bc.Location = firstEnv("GOOGLE_CLOUD_LOCATION", "GOOGLE_CLOUD_REGION", "CLOUDSDK_COMPUTE_REGION")
			}
			if bc.Location == "" {
				bc.Location = DefaultLocation
			}
		}
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
	if c.UI.RecentRequests < 0 || c.UI.RecentRequests > MaxUIRecentRequests {
		return usage(fmt.Sprintf("ui recent_requests must be between 0 and %d", MaxUIRecentRequests))
	}

	seen := make(map[string]bool)
	for _, bc := range c.Backends {
		if strings.TrimSpace(bc.Name) == "" {
			return usage("backends entries must include a non-empty name")
		}
		if seen[bc.Name] {
			return usage(fmt.Sprintf("duplicate backend name %q", bc.Name))
		}
		seen[bc.Name] = true

		switch bc.Type {
		case "gcp_openai", "openai_compatible":
		default:
			return usage(fmt.Sprintf("backend %q: type must be gcp_openai or openai_compatible", bc.Name))
		}

		if bc.Type == "openai_compatible" && bc.BaseURL == "" {
			return usage(fmt.Sprintf("backend %q: openai_compatible backend requires base_url", bc.Name))
		}
		if bc.Type == "gcp_openai" && bc.BaseURL == "" && bc.Project == "" {
			return usage(fmt.Sprintf("backend %q: gcp_openai backend requires base_url or project", bc.Name))
		}

		switch bc.Auth.Type {
		case "none", "bearer", "google_adc", "oauth_client_credentials":
		default:
			return usage(fmt.Sprintf("backend %q: auth type must be none, bearer, google_adc, or oauth_client_credentials", bc.Name))
		}

		for _, m := range bc.Models.Models {
			if strings.TrimSpace(m.ID) == "" {
				return usage(fmt.Sprintf("backend %q: models entries must include a non-empty id", bc.Name))
			}
			if m.Cost.InputPerMillion < 0 || m.Cost.OutputPerMillion < 0 || m.Cost.CachePerMillion < 0 {
				return usage(fmt.Sprintf("backend %q model %q: costs must be non-negative", bc.Name, m.ID))
			}
		}
	}
	return nil
}

func (c *Config) Address() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

// AllModels returns all explicitly-listed models across all backends (excludes "models: all" backends).
func (c *Config) AllModels() []Model {
	var out []Model
	for _, bc := range c.Backends {
		if !bc.Models.All {
			out = append(out, bc.Models.Models...)
		}
	}
	return out
}

// BackendForModel returns the first backend that serves the given model ID.
// Exact-listed models take priority over "models: all" pass-through backends.
func (c *Config) BackendForModel(id string) *BackendConfig {
	var fallback *BackendConfig
	for i := range c.Backends {
		bc := &c.Backends[i]
		if bc.Models.All {
			if fallback == nil {
				fallback = bc
			}
			continue
		}
		for _, m := range bc.Models.Models {
			if m.ID == id {
				return bc
			}
		}
	}
	return fallback
}

// LocalModelID translates an upstream model ID back to the local model ID.
func (c *Config) LocalModelID(upstreamID string) string {
	for _, bc := range c.Backends {
		if bc.Models.All {
			continue
		}
		for _, m := range bc.Models.Models {
			resolved := bc.resolvedUpstreamID(m)
			if resolved == upstreamID {
				return m.ID
			}
		}
	}
	return upstreamID
}

func (c *Config) ModelCost(id string) ModelCost {
	for _, bc := range c.Backends {
		for _, m := range bc.Models.Models {
			if m.ID == id {
				return m.Cost
			}
		}
	}
	return ModelCost{}
}

func (c *Config) HasModelCosts() bool {
	for _, bc := range c.Backends {
		for _, m := range bc.Models.Models {
			if !m.Cost.IsZero() {
				return true
			}
		}
	}
	return false
}

func (c ModelCost) IsZero() bool {
	return c.InputPerMillion == 0 && c.OutputPerMillion == 0 && c.CachePerMillion == 0
}

func (c ModelCost) RequestCost(inputTokens, outputTokens, cachedTokens int64) float64 {
	return float64(inputTokens)*c.InputPerMillion/1_000_000 +
		float64(outputTokens)*c.OutputPerMillion/1_000_000 +
		float64(cachedTokens)*c.CachePerMillion/1_000_000
}

// UpstreamModelID returns the upstream model ID that should be sent to the backend for the given local model ID.
func (bc *BackendConfig) UpstreamModelID(id string) string {
	for _, m := range bc.Models.Models {
		if m.ID == id {
			return bc.resolvedUpstreamID(m)
		}
	}
	// For pass-through backends or unknown models, apply type-based default.
	if bc.Type == "gcp_openai" && !strings.Contains(id, "/") {
		return "google/" + id
	}
	return id
}

func (bc *BackendConfig) resolvedUpstreamID(m Model) string {
	if m.UpstreamID != "" {
		return m.UpstreamID
	}
	if bc.Type == "gcp_openai" && !strings.Contains(m.ID, "/") {
		return "google/" + m.ID
	}
	return m.ID
}

// VertexBaseURL computes the Vertex AI OpenAI-compatible base URL for a gcp_openai backend.
func (bc *BackendConfig) VertexBaseURL() string {
	if bc.Location == "global" {
		return fmt.Sprintf("https://aiplatform.googleapis.com/v1/projects/%s/locations/%s/endpoints/openapi", bc.Project, bc.Location)
	}
	return fmt.Sprintf("https://%s-aiplatform.googleapis.com/v1/projects/%s/locations/%s/endpoints/openapi", bc.Location, bc.Project, bc.Location)
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
