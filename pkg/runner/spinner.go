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
	mu       sync.Mutex
	active   bool
	stopCh   chan struct{}
	done     chan struct{}
	prefix   string
	total    int
	current  int
}

// NewSpinner creates a spinner with an optional prefix message.
// If enabled is false, it's a no-op.
func NewSpinner(enabled bool, prefix string, total int) *Spinner {
	return &Spinner{
		prefix: prefix,
		total:  total,
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
			fmt.Fprintf(os.Stderr, "\r  %s  %s  [%d/%d]", frame, s.prefix, s.current, s.total)
			i++
			s.mu.Unlock()
		case <-s.stopCh:
			s.mu.Lock()
			// Clear the spinner line
			fmt.Fprintf(os.Stderr, "\r  ✓  %s  [%d/%d]  done\n", s.prefix, s.total, s.total)
			s.mu.Unlock()
			return
		}
	}
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
