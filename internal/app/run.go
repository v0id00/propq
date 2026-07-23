// Package app wires everything together — CLI flags, orchestrator, version.
package app

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/v0id00/propq/internal/config"
	"github.com/v0id00/propq/internal/display"
	"github.com/v0id00/propq/internal/runner"
	"github.com/v0id00/propq/internal/scanner"
)

// version is set at build time via -ldflags.
var version = "dev"

// Execute creates and runs the root command.
func Execute() {
	rootCmd := newRootCmd()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

// appConfig holds all runtime configuration, sourced from CLI flags
// with config file defaults merged in.
type appConfig struct {
	configPath string
	sql        string
	file       string
	edit       bool
	server     string
	dbfilter   string
	excludeDB  string
	timeout    int
	concurrency int
	force      bool
	dryRun     bool
	noTxn      bool
	json       bool
	noProgress bool
	noConfirm  bool
	version    bool
}

func newRootCmd() *cobra.Command {
	ac := &appConfig{}

	cmd := &cobra.Command{
		Use:   "propq",
		Short: "Async SQL executor for MySQL/MariaDB",
		Long: `propq — Async SQL executor for multiple MySQL/MariaDB servers.

Reads a TOML config file with connection definitions, then executes SQL
across all matching databases concurrently.

All execution behaviour can be set in the config file (propq.toml).
CLI flags override the config values when explicitly provided.

SQL sources (in priority order):
  --sql QUERY    Inline SQL
  -f, --file FILE  Read from file (use '-' for stdin)
  -e, --edit       Open $EDITOR to write SQL
  stdin pipe        echo 'SELECT 1' | propq`,
		Example: `  propq --sql "SELECT COUNT(*) FROM users" -s prod
  propq -f migration.sql -d "^shop_"
  echo "SELECT 1" | propq
  propq -e`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return run(ac, cmd)
		},
	}

	flags := cmd.Flags()

	// SQL source
	flags.StringVar(&ac.sql, "sql", "", "Inline SQL query")
	flags.StringVarP(&ac.file, "file", "f", "", "Read SQL from file (use '-' for stdin)")
	flags.BoolVarP(&ac.edit, "edit", "e", false, "Open $EDITOR to write SQL")

	// Filtering
	flags.StringVarP(&ac.server, "server", "s", "", "Regex filter for server names")
	flags.StringVarP(&ac.dbfilter, "dbfilter", "d", "", "Regex filter for database names")
	flags.StringVar(&ac.excludeDB, "exclude-db", "", "Regex to exclude databases")

	// Execution
	flags.IntVar(&ac.timeout, "timeout", 0, "Query timeout in seconds (default: 30)")
	flags.IntVar(&ac.concurrency, "concurrency", 0, "Global concurrency limit (default: per-server max_connections)")
	flags.BoolVar(&ac.force, "force", false, "Skip confirmation for destructive SQL")
	flags.BoolVar(&ac.dryRun, "dry-run", false, "Show target databases without executing")
	flags.BoolVar(&ac.noTxn, "no-transaction", false, "Run in autocommit mode (no transaction)")

	// Output
	flags.BoolVar(&ac.json, "json", false, "Output as JSON (default: table)")
	flags.BoolVarP(&ac.noProgress, "quiet", "q", false, "Suppress progress bar and banners")
	flags.BoolVar(&ac.noConfirm, "no-confirm", false, "Skip confirmation prompt when no filter is set")

	// Config
	flags.StringVarP(&ac.configPath, "config", "c", "", "Path to config file")

	// Misc
	flags.BoolVar(&ac.version, "version", false, "Show version")

	return cmd
}

func run(ac *appConfig, cmd *cobra.Command) error {
	// --version
	if ac.version {
		fmt.Printf("propq %s\n", version)
		return nil
	}

	// 1. Load config
	cfgPath, err := config.FindConfigPath(ac.configPath)
	if err != nil {
		display.PrintError(err.Error())
		return err
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		display.PrintError(fmt.Sprintf("config: %v", err))
		return err
	}

	if !ac.noProgress {
		display.PrintBanner(version)
		display.PrintInfo(fmt.Sprintf("Config: %s\n", cfgPath))
		display.PrintInfo(fmt.Sprintf("Servers: %d\n", len(cfg.Connections)))
	}

	// 2. Merge config defaults with CLI overrides.
	// CLI flag wins if explicitly set on command line; otherwise config value is used.
	mergeConfigWithCLI(ac, cmd, &cfg.Defaults)

	// 3. Get SQL
	sqlInput, err := scanner.Scan(ac.sql, ac.file, ac.edit, os.Stdin)
	if err != nil {
		display.PrintError(err.Error())
		return err
	}

	if !ac.noProgress {
		display.PrintSection("SQL")
		switch sqlInput.Source {
		case scanner.SourceArg:
			display.PrintInfo(fmt.Sprintf("  Inline: %s\n", truncate(sqlInput.Content, 80)))
		case scanner.SourceFile:
			display.PrintInfo(fmt.Sprintf("  File: %s\n", sqlInput.Label))
		case scanner.SourcePipe:
			display.PrintInfo("  From stdin (piped)\n")
		case scanner.SourceEditor:
			display.PrintInfo(fmt.Sprintf("  From editor (%d lines)\n", len(sqlInput.Content)))
		}
	}

	// 4. Check destructive SQL
	if runner.HasDestructiveKeywords(sqlInput.Content) {
		if !ac.force {
			display.PrintDestructiveWarning()
			return fmt.Errorf("destructive SQL requires --force")
		}
		if !ac.noProgress {
			display.PrintWarning("  Destructive SQL — --force is active\n")
		}
	}

	conns := make([]config.Connection, 0, len(cfg.Connections))
	for _, c := range cfg.Connections {
		conns = append(conns, c)
	}

	filterCfg := runner.FilterConfig{
		ServerRegex: ac.server,
		DBFilter:    ac.dbfilter,
		ExcludeDB:   ac.excludeDB,
	}

	// 5. Dry run
	if ac.dryRun {
		display.PrintSection("Dry Run")
		labels, count, err := runner.CountTargets(conns, filterCfg, ac.timeout)
		if err != nil {
			display.PrintError(err.Error())
			return err
		}
		display.PrintDryRun(labels)
		display.PrintTargetCount(count)
		return nil
	}

	// 6. If no DB filter, warn and confirm
	if ac.dbfilter == "" && ac.excludeDB == "" {
		labels, count, err := runner.CountTargets(conns, filterCfg, ac.timeout)
		if err != nil {
			display.PrintError(err.Error())
			return err
		}

		if count > 0 {
			display.PrintSection("Targets")
			if count <= 20 {
				display.PrintDryRun(labels)
			} else {
				display.PrintInfo(fmt.Sprintf("  %d databases will be targeted\n", count))
			}

			if cfg.Defaults.ConfirmWithoutFilter && !ac.noConfirm {
				if !display.PromptYesNo("\n  Run on all %d database(s)?", count) {
					display.PrintInfo("  Cancelled.\n")
					return nil
				}
			} else if !ac.noProgress {
				display.PrintInfo("  Running (confirmation skipped by config or --no-confirm)\n")
			}
		} else {
			display.PrintWarning("  No databases found.\n")
			return nil
		}
	}

	// 7. Execute
	if !ac.noProgress {
		display.PrintSection("Execute")
	}

	runCfg := runner.RunConfig{
		Timeout:     ac.timeout,
		Concurrency: ac.concurrency,
		DryRun:      false,
		Force:       ac.force,
		NoTxn:       ac.noTxn,
		Filter:      filterCfg,
		ShowBar:     !ac.noProgress,
	}

	// Handle Ctrl+C gracefully
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	results, err := runner.Run(conns, sqlInput.Content, runCfg)
	if err != nil {
		display.PrintError(err.Error())
		return err
	}

	// 8. Output
	if ac.json {
		display.PrintJSON(results)
	} else {
		display.PrintTable(results)
	}

	return nil
}

// mergeConfigWithCLI applies config defaults for any flag NOT explicitly
// set on the command line.  Uses cobra's Changed() to detect explicit flags.
func mergeConfigWithCLI(ac *appConfig, cmd *cobra.Command, d *config.Defaults) {
	// String flags: apply config value only if CLI flag is empty string
	if !cmd.Flags().Changed("server") && d.ServerFilter != "" {
		ac.server = d.ServerFilter
	}
	if !cmd.Flags().Changed("dbfilter") && d.DBFilter != "" {
		ac.dbfilter = d.DBFilter
	}
	if !cmd.Flags().Changed("exclude-db") && d.ExcludeDB != "" {
		ac.excludeDB = d.ExcludeDB
	}

	// Int flags: apply config value only if CLI flag was not changed
	if !cmd.Flags().Changed("timeout") {
		ac.timeout = d.Timeout
	}
	if !cmd.Flags().Changed("concurrency") {
		ac.concurrency = d.Concurrency
	}

	// Bool flags: apply config value only if CLI flag was not changed.
	// Changed() returns true even if the user wrote --flag=false, which is
	// the best we can do (user explicitly opted into that value).
	if !cmd.Flags().Changed("force") && d.Force {
		ac.force = true
	}
	if !cmd.Flags().Changed("dry-run") && d.DryRun {
		ac.dryRun = true
	}
	if !cmd.Flags().Changed("json") && d.JSON {
		ac.json = true
	}
	if !cmd.Flags().Changed("quiet") && d.Quiet {
		ac.noProgress = true
	}
	if !cmd.Flags().Changed("no-transaction") && d.NoTxn {
		ac.noTxn = true
	}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
