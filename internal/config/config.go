// Package config provides configuration loading, validation, and defaults
// management for the LLM Relay gateway. It parses YAML configuration files
// and handles environment variable substitution.
package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config represents the top-level configuration for the LLM Relay gateway.
type Config struct {
	Server     ServerConfig               `yaml:"server"`
	RedisURL   string                     `yaml:"redis_url"`
	Providers  map[string]ProviderConfig `yaml:"providers"`
	Routing    RoutingConfig              `yaml:"routing"`
	RateLimit  RateLimitConfig            `yaml:"rate_limit"`
	Cache      CacheConfig                `yaml:"cache"`
	Resilience ResilienceConfig           `yaml:"resilience"`
	Usage      UsageConfig                `yaml:"usage"`
}

type ServerConfig struct {
	Port                int    `yaml:"port"`
	ReadTimeoutSeconds  int    `yaml:"read_timeout_seconds"`
	WriteTimeoutSeconds int    `yaml:"write_timeout_seconds"`
	LogLevel            string `yaml:"log_level"`
}

type ProviderConfig struct {
	BaseURL string   `yaml:"base_url"`
	APIKey  string   `yaml:"api_key"`
	Models  []string `yaml:"models"`
}

type RoutingConfig struct {
	DefaultProvider string            `yaml:"default_provider"`
	ModelMap        map[string]string `yaml:"model_map"`
	FallbackChain   []string          `yaml:"fallback_chain"`
}

type RateLimitConfig struct {
	Enabled                bool `yaml:"enabled"`
	DefaultTokensPerMinute int  `yaml:"default_tokens_per_minute"`
	DefaultBurst           int  `yaml:"default_burst"`
}

type CacheConfig struct {
	Enabled             bool    `yaml:"enabled"`
	SimilarityThreshold float64 `yaml:"similarity_threshold"`
	MaxEntries          int     `yaml:"max_entries"`
	EmbeddingProvider   string  `yaml:"embedding_provider"`
	EmbeddingModel      string  `yaml:"embedding_model"`
	TTLSeconds          int     `yaml:"ttl_seconds"`
}

type CircuitBreakerConfig struct {
	FailureThreshold    int `yaml:"failure_threshold"`
	ResetTimeoutSeconds int `yaml:"reset_timeout_seconds"`
}

type ResilienceConfig struct {
	MaxRetries        int                  `yaml:"max_retries"`
	InitialBackoffMs  int                  `yaml:"initial_backoff_ms"`
	MaxBackoffMs      int                  `yaml:"max_backoff_ms"`
	BackoffMultiplier float64              `yaml:"backoff_multiplier"`
	CircuitBreaker    CircuitBreakerConfig `yaml:"circuit_breaker"`
}

type UsageConfig struct {
	AsyncBufferSize      int `yaml:"async_buffer_size"`
	FlushIntervalSeconds int `yaml:"flush_interval_seconds"`
}

// Load reads the YAML configuration file from the given path, unmarshals it,
// expands environment variables, and validates the configuration.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config file %s does not exist", path)
		}
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	cfg.expandEnvAndDefaults()

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &cfg, nil
}

// expandEnvAndDefaults processes the configuration to apply sensible default values 
// for missing fields (e.g. default ports and timeouts) so the server can run out-of-the-box.
// It also expands environment variables (like ${API_KEY}) in Provider configurations
// to allow securely injecting secrets without hardcoding them in the YAML file.
func (c *Config) expandEnvAndDefaults() {
	// Set server defaults
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.ReadTimeoutSeconds == 0 {
		c.Server.ReadTimeoutSeconds = 30
	}
	if c.Server.WriteTimeoutSeconds == 0 {
		c.Server.WriteTimeoutSeconds = 120
	}
	if c.Server.LogLevel == "" {
		c.Server.LogLevel = "info"
	}

	// Expand env vars in providers
	for name, provider := range c.Providers {
		// Replace ${ENV_VAR} or $ENV_VAR with actual environment variable values
		provider.APIKey = os.ExpandEnv(provider.APIKey)
		c.Providers[name] = provider
	}

	// Expand env vars in Redis URL (e.g., ${REDIS_URL})
	c.RedisURL = os.ExpandEnv(c.RedisURL)

	// Rate limit defaults
	if c.RateLimit.DefaultTokensPerMinute == 0 {
		c.RateLimit.DefaultTokensPerMinute = 100000
	}
	if c.RateLimit.DefaultBurst == 0 {
		c.RateLimit.DefaultBurst = 10000
	}

	// Usage defaults
	if c.Usage.AsyncBufferSize == 0 {
		c.Usage.AsyncBufferSize = 1000
	}
	if c.Usage.FlushIntervalSeconds == 0 {
		c.Usage.FlushIntervalSeconds = 5
	}
}

// validate ensures all required fields are set correctly to prevent runtime errors.
// It checks that at least one provider is configured, that every provider has a base URL,
// and that the default provider specified in routing exists in the provider list.
func (c *Config) validate() error {
	var errs []string

	if len(c.Providers) == 0 {
		errs = append(errs, "at least one provider must be configured")
	}

	for name, provider := range c.Providers {
		if provider.BaseURL == "" {
			errs = append(errs, fmt.Sprintf("provider %s is missing base_url", name))
		}
	}

	if c.Routing.DefaultProvider != "" {
		if _, exists := c.Providers[c.Routing.DefaultProvider]; !exists {
			errs = append(errs, fmt.Sprintf("default provider %s is not configured in providers", c.Routing.DefaultProvider))
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}

	return nil
}
