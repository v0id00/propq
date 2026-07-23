// Package app wires everything together — CLI flags, orchestrator, version.
package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/fatih/color"
	"github.com/spf13/cobra"
	"github.com/v0id00/propq/pkg/config"
	"github.com/v0id00/propq/pkg/display"
	"github.com/v0id00/propq/pkg/history"
	"github.com/v0id00/propq/pkg/runner"
	"github.com/v0id00/propq/pkg/scanner"
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
	retry       int
	force       bool
	dryRun      bool
	noTxn       bool
	json        bool
	csv         bool
	noProgress  bool
	noConfirm   bool
	version     bool
	outputFile  string
	stream      bool
	history     bool
	noOutput    bool
	noError     bool
	noResult    bool
	askCommit   bool
	tags        string
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
	flags.StringVarP(&ac.tags, "tags", "t", "", "Filter by tags (comma-separated, OR)")
	flags.BoolVarP(&ac.selectMode, "select", "S", false, "Open editor to pick databases interactively")

	// Execution
	flags.IntVar(&ac.timeout, "timeout", 0, "Query timeout in seconds (default: 30)")
	flags.IntVar(&ac.concurrency, "concurrency", 0, "Global concurrency limit (default: per-server max_connections)")
	flags.IntVar(&ac.retry, "retry", 0, "Retry failed databases N times with exponential backoff")
	flags.BoolVar(&ac.force, "force", false, "Skip confirmation for destructive SQL")
	flags.BoolVar(&ac.dryRun, "dry-run", false, "Show target databases without executing")
	flags.BoolVarP(&ac.all, "all", "a", false, "Run on ALL databases (per-DB mode; default: once per server)")
	flags.BoolVar(&ac.noTxn, "no-transaction", false, "Run in autocommit mode (no transaction)")
	flags.BoolVar(&ac.askCommit, "ask-for-commit", false, "Show summary and ask before committing")

	// Output
	flags.BoolVar(&ac.json, "json", false, "Output as JSON (default: table)")
	flags.BoolVar(&ac.csv, "csv", false, "Output as CSV")
	flags.StringVarP(&ac.outputFile, "output", "o", "", "Save output to file (in addition to stdout)")
	flags.BoolVar(&ac.stream, "stream", false, "Print results live as they complete")
	flags.BoolVarP(&ac.noOutput, "no-output", "N", false, "Suppress result output, show only summary")
	flags.BoolVar(&ac.noError, "no-error", false, "Hide error entries from output")
	flags.BoolVar(&ac.noResult, "no-result", false, "Hide data rows, show only ✓/✗ per target")
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
	cmd.AddCommand(newConfigCmd())

	return cmd
}

// ---------------------------------------------------------------------------
// propq config  —  check, validate, generate config
// ---------------------------------------------------------------------------

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Manage configuration (check, generate, validate)",
	}

	// Shared -c flag used by all subcommands
	var cfgPath string

	checkCmd := &cobra.Command{
		Use:   "check",
		Short: "Validate config and test all server connections",
		Long: `Validate the propq configuration file and test connectivity
to all configured servers.

Exits with 0 if all servers are reachable, 1 otherwise.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigCheck(cfgPath)
		},
	}
	checkCmd.Flags().StringVarP(&cfgPath, "config", "c", "", "Path to config file")

	generateCmd := &cobra.Command{
		Use:   "generate",
		Short: "Generate a default config file",
		Long: `Generate a default config file with example settings.

With -o/--output: writes to the given path.
Without -o: writes to the platform config directory.
  Linux/macOS: ~/.config/propq/config.toml
  Windows:     %APPDATA%/propq/config.toml
Use -o - to print to stdout.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			oPath, _ := cmd.Flags().GetString("output")
			return runConfigGenerate(oPath)
		},
	}
	generateCmd.Flags().StringP("output", "o", "", "Output path (default: platform config dir, '-' for stdout)")

	validateCmd := &cobra.Command{
		Use:   "validate",
		Short: "Validate config file syntax and structure",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runConfigValidate(cfgPath)
		},
	}
	validateCmd.Flags().StringVarP(&cfgPath, "config", "c", "", "Path to config file")

	cmd.AddCommand(checkCmd)
	cmd.AddCommand(generateCmd)
	cmd.AddCommand(validateCmd)
	return cmd
}

// runConfigGenerate writes a default config file.
func runConfigGenerate(outputPath string) error {
	if outputPath == "" {
		outputPath = config.PlatformConfigPath()
		if outputPath == "" {
			return fmt.Errorf("could not determine platform config directory")
		}
		fmt.Fprintf(os.Stderr, "  Writing to %s\n", outputPath)
	}

	content := config.DefaultExample()

	if outputPath == "-" {
		fmt.Print(content)
		return nil
	}

	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create directory %s: %w", dir, err)
	}

	if err := os.WriteFile(outputPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  ✓ Config file created at %s\n", outputPath)
	fmt.Fprintf(os.Stderr, "    Edit it with: vim %s\n", outputPath)
	return nil
}

// runConfigValidate checks config file syntax and structure.
func runConfigValidate(cfgPath string) error {
	path, err := config.FindConfigPath(cfgPath)
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "  ✓ Config file found: %s\n", path)

	cfg, err := config.Load(path)
	if err != nil {
		return fmt.Errorf("config parse error: %w", err)
	}

	if len(cfg.Connections) == 0 {
		fmt.Fprintf(os.Stderr, "  ⚠ No connections defined in config\n")
	} else {
		fmt.Fprintf(os.Stderr, "  ✓ %d connections defined\n", len(cfg.Connections))
		for name, conn := range cfg.Connections {
			if conn.Host == "" || conn.User == "" {
				fmt.Fprintf(os.Stderr, "  ⚠ %s: missing host or user\n", name)
			} else {
				fmt.Fprintf(os.Stderr, "  ✓ %s → %s:%d\n", name, conn.Host, conn.Port)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "  ✓ Config is valid\n")
	return nil
}

func runConfigCheck(cfgPath string) error {
	path, err := config.FindConfigPath(cfgPath)
	if err != nil {
		display.PrintError(fmt.Sprintf("config: %v", err))
		return err
	}

	cfg, err := config.Load(path)
	if err != nil {
		display.PrintError(fmt.Sprintf("config parse: %v", err))
		return err
	}

	fmt.Fprintf(os.Stderr, "Config: %s\n", path)
	fmt.Fprintf(os.Stderr, "Servers: %d\n\n", len(cfg.Connections))

	ok := 0
	fail := 0
	for name, conn := range cfg.Connections {
		dsn := conn.DSN("", 5)
		db, err := sql.Open("mysql", dsn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %-15s  %s\n", name, err)
			fail++
			continue
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = db.PingContext(ctx)
		cancel()
		db.Close()

		if err != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %-15s  %s\n", name, err)
			fail++
		} else {
			fmt.Fprintf(os.Stderr, "  ✓ %-15s  %s:%d\n", name, conn.Host, conn.Port)
			ok++
		}
	}

	fmt.Fprintf(os.Stderr, "\n%d OK, %d FAIL of %d servers\n", ok, fail, len(cfg.Connections))
	if fail > 0 {
		return fmt.Errorf("%d server(s) unreachable", fail)
	}
	return nil
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
		tagsF   string
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
			return runServers(cfgPath, server, jsonOut, timeout, quiet, tagsF)
		},
	}

	fl := cmd.Flags()
	fl.StringVarP(&cfgPath, "config", "c", "", "Path to config file")
	fl.StringVarP(&server, "server", "s", "", "Regex filter for server names")
	fl.StringVar(&tagsF, "tags", "", "Filter by tags (comma-separated)")
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

func runServers(cfgPath, serverRegex string, jsonOut bool, timeout int, quiet bool, tags string) error {
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

	// Filter by tags
	tagList := splitTags(tags)
	if len(tagList) > 0 {
		var tagged []config.Connection
		for _, c := range conns {
			for _, ct := range c.Tags {
				for _, ft := range tagList {
					if ct == ft {
						tagged = append(tagged, c)
						break
					}
				}
			}
		}
		conns = tagged
		if len(conns) == 0 {
			display.PrintError(fmt.Sprintf("no servers match tags: %s", tags))
			return fmt.Errorf("no servers match tags: %s", tags)
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

// splitTags splits a comma-separated tag string into a slice.
func splitTags(s string) []string {
	if s == "" {
		return nil
	}
	var tags []string
	for _, t := range strings.Split(s, ",") {
		t = strings.TrimSpace(t)
		if t != "" {
			tags = append(tags, t)
		}
	}
	return tags
}

// matchString returns true if the regex matches the string.
func matchString(pattern, s string) bool {
	re := compileRegex(pattern)
	if re == nil {
		return true // no filter = match all
	}
	return re.MatchString(s)
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
		display.PrintConfigInfo(cfgPath, len(cfg.Connections))
	}

	// 2. Merge config defaults with CLI overrides
	mergeConfigWithCLI(ac, cmd, &cfg.Defaults)

	// -d or -D implies --all (per-database mode)
	if (ac.dbfilter != "" || ac.excludeDB != "") && !cmd.Flags().Changed("all") {
		ac.all = true
	}

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
		sqlLabel := fmt.Sprintf("inline: %s", truncate(sqlInput.Content, 60))
		if sqlInput.Source == scanner.SourceFile {
			sqlLabel = fmt.Sprintf("file: %s", sqlInput.Label)
		} else if sqlInput.Source == scanner.SourcePipe {
			sqlLabel = "stdin (piped)"
		} else if sqlInput.Source == scanner.SourceEditor {
			sqlLabel = fmt.Sprintf("editor (%d lines)", len(sqlInput.Content))
		}
		display.PrintSQLInfo(sqlLabel)
	}

	// 4. Check destructive SQL
	if runner.HasDestructiveKeywords(sqlInput.Content) {
		if !ac.force {
			display.PrintDestructiveWarning()
			return fmt.Errorf("destructive SQL requires --force")
		}
		if !ac.noProgress {
			display.PrintWarning("Destructive SQL — --force active")
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
		Tags:        splitTags(ac.tags),
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
			display.PrintWarning("No databases found.")
			return nil
		}

		display.PrintInfo(fmt.Sprintf("Opening editor to pick from %d database(s)...", count))

		headerComment := "# Delete lines you do NOT want to target, then :wq\n" +
			"# Format: server.database\n" +
			"# To select all, just save: :wq\n\n"

		selected, err := scanner.SelectFromEditor(labels, headerComment, editor)
		if err != nil {
			display.PrintError(fmt.Sprintf("editor: %v", err))
			return err
		}

		if len(selected) == 0 {
			display.PrintWarning("No databases selected.")
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

		display.PrintDBTarget(len(selectedTargets))
	}

	// 6. Dry run
	if ac.dryRun {
		if ac.selectMode {
			var labels []string
			for _, t := range selectedTargets {
				labels = append(labels, fmt.Sprintf("%s.%s", t.Server, t.DB))
			}
			display.PrintDryRun(labels)
		} else if !ac.all {
			filtered := filterConnections(conns, ac.server)
			var labels []string
			for _, c := range filtered {
				labels = append(labels, c.Name)
			}
			display.PrintDryRun(labels)
		} else {
			labels, count, err := runner.CountTargets(conns, filterCfg, ac.timeout)
			if err != nil {
				display.PrintError(err.Error())
				return err
			}
			display.PrintDryRun(labels)
			display.PrintDBTarget(count)
		}
		return nil
	}

	// 7. If --all mode with no DB filter, warn and confirm
	if !ac.selectMode && ac.all && ac.dbfilter == "" && ac.excludeDB == "" {
		_, count, err := runner.CountTargets(conns, filterCfg, ac.timeout)
		if err != nil {
			display.PrintError(err.Error())
			return err
		}

		if count > 0 {
			display.PrintNoDBFilterWarning()
			display.PrintDBTarget(count)

			if cfg.Defaults.ConfirmWithoutFilter && !ac.noConfirm {
				if !display.PromptYesNo("Run on all %d database(s)?", count) {
					display.PrintCancelled()
					return nil
				}
			} else if !ac.noProgress {
				display.PrintInfo("Running (confirmation skipped)")
			}
		} else {
			display.PrintWarning("No databases found.")
			return nil
		}
	}

	// 8. If --ask-for-commit: show targets, ask, then execute
	if ac.askCommit {
		if !ac.all {
			// Per-server mode: show server list + SQL, ask once
			var labels []string
			for _, c := range conns {
				if ac.server == "" || matchString(ac.server, c.Name) {
					labels = append(labels, c.Name)
				}
			}
			sqlPreview := sqlInput.Content
			if len(sqlPreview) > 60 {
				sqlPreview = sqlPreview[:57] + "..."
			}
			display.PrintDryRun(labels)
			display.PrintInfo(fmt.Sprintf("SQL: %s", sqlPreview))
			display.PrintDBTarget(len(labels))
			if !display.PromptYesNo("Commit changes to %d server(s)?", len(labels)) {
				display.PrintCancelled()
				return nil
			}
		}
		// Per-DB mode: handled in runner.Run via AskCommit flag
	}

	// 9. Execute
	if !ac.noProgress && !ac.stream {
		fmt.Fprintln(os.Stderr)
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
		Retry:       ac.retry,
		AskCommit:   ac.askCommit,
		SQLText:     sqlInput.Content,
	}
	if ac.stream {
		runCfg.OnResult = display.PrintStreamResult
		if ac.noError || ac.noResult {
			origCallback := runCfg.OnResult
			runCfg.OnResult = func(r runner.Result) {
				if ac.noError && r.Status == runner.StatusError {
					return
				}
				if ac.noResult {
					r.Rows = nil
				}
				origCallback(r)
			}
		}
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

	// 10. Output
	if ac.stream && ac.noOutput {
		display.PrintDone(countOK(results), countERR(results))
	} else if ac.noOutput {
		display.PrintDone(countOK(results), countERR(results))
	} else {
		out := filterResults(results, ac.noError, ac.noResult)
		var rendered string
		if ac.json {
			rendered = display.RenderJSON(out)
		} else if ac.csv {
			rendered = display.RenderCSV(out)
		} else {
			rendered = display.RenderTable(out)
		}
		os.Stdout.WriteString(rendered)
		if ac.outputFile != "" {
			os.WriteFile(ac.outputFile, []byte(rendered), 0644)
			display.PrintSaved(ac.outputFile)
		}
	}

	return nil
}

func countOK(results []runner.Result) int {
	n := 0
	for _, r := range results {
		if r.Status == runner.StatusOK {
			n++
		}
	}
	return n
}

func countERR(results []runner.Result) int {
	n := 0
	for _, r := range results {
		if r.Status == runner.StatusError {
			n++
		}
	}
	return n
}

// filterResults filters results based on noError and noResult flags.
func filterResults(results []runner.Result, noError, noResult bool) []runner.Result {
	if !noError && !noResult {
		return results
	}
	var out []runner.Result
	for _, r := range results {
		if noError && r.Status == runner.StatusError {
			continue
		}
		if noResult {
			r.Rows = nil
		}
		out = append(out, r)
	}
	return out
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
