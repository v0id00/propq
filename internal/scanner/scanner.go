// Package scanner reads SQL input from various sources.
package scanner

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// Source describes where the SQL came from.
type Source int

const (
	SourceArg   Source = iota // --sql flag
	SourceFile                // -f / --file
	SourcePipe                // stdin pipe
	SourceEditor              // -e / --edit
)

// SQL holds the SQL content and its origin.
type SQL struct {
	Content string
	Source  Source
	Label   string // for display (filename, "stdin", "editor", "arg")
}

// Scan determines the SQL source and reads the content.
// Priority: --sql > -f > -e > stdin (if piped)
func Scan(sqlStr, filePath string, editMode bool, stdin io.Reader) (*SQL, error) {
	// 1. --sql flag
	if sqlStr != "" {
		return &SQL{Content: sqlStr, Source: SourceArg, Label: "arg"}, nil
	}

	// 2. -f / --file
	if filePath != "" {
		if filePath == "-" {
			// Read from stdin explicitly
			return readStdin(stdin)
		}
		data, err := os.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("read SQL file: %w", err)
		}
		content := strings.TrimSpace(string(data))
		if content == "" {
			return nil, fmt.Errorf("SQL file is empty: %s", filePath)
		}
		return &SQL{Content: content, Source: SourceFile, Label: filePath}, nil
	}

	// 3. -e / --edit
	if editMode {
		content, err := FromEditor()
		if err != nil {
			return nil, fmt.Errorf("editor: %w", err)
		}
		if strings.TrimSpace(content) == "" {
			return nil, fmt.Errorf("SQL is empty after editing")
		}
		return &SQL{Content: content, Source: SourceEditor, Label: "editor"}, nil
	}

	// 4. stdin pipe (non-TTY)
	if IsPipedInput() {
		return readStdin(stdin)
	}

	return nil, fmt.Errorf("no SQL source provided\n\n" +
		"  Provide SQL via one of:\n" +
		"    --sql QUERY    Inline SQL query\n" +
		"    -f, --file FILE  Read from file (or '-' for stdin)\n" +
		"    -e, --edit       Open $EDITOR to write SQL\n" +
		"    stdin pipe       echo 'SELECT 1' | propq\n")
}

// IsPipedInput returns true if stdin is a pipe or redirected file (non-TTY).
func IsPipedInput() bool {
	stat, _ := os.Stdin.Stat()
	return (stat.Mode() & os.ModeCharDevice) == 0
}

// readStdin reads all content from stdin.
func readStdin(r io.Reader) (*SQL, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return nil, fmt.Errorf("stdin is empty")
	}
	return &SQL{Content: content, Source: SourcePipe, Label: "stdin"}, nil
}

// FromEditor opens $EDITOR (or a fallback) to let the user write/edit content.
// Returns the edited content as a string.
func FromEditor() (string, error) {
	editor := findEditor()
	if editor == "" {
		return "", fmt.Errorf("no editor found. Set $EDITOR or $VISUAL")
	}

	tmpFile, err := os.CreateTemp("", "propq-*.sql")
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	// Write header comment
	header := "-- Write your SQL here (semicolons for multiple statements)\n" +
		"-- Save and quit (:wq) to execute\n" +
		"-- To cancel: :q!\n\n"
	if _, err := tmpFile.WriteString(header); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return "", fmt.Errorf("write template: %w", err)
	}
	tmpFile.Close()

	fmt.Fprintf(os.Stderr, "  Opening %s ... write SQL, then :wq\n", editor)

	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		os.Remove(tmpPath)
		return "", fmt.Errorf("editor exited with error: %w", err)
	}

	data, err := os.ReadFile(tmpPath)
	os.Remove(tmpPath)
	if err != nil {
		return "", fmt.Errorf("read edited file: %w", err)
	}

	// Strip comment lines starting with --
	lines := strings.Split(string(data), "\n")
	var cleanLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "--") {
			continue
		}
		cleanLines = append(cleanLines, trimEndComment(line, "--"))
	}

	content := strings.TrimSpace(strings.Join(cleanLines, "\n"))
	return content, nil
}

// SelectFromEditor opens $EDITOR with a list of items. User deletes lines they
// don't want, saves, and the remaining lines are returned.
func SelectFromEditor(items []string, headerComment string) ([]string, error) {
	editor := findEditor()
	if editor == "" {
		return nil, fmt.Errorf("no editor found. Set $EDITOR or $VISUAL")
	}

	tmpFile, err := os.CreateTemp("", "propq-select-*.txt")
	if err != nil {
		return nil, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()

	var builder strings.Builder
	if headerComment != "" {
		builder.WriteString(headerComment)
		builder.WriteString("\n")
	}
	for _, item := range items {
		builder.WriteString(item)
		builder.WriteString("\n")
	}

	if _, err := tmpFile.WriteString(builder.String()); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return nil, fmt.Errorf("write items: %w", err)
	}
	tmpFile.Close()

	fmt.Fprintf(os.Stderr, "  Opening %s ... delete lines you don't want, then :wq\n", editor)

	cmd := exec.Command(editor, tmpPath)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		os.Remove(tmpPath)
		return nil, fmt.Errorf("editor exited with error: %w", err)
	}

	data, err := os.ReadFile(tmpPath)
	os.Remove(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("read edited file: %w", err)
	}

	var selected []string
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "--") {
			continue
		}
		selected = append(selected, line)
	}

	return selected, nil
}

// findEditor locates the user's preferred editor.
func findEditor() string {
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	for _, candidate := range []string{"vim", "nano", "hx", "micro"} {
		if _, err := exec.LookPath(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

// trimEndComment removes inline SQL comments from a line.
// Only removes the first occurrence of the comment marker outside strings.
func trimEndComment(line string, marker string) string {
	if marker == "" {
		return line
	}
	inSingle := false
	inDouble := false
	inBacktick := false
	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch {
		case ch == '\'' && !inDouble && !inBacktick:
			inSingle = !inSingle
		case ch == '"' && !inSingle && !inBacktick:
			inDouble = !inDouble
		case ch == '`' && !inSingle && !inDouble:
			inBacktick = !inBacktick
		case !inSingle && !inDouble && !inBacktick:
			if i+len(marker) <= len(line) && line[i:i+len(marker)] == marker {
				return strings.TrimRight(line[:i], " \t")
			}
		}
	}
	return line
}
