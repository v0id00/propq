// Package display formats and outputs results.
package display

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/v0id00/propq/internal/runner"
)

// ──   Styles   ──────────────────────────────────────────────

var (
	styleOK    = color.New(color.FgGreen, color.Bold).SprintFunc()
	styleErr   = color.New(color.FgRed, color.Bold).SprintFunc()
	styleInfo  = color.New(color.FgCyan).SprintFunc()
	styleDim   = color.New(color.FgHiBlack).SprintFunc()
	styleHead  = color.New(color.FgCyan, color.Bold).SprintFunc()
	styleNull  = color.New(color.FgYellow, color.Italic).SprintFunc()
	styleTitle = color.New(color.FgWhite, color.Bold).SprintFunc()
)

// ──   Banner   ──────────────────────────────────────────────

func PrintBanner(version string) {
	fmt.Fprintf(os.Stderr, "propq %s  ·  MySQL/MariaDB async SQL executor\n", version)
}

func PrintConfigInfo(path string, count int) {
	fmt.Fprintf(os.Stderr, "  %s  %s  ·  %s %d\n",
		styleInfo("cfg"), styleDim(path), styleInfo("srv"), count)
}

func PrintSQLInfo(label string) {
	fmt.Fprintf(os.Stderr, "  %s  %s\n", styleInfo("sql"), label)
}

func PrintServerCount(n int) {
	fmt.Fprintf(os.Stderr, "  %s  %d server(s)\n", styleInfo("→"), n)
}

func PrintDBTarget(count int) {
	fmt.Fprintf(os.Stderr, "  %s  %d database(s)\n", styleInfo("→"), count)
}

func PrintDone(ok, err int) {
	if err > 0 {
		fmt.Fprintf(os.Stderr, "  %s  %d OK  %s %d ERR\n", styleInfo("■"), ok, styleErr("✗"), err)
	} else {
		fmt.Fprintf(os.Stderr, "  %s  %d OK\n", styleOK("✓"), ok)
	}
}

func PrintCancelled() {
	fmt.Fprintf(os.Stderr, "  %s\n", styleErr("✗ Cancelled"))
}

func PrintSaved(path string) {
	fmt.Fprintf(os.Stderr, "  %s  %s\n", styleInfo("📁"), path)
}

func PrintInfo(msg string) {
	fmt.Fprintf(os.Stderr, "  %s\n", msg)
}

func PrintWarning(msg string) {
	fmt.Fprintf(os.Stderr, "  %s  %s\n", styleErr("⚠"), msg)
}

func PrintError(msg string) {
	fmt.Fprintf(os.Stderr, "  %s  %s\n", styleErr("✗"), msg)
}

func PrintDestructiveWarning() {
	fmt.Fprintf(os.Stderr, "\n  %s  Destructive SQL detected! Use --force to execute.\n\n", styleErr("⚠"))
}

func PrintDryRun(labels []string) {
	if len(labels) == 0 {
		PrintInfo("No targets.")
		return
	}
	fmt.Fprintf(os.Stderr, "  %s  %d target(s):\n", styleInfo("◇"), len(labels))
	w := tabwriter.NewWriter(os.Stderr, 0, 0, 2, ' ', 0)
	for _, l := range labels {
		fmt.Fprintf(w, "    %s\n", l)
	}
	w.Flush()
}

func PrintNoDBFilterWarning() {
	fmt.Fprintf(os.Stderr, "  %s  No database filter set — targeting ALL databases\n", styleErr("⚠"))
}

// ──   Prompt   ──────────────────────────────────────────────

func PromptYesNo(format string, args ...any) bool {
	msg := fmt.Sprintf(format, args...)
	fmt.Fprintf(os.Stderr, "%s [y/N] ", msg)
	var resp string
	fmt.Scanln(&resp)
	resp = strings.TrimSpace(strings.ToLower(resp))
	return resp == "y" || resp == "yes"
}

// ──   Table output   ────────────────────────────────────────

func PrintTable(results []runner.Result) {
	os.Stdout.WriteString(RenderTable(results))
}

func RenderTable(results []runner.Result) string {
	if len(results) == 0 {
		return ""
	}

	okCount := 0
	errCount := 0
	for _, r := range results {
		switch r.Status {
		case runner.StatusOK:
			okCount++
		case runner.StatusError:
			errCount++
		}
	}

	var buf bytes.Buffer

	// Per-server result blocks
	for _, r := range results {
		label := fmt.Sprintf("%s.%s", r.Server, r.Database)
		if r.Status == runner.StatusOK {
			if r.Rows != nil && len(r.Rows.Rows) > 0 {
				buf.WriteString(renderRows(label, r.Rows))
			} else if r.Affected > 0 {
				buf.WriteString(fmt.Sprintf("  %s  %s  %s affected=%d\n",
					styleOK("✓"), label, styleDim(timeStr(r.Elapsed)), r.Affected))
			} else {
				buf.WriteString(fmt.Sprintf("  %s  %s  %s\n",
					styleOK("✓"), label, styleDim(timeStr(r.Elapsed))))
			}
		} else if r.Status == runner.StatusError {
			buf.WriteString(fmt.Sprintf("  %s  %s  %s\n",
				styleErr("✗"), label, styleDim(r.Error)))
		}
	}

	// Summary footer
	if okCount > 0 || errCount > 0 {
		buf.WriteString(fmt.Sprintf("\n  %s", styleDim(fmt.Sprintf("%d OK", okCount))))
		if errCount > 0 {
			buf.WriteString(fmt.Sprintf("  %s", styleErr(fmt.Sprintf("%d ERR", errCount))))
		}
		buf.WriteString(fmt.Sprintf("  %s\n", styleDim(fmt.Sprintf("(%d total)", len(results)))))
	}

	return buf.String()
}

func renderRows(label string, rr *runner.RowResult) string {
	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("  %s  %s  %s (%d row(s))\n",
		styleOK("✓"), label, styleDim(timeStr("")), len(rr.Rows)))

	// Column headers
	buf.WriteString("    ")
	for i, col := range rr.Columns {
		if i > 0 {
			buf.WriteString(" │ ")
		}
		buf.WriteString(styleHead(col))
	}
	buf.WriteString("\n")

	// Data rows
	for _, row := range rr.Rows {
		buf.WriteString("    ")
		for i, val := range row {
			if i > 0 {
				buf.WriteString(" │ ")
			}
			if val == "NULL" {
				buf.WriteString(styleNull("NULL"))
			} else {
				buf.WriteString(val)
			}
		}
		buf.WriteString("\n")
	}

	return buf.String()
}

func timeStr(t string) string {
	if t == "" {
		return ""
	}
	return t
}

// ──   Stream output   ───────────────────────────────────────

func PrintStreamResult(r runner.Result) {
	label := fmt.Sprintf("%s.%s", r.Server, r.Database)

	if r.Status == runner.StatusOK {
		if r.Rows != nil && len(r.Rows.Rows) > 0 {
			// Print column headers first time, then rows
			headerLine := ""
			for i, col := range r.Rows.Columns {
				if i > 0 {
					headerLine += " │ "
				}
				headerLine += styleHead(col)
			}
			fmt.Fprintf(os.Stdout, "%s  %s\n", label, styleDim(fmt.Sprintf("(%d row(s))", len(r.Rows.Rows))))
			for _, row := range r.Rows.Rows {
				line := ""
				for i, val := range row {
					if i > 0 {
						line += " │ "
					}
					if val == "NULL" {
						line += styleNull("NULL")
					} else {
						line += val
					}
				}
				fmt.Fprintf(os.Stdout, "  %s\n", line)
			}
		} else if r.Affected > 0 {
			fmt.Fprintf(os.Stdout, "%s  %s  affected=%d\n", styleOK("✓"), label, r.Affected)
		} else {
			fmt.Fprintf(os.Stdout, "%s  %s\n", styleOK("✓"), label)
		}
	} else if r.Status == runner.StatusError {
		fmt.Fprintf(os.Stdout, "%s  %s  %s\n", styleErr("✗"), label, r.Error)
	}
}

// ──   JSON output   ─────────────────────────────────────────

func PrintJSON(results []runner.Result) {
	os.Stdout.WriteString(RenderJSON(results))
}

func RenderJSON(results []runner.Result) string {
	b, _ := json.MarshalIndent(results, "", "  ")
	return string(b) + "\n"
}
