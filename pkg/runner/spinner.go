// Package runner — spinner for terminal progress indication.
package runner

import (
	"fmt"
	"os"
	"sync"
	"time"
)

// spinner frames from unicode-animations (braille set)
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner displays an animated unicode spinner on stderr.
type Spinner struct {
	mu      sync.Mutex
	active  bool
	stopCh  chan struct{}
	done    chan struct{}
	prefix  string
	total   int
	current int
	start   time.Time
}

// NewSpinner creates a spinner with an optional prefix message.
// If enabled is false, it's a no-op.
func NewSpinner(enabled bool, prefix string, total int) *Spinner {
	return &Spinner{
		prefix: prefix,
		total:  total,
		start:  time.Now(),
	}
}

// Start begins the spinner animation in a background goroutine.
func (s *Spinner) Start() {
	s.mu.Lock()
	if s.active {
		s.mu.Unlock()
		return
	}
	s.active = true
	s.stopCh = make(chan struct{})
	s.done = make(chan struct{})
	s.mu.Unlock()

	go s.run()
}

// run is the animation loop.
func (s *Spinner) run() {
	defer close(s.done)
	ticker := time.NewTicker(80 * time.Millisecond)
	defer ticker.Stop()

	i := 0
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			frame := spinnerFrames[i%len(spinnerFrames)]
			line := s.statusLine(frame)
			fmt.Fprintf(os.Stderr, "\r\033[K  %s", line)
			i++
			s.mu.Unlock()
		case <-s.stopCh:
			s.mu.Lock()
			// Clear the spinner line and write a final done line
			fmt.Fprintf(os.Stderr, "\r\033[K  \033[32m✓\033[0m  %s  [%d/%d] (100%%)  done\n", s.prefix, s.total, s.total)
			s.mu.Unlock()
			return
		}
	}
}

// statusLine renders the spinner status: frame, prefix, count, percent, ETA.
// Caller must hold s.mu.
func (s *Spinner) statusLine(frame string) string {
	line := fmt.Sprintf("%s  %s  [%d/%d]", frame, s.prefix, s.current, s.total)
	if s.total > 0 {
		line += fmt.Sprintf(" (%d%%)", s.current*100/s.total)
	}
	if s.current > 0 && s.current < s.total {
		elapsed := time.Since(s.start)
		perTask := elapsed / time.Duration(s.current)
		rem := time.Duration(s.total-s.current) * perTask
		line += fmt.Sprintf("  ETA %s", fmtDuration(rem))
	} else if s.current == 0 {
		line += "  ETA --"
	}
	return line
}

// fmtDuration renders a duration compactly: 45s, 1m23s, 2h05m, <1s.
func fmtDuration(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	secs := int(d.Round(time.Second).Seconds())
	if secs < 60 {
		return fmt.Sprintf("%ds", secs)
	}
	mins := secs / 60
	secs = secs % 60
	if mins < 60 {
		return fmt.Sprintf("%dm%02ds", mins, secs)
	}
	hours := mins / 60
	mins = mins % 60
	return fmt.Sprintf("%dh%02dm", hours, mins)
}

// Inc increments the counter.
func (s *Spinner) Inc() {
	s.mu.Lock()
	s.current++
	s.mu.Unlock()
}

// Stop stops the spinner and writes a completion line.
func (s *Spinner) Stop() {
	s.mu.Lock()
	if !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	close(s.stopCh)
	s.mu.Unlock()
	<-s.done
}
