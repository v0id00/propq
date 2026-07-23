// Package app wires everything together — CLI flags, orchestrator, version.
package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/v0id00/propq/internal/config"
	"github.com/v0id00/propq/internal/display"
	"github.com/v0id00/propq/internal/history"
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
	configPath  string
	sql         string
	file        string
	edit        bool
	server      string
	dbfilter    string
	excludeDB   string
	selectMode  bool
	all         bool
	timeout     int
	concurrency int
	force       bool
	dryRun      bool
	noTxn       bool
	json        bool
	noProgress  bool
	noConfirm   bool
	version     bool
	outputFile  string
	stream      bool
	history     bool
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
  propq -e
  propq servers`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(0),
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
	flags.StringVarP(&ac.excludeDB, "exclude-db", "D", "", "Regex to exclude databases (inverse of -d)")
	flags.BoolVarP(&ac.selectMode, "select", "S", false, "Open editor to pick databases interactively")

	// Execution
	flags.IntVar(&ac.timeout, "timeout", 0, "Query timeout in seconds (default: 30)")
	flags.IntVar(&ac.concurrency, "concurrency", 0, "Global concurrency limit (default: per-server max_connections)")
	flags.BoolVar(&ac.force, "force", false, "Skip confirmation for destructive SQL")
	flags.BoolVar(&ac.dryRun, "dry-run", false, "Show target databases without executing")
	flags.BoolVarP(&ac.all, "all", "a", false, "Run on ALL databases (per-DB mode; default: once per server)")
	flags.BoolVar(&ac.noTxn, "no-transaction", false, "Run in autocommit mode (no transaction)")

	// Output
	flags.BoolVar(&ac.json, "json", false, "Output as JSON (default: table)")
	flags.StringVarP(&ac.outputFile, "output", "o", "", "Save output to file (in addition to stdout)")
	flags.BoolVar(&ac.stream, "stream", false, "Print results live as they complete")
	flags.BoolVarP(&ac.noProgress, "quiet", "q", false, "Suppress progress bar and banners")
	flags.BoolVar(&ac.noConfirm, "no-confirm", false, "Skip confirmation prompt when no filter is set")

	// Config
	flags.StringVarP(&ac.configPath, "config", "c", "", "Path to config file")

	// Misc
	flags.BoolVar(&ac.version, "version", false, "Show version")
	flags.BoolVar(&ac.history, "history", false, "Show recent query history")

	// Subcommands
	cmd.AddCommand(newServersCmd())
	cmd.AddCommand(newCompletionCmd())
	cmd.AddCommand(newSkillCmd())

	return cmd
}

// ---------------------------------------------------------------------------
// propq completion  —  generate shell completion scripts
// ---------------------------------------------------------------------------

func newCompletionCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "completion [bash|zsh|fish|powershell]",
		Short: "Generate shell completion script",
		Long: `Generate shell completion script for propq.

To use:
  propq completion bash > /etc/bash_completion.d/propq
  propq completion zsh > /usr/local/share/zsh/site-functions/_propq
  propq completion fish > ~/.config/fish/completions/propq.fish
  propq completion powershell > propq.ps1

Then restart your shell or reload completion.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			case "powershell":
				return cmd.Root().GenPowerShellCompletion(os.Stdout)
			default:
				return fmt.Errorf("unknown shell: %s (supported: bash, zsh, fish, powershell)", args[0])
			}
		},
	}
	return cmd
}

// ---------------------------------------------------------------------------
// propq servers  —  list configured servers with database counts
// ---------------------------------------------------------------------------

func newServersCmd() *cobra.Command {
	var (
		cfgPath string
		server  string
		jsonOut bool
		timeout int
		quiet   bool
	)

	cmd := &cobra.Command{
		Use:   "servers",
		Short: "List configured servers and their database counts",
		Long: `List all servers from the config file, connect to each one,
and show how many databases they have.

You can filter by server name regex with -s.

Examples:
  propq servers
  propq servers -s "www1|www7"
  propq servers --json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServers(cfgPath, server, jsonOut, timeout, quiet)
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&cfgPath, "config", "c", "", "Path to config file")
	fl.StringVarP(&server, "server", "s", "", "Regex filter for server names")
	fl.BoolVar(&jsonOut, "json", false, "JSON output")
	fl.IntVar(&timeout, "timeout", 10, "Connection timeout in seconds")
	fl.BoolVarP(&quiet, "quiet", "q", false, "Suppress banner")

	return cmd
}

type serverInfo struct {
	Name       string `json:"name"`
	Host       string `json:"host"`
	Port       int    `json:"port"`
	Databases  int    `json:"databases"`
	Error      string `json:"error,omitempty"`
}

func runServers(cfgPath, serverRegex string, jsonOut bool, timeout int, quiet bool) error {
	resolved, err := config.FindConfigPath(cfgPath)
	if err != nil {
		display.PrintError(err.Error())
		return err
	}

	cfg, err := config.Load(resolved)
	if err != nil {
		display.PrintError(fmt.Sprintf("config: %v", err))
		return err
	}

	if !quiet {
		display.PrintInfo(fmt.Sprintf("Config: %s\n", resolved))
		display.PrintInfo(fmt.Sprintf("Servers: %d\n\n", len(cfg.Connections)))
	}

	// Filter servers
	conns := make([]config.Connection, 0, len(cfg.Connections))
	for _, c := range cfg.Connections {
		conns = append(conns, c)
	}

	if serverRegex != "" {
		conns = filterConnections(conns, serverRegex)
		if len(conns) == 0 {
			display.PrintError(fmt.Sprintf("no servers match: %s", serverRegex))
			return fmt.Errorf("no servers match: %s", serverRegex)
		}
	}

	// Fetch DB counts concurrently
	results := fetchServerDBs(conns, timeout)

	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(results)
		return nil
	}

	// Table output
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	color.New(color.FgCyan, color.Bold).Fprintf(w, "Server\tHost\tPort\tDatabases\tStatus\n")
	fmt.Fprintf(w, "------\t----\t----\t---------\t------\n")

	okCount := 0
	for _, r := range results {
		if r.Error == "" {
			okCount++
			status := color.GreenString("✓")
			fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\n", r.Name, r.Host, r.Port, r.Databases, status)
		} else {
			status := color.RedString("✗ %s", r.Error)
			fmt.Fprintf(w, "%s\t%s\t%d\t-\t%s\n", r.Name, r.Host, r.Port, status)
		}
	}
	w.Flush()

	fmt.Fprintf(os.Stdout, "\n%d/%d servers reachable\n", okCount, len(results))
	return nil
}

func filterConnections(conns []config.Connection, regex string) []config.Connection {
	re := compileRegex(regex)
	if re == nil {
		return conns
	}
	var filtered []config.Connection
	for _, c := range conns {
		if re.MatchString(c.Name) {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

func compileRegex(s string) *regexp.Regexp {
	if s == "" {
		return nil
	}
	re, err := regexp.Compile(s)
	if err != nil {
		return nil
	}
	return re
}

func fetchServerDBs(conns []config.Connection, timeout int) []serverInfo {
	// We'll use runner's fetchDBList function via the runner package
	// Actually runner.fetchDBList is unexported. Let me add a public API.
	// For now, implement inline.
	
	type fetchResult struct {
		info serverInfo
	}

	ch := make(chan fetchResult, len(conns))
	var wg sync.WaitGroup

	for _, c := range conns {
		wg.Add(1)
		go func(conn config.Connection) {
			defer wg.Done()
			si := serverInfo{
				Name: conn.Name,
				Host: conn.Host,
				Port: conn.Port,
			}
			// Use runner's public function or replicate
			dbs, err := fetchDBListPublic(conn, timeout)
			if err != nil {
				si.Error = err.Error()
			} else {
				si.Databases = len(dbs)
			}
			ch <- fetchResult{info: si}
		}(c)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	var results []serverInfo
	for r := range ch {
		results = append(results, r.info)
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})

	return results
}

// fetchDBListPublic is a copy of runner.fetchDBList exposed for the servers command.
func fetchDBListPublic(conn config.Connection, timeout int) ([]string, error) {
	dsn := conn.DSN("", timeout)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	defer db.Close()

	db.SetConnMaxLifetime(time.Duration(timeout) * time.Second)
	db.SetMaxOpenConns(2)

	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	rows, err := db.QueryContext(ctx, "SHOW DATABASES")
	if err != nil {
		return nil, fmt.Errorf("query: %w", err)
	}
	defer rows.Close()

	var dbs []string
	systemDBs := map[string]bool{
		"information_schema": true,
		"mysql":              true,
		"performance_schema": true,
		"sys":                true,
		"innodb":             true,
	}

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("scan: %w", err)
		}
		if !systemDBs[strings.ToLower(name)] {
			dbs = append(dbs, name)
		}
	}
	return dbs, rows.Err()
}

// ---------------------------------------------------------------------------
// Main run logic
// ---------------------------------------------------------------------------

func run(ac *appConfig, cmd *cobra.Command) error {
	// --version
	if ac.version {
		fmt.Printf("propq %s\n", version)
		return nil
	}

	// --history: show recent queries
	if ac.history {
		fmt.Print(history.List(10))
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

	// 2. Merge config defaults with CLI overrides
	mergeConfigWithCLI(ac, cmd, &cfg.Defaults)

	// 3. Get SQL
	editor := cfg.Defaults.Editor
	historyComment := history.Recent(5)
	sqlInput, err := scanner.Scan(ac.sql, ac.file, ac.edit, os.Stdin, editor, historyComment)
	if err != nil {
		display.PrintError(err.Error())
		return err
	}
	history.Save(sqlInput.Content)

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

	// 5. If --select mode, open editor for interactive DB selection
	var selectedTargets []runner.Target
	if ac.selectMode {
		labels, count, err := runner.CountTargets(conns, filterCfg, ac.timeout)
		if err != nil {
			display.PrintError(err.Error())
			return err
		}
		if count == 0 {
			display.PrintWarning("  No databases found.\n")
			return nil
		}

		display.PrintSection("Select Databases")
		display.PrintInfo(fmt.Sprintf("  %d database(s). Opening editor to pick...\n\n", count))

		headerComment := "# Delete lines you do NOT want to target, then :wq\n" +
			"# Format: server.database\n" +
			"# To select all, just save: :wq\n\n"

		selected, err := scanner.SelectFromEditor(labels, headerComment, editor)
		if err != nil {
			display.PrintError(fmt.Sprintf("editor: %v", err))
			return err
		}

		if len(selected) == 0 {
			display.PrintWarning("  No databases selected.\n")
			return nil
		}

		for _, s := range selected {
			if parts := parseServerDB(s); parts != nil {
				selectedTargets = append(selectedTargets, runner.Target{
					Server: parts[0],
					DB:     parts[1],
				})
			}
		}

		display.PrintInfo(fmt.Sprintf("  ✓ %d database(s) selected.\n\n", len(selectedTargets)))
	}

	// 6. Dry run
	if ac.dryRun {
		display.PrintSection("Dry Run")
		if ac.selectMode {
			var labels []string
			for _, t := range selectedTargets {
				labels = append(labels, fmt.Sprintf("%s.%s", t.Server, t.DB))
			}
			display.PrintDryRun(labels)
			display.PrintTargetCount(len(labels))
		} else if !ac.all {
			// Per-server mode: show servers only
			filtered := filterConnections(conns, ac.server)
			var labels []string
			for _, c := range filtered {
				labels = append(labels, c.Name)
			}
			display.PrintDryRun(labels)
			display.PrintInfo(fmt.Sprintf("  → %d server(s) targeted (default per-server mode)\n\n", len(labels)))
		} else {
			labels, count, err := runner.CountTargets(conns, filterCfg, ac.timeout)
			if err != nil {
				display.PrintError(err.Error())
				return err
			}
			display.PrintDryRun(labels)
			display.PrintTargetCount(count)
		}
		return nil
	}

	// 7. If --all mode with no DB filter, warn and confirm
	if !ac.selectMode && ac.all && ac.dbfilter == "" && ac.excludeDB == "" {
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
		ShowBar:     !ac.noProgress && !ac.stream,
		All:         ac.all,
		Stream:      ac.stream,
	}
	if ac.stream {
		runCfg.OnResult = display.PrintStreamResult
	}
	if ac.selectMode {
		runCfg.Targets = selectedTargets
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
	if ac.stream {
		// Results were already printed live by the runner
	} else if ac.json {
		rendered := display.RenderJSON(results)
		os.Stdout.WriteString(rendered)
		if ac.outputFile != "" {
			os.WriteFile(ac.outputFile, []byte(rendered), 0644)
			display.PrintStep("📁", fmt.Sprintf("Output saved to %s", ac.outputFile))
		}
	} else {
		rendered := display.RenderTable(results)
		os.Stdout.WriteString(rendered)
		if ac.outputFile != "" {
			os.WriteFile(ac.outputFile, []byte(rendered), 0644)
			display.PrintStep("📁", fmt.Sprintf("Output saved to %s", ac.outputFile))
		}
	}

	return nil
}

// mergeConfigWithCLI applies config defaults for any flag NOT explicitly
// set on the command line.
func mergeConfigWithCLI(ac *appConfig, cmd *cobra.Command, d *config.Defaults) {
	if !cmd.Flags().Changed("server") && d.ServerFilter != "" {
		ac.server = d.ServerFilter
	}
	if !cmd.Flags().Changed("dbfilter") && d.DBFilter != "" {
		ac.dbfilter = d.DBFilter
	}
	if !cmd.Flags().Changed("exclude-db") && d.ExcludeDB != "" {
		ac.excludeDB = d.ExcludeDB
	}
	if !cmd.Flags().Changed("timeout") {
		ac.timeout = d.Timeout
	}
	if !cmd.Flags().Changed("concurrency") {
		ac.concurrency = d.Concurrency
	}
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

// parseServerDB splits a "server.database" label into [server, database].
// Uses last dot as separator so server names containing dots still work.
func parseServerDB(label string) []string {
	dot := strings.LastIndex(label, ".")
	if dot < 1 {
		return nil
	}
	return []string{label[:dot], label[dot+1:]}
}
