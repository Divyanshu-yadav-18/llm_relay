package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	t.Run("Valid config file", func(t *testing.T) {
		t.Helper()
		content := `
server:
  port: 9090
providers:
  openai:
    base_url: https://api.openai.com/v1
    api_key: test_key
routing:
  default_provider: openai
`
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "config.yaml")
		err := os.WriteFile(configFile, []byte(content), 0644)
		if err != nil {
			t.Fatalf("failed to write temp config: %v", err)
		}

		cfg, err := Load(configFile)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if cfg.Server.Port != 9090 {
			t.Errorf("expected port 9090, got %d", cfg.Server.Port)
		}
		if cfg.Providers["openai"].APIKey != "test_key" {
			t.Errorf("expected api key test_key, got %s", cfg.Providers["openai"].APIKey)
		}
	})

	t.Run("Non-existent file", func(t *testing.T) {
		t.Helper()
		_, err := Load("non-existent.yaml")
		if err == nil {
			t.Fatalf("expected error for non-existent file, got nil")
		}
	})

	t.Run("Invalid YAML", func(t *testing.T) {
		t.Helper()
		content := `
server:
  port: 9090
  invalid_yaml: [unclosed list
`
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "config.yaml")
		_ = os.WriteFile(configFile, []byte(content), 0644)

		_, err := Load(configFile)
		if err == nil {
			t.Fatalf("expected error for invalid YAML, got nil")
		}
	})

	t.Run("Default values applied", func(t *testing.T) {
		t.Helper()
		content := `
providers:
  openai:
    base_url: https://api.openai.com/v1
`
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "config.yaml")
		_ = os.WriteFile(configFile, []byte(content), 0644)

		cfg, err := Load(configFile)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if cfg.Server.Port != 8080 {
			t.Errorf("expected default port 8080, got %d", cfg.Server.Port)
		}
		if cfg.RateLimit.DefaultTokensPerMinute != 100000 {
			t.Errorf("expected default rate limit 100000, got %d", cfg.RateLimit.DefaultTokensPerMinute)
		}
	})

	t.Run("Environment variable expansion", func(t *testing.T) {
		t.Helper()
		os.Setenv("TEST_API_KEY", "env_secret")
		defer os.Unsetenv("TEST_API_KEY")

		content := `
providers:
  openai:
    base_url: https://api.openai.com/v1
    api_key: ${TEST_API_KEY}
`
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "config.yaml")
		_ = os.WriteFile(configFile, []byte(content), 0644)

		cfg, err := Load(configFile)
		if err != nil {
			t.Fatalf("expected no error, got %v", err)
		}
		if cfg.Providers["openai"].APIKey != "env_secret" {
			t.Errorf("expected env_secret, got %s", cfg.Providers["openai"].APIKey)
		}
	})

	t.Run("Validation catches missing providers", func(t *testing.T) {
		t.Helper()
		content := `
server:
  port: 9090
`
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "config.yaml")
		_ = os.WriteFile(configFile, []byte(content), 0644)

		_, err := Load(configFile)
		if err == nil {
			t.Fatalf("expected error for missing providers, got nil")
		}
	})

	t.Run("Validation catches missing base_url", func(t *testing.T) {
		t.Helper()
		content := `
providers:
  openai:
    api_key: test_key
`
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "config.yaml")
		_ = os.WriteFile(configFile, []byte(content), 0644)

		_, err := Load(configFile)
		if err == nil {
			t.Fatalf("expected error for missing base_url, got nil")
		}
	})

	t.Run("Validation catches default_provider not in providers list", func(t *testing.T) {
		t.Helper()
		content := `
providers:
  openai:
    base_url: https://api.openai.com/v1
routing:
  default_provider: unknown
`
		tmpDir := t.TempDir()
		configFile := filepath.Join(tmpDir, "config.yaml")
		_ = os.WriteFile(configFile, []byte(content), 0644)

		_, err := Load(configFile)
		if err == nil {
			t.Fatalf("expected error for unknown default_provider, got nil")
		}
	})
}
