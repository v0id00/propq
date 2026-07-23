package scanner

import (
	"strings"
	"testing"
)

func TestScanSQLArg(t *testing.T) {
	sql, err := Scan("SELECT 1", "", false, nil, "", "")
	if err != nil {
		t.Fatalf("Scan failed: %v", err)
	}
	if sql.Content != "SELECT 1" {
		t.Errorf("expected 'SELECT 1', got '%s'", sql.Content)
	}
	if sql.Source != SourceArg {
		t.Errorf("expected SourceArg, got %v", sql.Source)
	}
}

func TestScanNoSource(t *testing.T) {
	_, err := Scan("", "", false, strings.NewReader(""), "", "")
	if err == nil {
		t.Error("expected error for no source")
	}
}

func TestTrimEndComment(t *testing.T) {
	tests := []struct {
		input  string
		marker string
		want   string
	}{
		{"SELECT 1 -- comment", "--", "SELECT 1"},
		{"SELECT 'a -- b' -- comment", "--", "SELECT 'a -- b'"},
		{"INSERT INTO `t--1` VALUES(1) -- x", "--", "INSERT INTO `t--1` VALUES(1)"},
		{"SELECT 1", "--", "SELECT 1"},
		{"SELECT 1 /* block */", "/*", "SELECT 1"}, // trimEndComment uses -- marker, so /* won't match
	}

	for _, tt := range tests {
		got := trimEndComment(tt.input, tt.marker)
		if got != tt.want {
			t.Errorf("trimEndComment(%q, %q) = %q, want %q", tt.input, tt.marker, got, tt.want)
		}
	}
}
