// Package display formats and outputs results.
package display

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/v0id00/propq/internal/runner"
)

// PrintTable renders results as a human-readable table.
func PrintTable(results []runner.Result) {
	if len(results) == 0 {
		color.Yellow("No results.")
		return
	}

	// Count stats
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

	// Summary line
	summaryColor := color.FgGreen
	if errCount > 0 && okCount > 0 {
		summaryColor = color.FgYellow
	} else if errCount > 0 {
		summaryColor = color.FgRed
	}
	color.New(summaryColor, color.Bold).Fprintf(os.Stdout, "\nResults: %d OK  %d ERR  %d total\n\n", okCount, errCount, len(results))

	// Table using tabwriter
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)

	// Header
	color.New(color.FgCyan, color.Bold).Fprintf(w, "Server\tDatabase\tStatus\tAffected\tElapsed\tError\n")
	fmt.Fprintf(w, "------\t--------\t------\t--------\t-------\t-----\n")

	for _, r := range results {
		statusStr := string(r.Status)
		errStr := r.Error
		if errStr != "" {
			if len(errStr) > 80 {
				errStr = errStr[:77] + "..."
			}
		}

		// Color based on status
		statusColor := color.FgGreen
		if r.Status == runner.StatusError {
			statusColor = color.FgRed
		} else if r.Status == runner.StatusSkip {
			statusColor = color.FgYellow
		}

		statusColored := color.New(statusColor, color.Bold).Sprint(statusStr)
		errColored := ""
		if errStr != "" {
			errColored = color.RedString(errStr)
		}

		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\t%s\n",
			r.Server,
			r.Database,
			statusColored,
			r.Affected,
			r.Elapsed,
			errColored,
		)
	}

	w.Flush()
}

// PrintJSON outputs results as a JSON array.
func PrintJSON(results []runner.Result) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		color.Red("JSON encode error: %v", err)
	}
}

// PrintDryRun shows the list of databases that would be targeted.
func PrintDryRun(labels []string) {
	if len(labels) == 0 {
		color.Yellow("No databases targeted.")
		return
	}

	color.Cyan("\nDry Run — %d database(s) would be affected:\n\n", len(labels))

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	color.New(color.FgCyan, color.Bold).Fprintf(w, "#\tTarget\n")
	fmt.Fprintf(w, "-\t------\n")
	for i, label := range labels {
		fmt.Fprintf(w, "%d\t%s\n", i+1, label)
	}
	w.Flush()
}

// PrintError prints a fatal error message.
func PrintError(msg string) {
	color.Red("Error: %s", msg)
}

// PrintWarning prints a warning message.
func PrintWarning(msg string) {
	color.Yellow("Warning: %s", msg)
}

// PrintInfo prints an informational message.
func PrintInfo(msg string) {
	color.Cyan("%s", msg)
}

// PrintBanner prints the startup banner.
func PrintBanner(version string) {
	color.New(color.FgCyan, color.Bold).Fprintf(os.Stderr, "\n  propq %s\n", version)
	color.New(color.FgCyan).Fprintf(os.Stderr, "  MySQL/MariaDB async SQL executor\n\n")
}

// PrintDestructiveWarning prints a warning about destructive SQL.
func PrintDestructiveWarning() {
	color.New(color.FgRed, color.Bold).Fprintln(os.Stderr, "\n  ⚠ Destructive SQL detected!")
	color.New(color.FgRed).Fprintln(os.Stderr, "  This query contains DROP, TRUNCATE, DELETE, or ALTER TABLE statements.")
	color.New(color.FgRed).Fprintln(os.Stderr, "  Use --force to execute.")
}

// PromptYesNo asks the user a yes/no question and returns true for yes.
func PromptYesNo(format string, args ...any) bool {
	msg := fmt.Sprintf(format, args...)
	color.New(color.FgYellow, color.Bold).Fprint(os.Stderr, msg+" [y/N] ")

	var response string
	fmt.Scanln(&response)
	response = strings.TrimSpace(strings.ToLower(response))
	return response == "y" || response == "yes"
}

// PrintTargetCount prints how many databases will be targeted.
func PrintTargetCount(count int) {
	color.Cyan("  → %d database(s) targeted\n\n", count)
}

// PrintSection prints a section header to stderr.
func PrintSection(title string) {
	color.New(color.FgCyan, color.Bold).Fprintf(os.Stderr, "\n── %s ──\n\n", title)
}

// PrintStep prints a step message.
func PrintStep(emoji, msg string) {
	color.New(color.FgCyan).Fprintf(os.Stderr, "  %s %s\n", emoji, msg)
}
