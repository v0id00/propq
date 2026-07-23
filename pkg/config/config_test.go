package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	// Create temp config
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "propq.toml")
	content := `
[defaults]
timeout = 15

[connections.test-db]
host = "127.0.0.1"
port = 3306
user = "testuser"
password = "testpass"
max_connections = 5
tags = ["test", "dev"]
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.Defaults.Timeout != 15 {
		t.Errorf("expected timeout 15, got %d", cfg.Defaults.Timeout)
	}

	conn, ok := cfg.Connections["test-db"]
	if !ok {
		t.Fatal("expected connection 'test-db'")
	}

	if conn.Host != "127.0.0.1" {
		t.Errorf("expected host 127.0.0.1, got %s", conn.Host)
	}
	if conn.Port != 3306 {
		t.Errorf("expected port 3306, got %d", conn.Port)
	}
	if conn.User != "testuser" {
		t.Errorf("expected user testuser, got %s", conn.User)
	}
	if conn.MaxConnections != 5 {
		t.Errorf("expected max_connections 5, got %d", conn.MaxConnections)
	}
	if len(conn.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(conn.Tags))
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "propq.toml")
	content := `
[connections.test]
host = "localhost"
user = "root"
password = ""
`
	if err := os.WriteFile(cfgPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(cfgPath)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Check defaults
	if cfg.Defaults.Timeout != 30 {
		t.Errorf("expected default timeout 30, got %d", cfg.Defaults.Timeout)
	}
	if cfg.Defaults.Concurrency != 5 {
		t.Errorf("expected default concurrency 5, got %d", cfg.Defaults.Concurrency)
	}

	conn := cfg.Connections["test"]
	if conn.Port != 3306 {
		t.Errorf("expected default port 3306, got %d", conn.Port)
	}
	if conn.MaxConnections != 3 {
		t.Errorf("expected default max_connections 3, got %d", conn.MaxConnections)
	}
}

func TestFindConfigPath(t *testing.T) {
	// Explicit path that doesn't exist
	_, err := FindConfigPath("/nonexistent/propq.toml")
	if err == nil {
		t.Error("expected error for nonexistent path")
	}

	// Explicit path that exists
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "custom.toml")
	os.WriteFile(cfgPath, []byte(""), 0644)

	found, err := FindConfigPath(cfgPath)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if found != cfgPath {
		t.Errorf("expected %s, got %s", cfgPath, found)
	}
}

func TestDSN(t *testing.T) {
	conn := Connection{
		Name: "test",
		Host: "127.0.0.1",
		Port: 3306,
		User: "myuser",
		Password: "mypass",
	}

	dsn := conn.DSN("mydb", 30)
	if dsn == "" {
		t.Error("DSN should not be empty")
	}

	// Check it contains expected parts
	if !contains(dsn, "myuser:mypass") {
		t.Errorf("DSN missing credentials: %s", dsn)
	}
	if !contains(dsn, "127.0.0.1:3306") {
		t.Errorf("DSN missing host:port: %s", dsn)
	}
	if !contains(dsn, "/mydb") {
		t.Errorf("DSN missing database: %s", dsn)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
