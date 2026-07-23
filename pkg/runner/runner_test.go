package runner

import (
	"testing"

	"github.com/v0id00/propq/pkg/config"
)

func TestFilterServers(t *testing.T) {
	conns := []config.Connection{
		{Name: "prod-eu-1"},
		{Name: "prod-us-1"},
		{Name: "staging"},
		{Name: "dev-1"},
	}

	tests := []struct {
		regex string
		want  int
	}{
		{"", 4},
		{"prod", 2},
		{"staging", 1},
		{"eu", 1},
		{"PROD", 0}, // regexp is case-sensitive; (?i)PROD would match
	}

	for _, tt := range tests {
		got := filterServers(conns, tt.regex, nil)
		if len(got) != tt.want {
			t.Errorf("filterServers(%q) = %d, want %d", tt.regex, len(got), tt.want)
		}
	}
}

func TestFilterDatabases(t *testing.T) {
	targets := []target{
		{server: "s1", database: "shop_dev"},
		{server: "s1", database: "shop_prod"},
		{server: "s2", database: "analytics"},
		{server: "s2", database: "logs_2024"},
	}

	tests := []struct {
		filter   string
		exclude  string
		want     int
	}{
		{"", "", 4},
		{"^shop", "", 2},
		{"", "backup", 4},
		{"shop", "dev", 1},
		{"prod", "", 1},
	}

	for _, tt := range tests {
		got := filterDatabases(targets, tt.filter, tt.exclude)
		if len(got) != tt.want {
			t.Errorf("filterDatabases(%q, %q) = %d, want %d", tt.filter, tt.exclude, len(got), tt.want)
		}
	}
}

func TestHasDestructiveKeywords(t *testing.T) {
	tests := []struct {
		sql  string
		want bool
	}{
		{"SELECT 1", false},
		{"SELECT * FROM users", false},
		{"DROP TABLE users", true},
		{"DROP DATABASE IF EXISTS test", true},
		{"TRUNCATE TABLE logs", true},
		{"DELETE FROM users WHERE id = 1", true},
		{"ALTER TABLE users ADD COLUMN x INT", true},
		{"   delete   from   temp   ", true},
		{"show tables", false},
		{"SELECT * FROM information_schema.tables", false},
	}

	for _, tt := range tests {
		got := HasDestructiveKeywords(tt.sql)
		if got != tt.want {
			t.Errorf("HasDestructiveKeywords(%q) = %v, want %v", tt.sql, got, tt.want)
		}
	}
}
