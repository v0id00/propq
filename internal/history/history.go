// Package history manages query history storage.
package history

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	historyFile = ".propq_history"
	maxEntries  = 50
)

// Path returns the path to the history file.
func Path() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return historyFile
	}
	return filepath.Join(home, historyFile)
}

// Save appends a query to the history file, deduplicating the last entry.
func Save(query string) {
	if strings.TrimSpace(query) == "" {
		return
	}

	p := Path()
	entries := loadEntries(p)

	// Don't duplicate the last entry
	if len(entries) > 0 && entries[len(entries)-1].query == query {
		return
	}

	entries = append(entries, entry{
		time:  time.Now(),
		query: query,
	})

	// Keep only last N
	if len(entries) > maxEntries {
		entries = entries[len(entries)-maxEntries:]
	}

	writeEntries(p, entries)
}

// Recent returns the most recent queries as comment lines (for editor template).
func Recent(n int) string {
	entries := loadEntries(Path())
	if len(entries) == 0 {
		return ""
	}

	if n > len(entries) {
		n = len(entries)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("-- Recent queries (last %d):\n", n))
	for i := len(entries) - n; i < len(entries); i++ {
		e := entries[i]
		short := e.query
		if len(short) > 70 {
			short = short[:67] + "..."
		}
		b.WriteString(fmt.Sprintf("--   %s  %s\n", e.time.Format("15:04"), strings.ReplaceAll(short, "\n", "\\n")))
	}
	b.WriteString("--\n")
	return b.String()
}

// List returns recent queries formatted for display.
func List(max int) string {
	entries := loadEntries(Path())
	if len(entries) == 0 {
		return "No query history yet."
	}

	if max <= 0 || max > len(entries) {
		max = len(entries)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Recent queries (last %d):\n\n", max))
	for i := len(entries) - max; i < len(entries); i++ {
		e := entries[i]
		short := e.query
		if len(short) > 80 {
			short = short[:77] + "..."
		}
		b.WriteString(fmt.Sprintf("  %2d.  %s\n      %s\n\n", i+1, e.time.Format("2006-01-02 15:04:05"), short))
	}
	return b.String()
}

type entry struct {
	time  time.Time
	query string
}

func loadEntries(path string) []entry {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	lines := strings.Split(string(data), "\n")
	var entries []entry
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: timestamp|query
		if parts := strings.SplitN(line, "|", 2); len(parts) == 2 {
			t, err := time.Parse(time.RFC3339, parts[0])
			if err != nil {
				continue
			}
			entries = append(entries, entry{time: t, query: parts[1]})
		}
	}
	return entries
}

func writeEntries(path string, entries []entry) {
	var b strings.Builder
	for _, e := range entries {
		b.WriteString(fmt.Sprintf("%s|%s\n", e.time.Format(time.RFC3339), e.query))
	}
	os.WriteFile(path, []byte(b.String()), 0600)
}
