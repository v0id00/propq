package runner

import (
	"strings"
	"testing"
	"time"
)

func TestFmtDuration(t *testing.T) {
	tests := []struct {
		in   time.Duration
		want string
	}{
		{500 * time.Millisecond, "<1s"},
		{5 * time.Second, "5s"},
		{83 * time.Second, "1m23s"},
		{2*time.Minute + 5*time.Second, "2m05s"},
		{2*time.Hour + 5*time.Minute, "2h05m"},
		{3*time.Hour + 45*time.Minute, "3h45m"},
	}
	for _, tt := range tests {
		got := fmtDuration(tt.in)
		if got != tt.want {
			t.Errorf("fmtDuration(%v) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestStatusLineETA(t *testing.T) {
	s := &Spinner{prefix: "Executing", total: 100, current: 50, start: time.Now().Add(-10 * time.Second)}
	line := s.statusLine("⠋")
	if !strings.Contains(line, "[50/100]") {
		t.Errorf("statusLine missing count: %q", line)
	}
	if !strings.Contains(line, "(50%)") {
		t.Errorf("statusLine missing percent: %q", line)
	}
	if !strings.Contains(line, "ETA") {
		t.Errorf("statusLine missing ETA: %q", line)
	}

	// No ETA until progress starts
	s2 := &Spinner{prefix: "Executing", total: 100, current: 0}
	if !strings.Contains(s2.statusLine("⠋"), "ETA --") {
		t.Errorf("statusLine should show ETA -- at start: %q", s2.statusLine("⠋"))
	}

	// No ETA once finished
	s3 := &Spinner{prefix: "Executing", total: 100, current: 100}
	if strings.Contains(s3.statusLine("⠋"), "ETA") {
		t.Errorf("statusLine should not show ETA when done: %q", s3.statusLine("⠋"))
	}
}
