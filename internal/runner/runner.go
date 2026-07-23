// Package runner executes SQL asynchronously across multiple databases.
package runner

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"
	"github.com/schollz/progressbar/v3"
	"github.com/v0id00/propq/internal/config"
)

// Status describes the result of a single database execution.
type Status string

const (
	StatusOK    Status = "OK"
	StatusError Status = "ERR"
	StatusSkip  Status = "SKIP"
)

// RowResult holds result rows for a SELECT query.
type RowResult struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// Result holds the outcome of executing SQL on one database.
type Result struct {
	Server   string     `json:"server"`
	Database string     `json:"database"`
	Status   Status     `json:"status"`
	Rows     *RowResult `json:"rows,omitempty"`
	Affected int64      `json:"affected"`
	Error    string     `json:"error,omitempty"`
	Elapsed  string     `json:"elapsed"`
}

// Target represents a single (server, database) pair to execute on.
type Target struct {
	Server string
	DB     string
}

// FilterConfig controls which servers and databases are targeted.
type FilterConfig struct {
	ServerRegex string
	DBFilter    string
	ExcludeDB   string
}

// RunConfig holds execution parameters.
type RunConfig struct {
	Timeout     int
	Concurrency int
	DryRun      bool
	Force       bool
	NoTxn       bool
	Filter      FilterConfig
	ShowBar     bool        // show progress bar
	Targets     []Target    // pre-filtered targets (if set, skips fetch+filter)
	All         bool        // run on ALL databases (per-DB mode); default: once per server
	Stream      bool         // print results live as they complete
	OnResult    func(Result) // optional callback for each result (streaming)
	AskCommit   bool        // per-target confirmation before executing
	SQLText     string      // SQL text shown in ask-commit prompt
}

// Run executes the SQL on all matching databases.
func Run(conns []config.Connection, sqlContent string, cfg RunConfig) ([]Result, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30
	}

	var filtered []config.Connection

	// ── Default: once per server (fast) ──
	// ── Per-database mode only when --all is set ──
	if !cfg.All {
		filtered = filterServers(conns, cfg.Filter.ServerRegex)
		if len(filtered) == 0 {
			return nil, fmt.Errorf("no servers match filter: %s", cfg.Filter.ServerRegex)
		}
		return runOncePerServer(filtered, sqlContent, cfg, timeout)
	}

	// ── Normal (per-database) mode ──
	// If pre-filtered targets provided, use them directly
	var targets []target

	if cfg.Targets != nil {
		for _, t := range cfg.Targets {
			targets = append(targets, target{server: t.Server, database: t.DB})
		}
		// Build filtered conn list from targets
		connMap := make(map[string]config.Connection)
		for _, c := range conns {
			connMap[c.Name] = c
		}
		seen := make(map[string]bool)
		for _, t := range targets {
			if !seen[t.server] {
				seen[t.server] = true
				if c, ok := connMap[t.server]; ok {
					filtered = append(filtered, c)
				}
			}
		}
	} else {
		// Normal flow: filter servers, fetch DB lists, apply filters
		filtered = filterServers(conns, cfg.Filter.ServerRegex)
		if len(filtered) == 0 {
			return nil, fmt.Errorf("no servers match filter: %s", cfg.Filter.ServerRegex)
		}

		serverDBs, err := fetchAllDatabases(filtered, timeout)
		if err != nil {
			return nil, fmt.Errorf("fetch databases: %w", err)
		}

		for serverName, dbs := range serverDBs {
			for _, db := range dbs {
				targets = append(targets, target{server: serverName, database: db})
			}
		}

		targets = filterDatabases(targets, cfg.Filter.DBFilter, cfg.Filter.ExcludeDB)
	}

	if len(targets) == 0 {
		return nil, fmt.Errorf("no databases match the filters")
	}

	// AskCommit + All mode: sequential per-database with confirmation
	if cfg.AskCommit && cfg.All {
		return runSequentialWithPrompt(targets, conns, sqlContent, cfg, timeout)
	}

	// Build connection map and per-server semaphores
	connMap := make(map[string]config.Connection, len(filtered))
	serverSems := make(map[string]chan struct{})
	for _, c := range filtered {
		connMap[c.Name] = c
		limit := c.MaxConnections
		if limit <= 0 {
			limit = 3
		}
		serverSems[c.Name] = make(chan struct{}, limit)
	}

	// Progress bar
	totalTasks := len(targets)
	bar := buildProgressBar(totalTasks, cfg.ShowBar, "Executing")

	// Channels for cancellation and results
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resultCh := make(chan Result, totalTasks)
	var wg sync.WaitGroup

	// Group targets by server, then launch with per-server semaphore
	serverTargets := make(map[string][]target)
	for _, t := range targets {
		serverTargets[t.server] = append(serverTargets[t.server], t)
	}

	for _, c := range filtered {
		tgs := serverTargets[c.Name]
		sem := serverSems[c.Name]

		for _, t := range tgs {
			wg.Add(1)
			go func(conn config.Connection, t target, sem chan struct{}) {
				defer wg.Done()
				defer func() {
					if bar != nil {
						bar.Add(1)
					}
				}()

				// Per-server semaphore (like db-runner)
				sem <- struct{}{}
				defer func() { <-sem }()

				// Check cancellation
				select {
				case <-ctx.Done():
					resultCh <- Result{
						Server: conn.Name, Database: t.database,
						Status: StatusSkip,
					}
					return
				default:
				}

				if cfg.DryRun {
					resultCh <- Result{
						Server: conn.Name, Database: t.database,
						Status: StatusOK,
					}
					return
				}

				start := time.Now()
				r := executeOnDBLikeDBRunner(ctx, conn, t.database, sqlContent, timeout, cfg.NoTxn)
				r.Server = conn.Name
				r.Database = t.database
				r.Elapsed = time.Since(start).Round(time.Millisecond).String()
				resultCh <- r
			}(c, t, sem)
		}
	}

	// Close when all done
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// Collect results
	var results []Result
	for r := range resultCh {
		results = append(results, r)
		if cfg.OnResult != nil {
			cfg.OnResult(r)
		}
	}

	if bar != nil {
		bar.Finish()
		fmt.Fprintln(os.Stderr)
	}

	return results, nil
}

// target represents a (server, database) pair.
type target struct {
	server   string
	database string
}

// filterServers filters connections by server name regex.
func filterServers(conns []config.Connection, serverRegex string) []config.Connection {
	if serverRegex == "" {
		return conns
	}
	re, err := regexp.Compile(serverRegex)
	if err != nil {
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

// filterDatabases filters targets by dbFilter and excludeDB regex.
func filterDatabases(targets []target, dbFilter, excludeDB string) []target {
	if dbFilter == "" && excludeDB == "" {
		return targets
	}

	var filterRe, excludeRe *regexp.Regexp
	var err error
	if dbFilter != "" {
		filterRe, err = regexp.Compile(dbFilter)
		if err != nil {
			return targets
		}
	}
	if excludeDB != "" {
		excludeRe, err = regexp.Compile(excludeDB)
		if err != nil {
			return targets
		}
	}

	var filtered []target
	for _, t := range targets {
		if filterRe != nil && !filterRe.MatchString(t.database) {
			continue
		}
		if excludeRe != nil && excludeRe.MatchString(t.database) {
			continue
		}
		filtered = append(filtered, t)
	}
	return filtered
}

// fetchAllDatabases connects to servers concurrently and lists databases.
func fetchAllDatabases(conns []config.Connection, timeout int) (map[string][]string, error) {
	type fetchResult struct {
		name string
		dbs  []string
		err  string
	}

	ch := make(chan fetchResult, len(conns))
	var wg sync.WaitGroup

	for _, c := range conns {
		wg.Add(1)
		go func(conn config.Connection) {
			defer wg.Done()
			dbs, err := fetchDBList(conn, timeout)
			if err != nil {
				ch <- fetchResult{name: conn.Name, err: err.Error()}
				return
			}
			ch <- fetchResult{name: conn.Name, dbs: dbs}
		}(c)
	}

	go func() {
		wg.Wait()
		close(ch)
	}()

	serverDBs := make(map[string][]string)
	var errs []string
	for r := range ch {
		if r.err != "" {
			errs = append(errs, fmt.Sprintf("%s: %s", r.name, r.err))
			continue
		}
		serverDBs[r.name] = r.dbs
	}

	if len(serverDBs) == 0 {
		return nil, fmt.Errorf("could not connect to any server: %s", strings.Join(errs, "; "))
	}
	if len(errs) > 0 {
		fmt.Fprintf(os.Stderr, "  ⚠ %d server(s) had errors: %s\n", len(errs), strings.Join(errs, "; "))
	}

	return serverDBs, nil
}

// systemDBs are skipped automatically.
var systemDBs = map[string]bool{
	"information_schema": true,
	"mysql":              true,
	"performance_schema": true,
	"sys":                true,
	"innodb":             true,
}

// fetchDBList connects to a server and lists non-system databases.
func fetchDBList(conn config.Connection, timeout int) ([]string, error) {
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

// executeOnDBLikeDBRunner connects to a database and runs the SQL.
// Returns rows for SELECT/SHOW/EXPLAIN/DESC queries.
func executeOnDBLikeDBRunner(ctx context.Context, conn config.Connection, dbName, sqlStr string, timeout int, noTxn bool) Result {
	autocommit := "true"
	if !noTxn {
		autocommit = "false"
	}

	dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/%s?timeout=%ds&readTimeout=%ds&writeTimeout=%ds&multiStatements=true&autocommit=%s",
		conn.User, conn.Password, conn.Host, conn.Port, dbName, timeout, timeout, timeout, autocommit)
	// Per-server mode (* means no database selected)
	dsn = strings.Replace(dsn, "/"+"*"+"?", "/?", 1)

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("connect: %v", err)}
	}
	defer db.Close()

	db.SetConnMaxLifetime(time.Duration(timeout) * time.Second)
	db.SetMaxOpenConns(1)

	queryCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	// Detect SELECT-like queries and return rows
	if isSelectQuery(sqlStr) {
		rows, err := db.QueryContext(queryCtx, sqlStr)
		if err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("query: %v", err)}
		}

		// Read first result set
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return Result{Status: StatusError, Error: fmt.Sprintf("columns: %v", err)}
		}

		data := readRows(rows)
		rows.Close()

		// Consume any remaining result sets (multiStatements)
		for rows.NextResultSet() {
			rows.Close()
		}

		if err := rows.Err(); err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("rows: %v", err)}
		}

		rr := &RowResult{Columns: columns, Rows: data}
		affected := int64(len(data))

		if !noTxn {
			db.ExecContext(ctx, "COMMIT")
		}

		return Result{Status: StatusOK, Affected: affected, Rows: rr}
	}

	// Non-SELECT: use ExecContext
	res, err := db.ExecContext(queryCtx, sqlStr)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("execute: %v", err)}
	}

	if !noTxn {
		db.ExecContext(ctx, "COMMIT")
	}

	affected, _ := res.RowsAffected()
	return Result{Status: StatusOK, Affected: affected}
}

// readRows reads all rows from a result set into [][]string.
func readRows(rows *sql.Rows) [][]string {
	cols, _ := rows.Columns()
	if cols == nil {
		return nil
	}
	var data [][]string
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		valPtrs := make([]interface{}, len(cols))
		for i := range vals {
			valPtrs[i] = &vals[i]
		}
		if err := rows.Scan(valPtrs...); err != nil {
			return data
		}
		row := make([]string, len(cols))
		for i, v := range vals {
			if v == nil {
				row[i] = "NULL"
			} else {
				switch val := v.(type) {
				case []byte:
					row[i] = string(val)
				default:
					row[i] = fmt.Sprintf("%v", val)
				}
			}
		}
		data = append(data, row)
	}
	return data
}

// isSelectQuery returns true if the SQL is a SELECT-like query that returns rows.
func isSelectQuery(sql string) bool {
	trimmed := strings.TrimSpace(sql)
	upper := strings.ToUpper(trimmed)
	// Check common row-returning statements
	for _, prefix := range []string{"SELECT", "SHOW", "EXPLAIN", "DESC", "DESCRIBE", "WITH"} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	// Also check for UNION (e.g. SELECT ... UNION SELECT ...)
	if strings.Contains(upper, " UNION ") && strings.Contains(upper, "SELECT") {
		return true
	}
	return false
}

// CountTargets returns the list of databases that would be targeted, without executing.
func CountTargets(conns []config.Connection, filter FilterConfig, timeout int) ([]string, int, error) {
	filtered := filterServers(conns, filter.ServerRegex)
	if len(filtered) == 0 {
		return nil, 0, fmt.Errorf("no servers match filter")
	}

	serverDBs, err := fetchAllDatabases(filtered, timeout)
	if err != nil {
		return nil, 0, err
	}

	var targets []target
	for serverName, dbs := range serverDBs {
		for _, db := range dbs {
			targets = append(targets, target{server: serverName, database: db})
		}
	}
	targets = filterDatabases(targets, filter.DBFilter, filter.ExcludeDB)

	var labels []string
	for _, t := range targets {
		labels = append(labels, fmt.Sprintf("%s.%s", t.server, t.database))
	}
	return labels, len(targets), nil
}

// runSequentialWithPrompt executes SQL on each target sequentially,
// asking for confirmation before each one. Used by --ask-for-commit + --all.
func runSequentialWithPrompt(targets []target, conns []config.Connection, sqlContent string, cfg RunConfig, timeout int) ([]Result, error) {
	connMap := make(map[string]config.Connection, len(conns))
	for _, c := range conns {
		connMap[c.Name] = c
	}

	var results []Result
	for _, t := range targets {
		conn, ok := connMap[t.server]
		if !ok {
			results = append(results, Result{
				Server: t.server, Database: t.database,
				Status: StatusError, Error: fmt.Sprintf("unknown server: %s", t.server),
			})
			continue
		}

		// Show SQL and ask
		label := fmt.Sprintf("%s.%s", t.server, t.database)
		sqlPreview := strings.ReplaceAll(strings.TrimSpace(sqlContent), "\n", " ")
		if len(sqlPreview) > 60 {
			sqlPreview = sqlPreview[:57] + "..."
		}
		fmt.Fprintf(os.Stderr, "\n  ◇  %s\n     %s\n", label, sqlPreview)

		if !promptYesNo("Execute on this database?") {
			results = append(results, Result{
				Server: t.server, Database: t.database,
				Status: StatusSkip,
			})
			continue
		}

		// Execute with transaction (autocommit=false + manual commit)
		start := time.Now()
		r := executeOnDBLikeDBRunner(context.Background(), conn, t.database, sqlContent, timeout, false)
		r.Server = conn.Name
		r.Database = t.database
		r.Elapsed = time.Since(start).Round(time.Millisecond).String()

		// Print result immediately and clear rows for final summary
		if r.Status == StatusOK && r.Rows != nil && len(r.Rows.Rows) > 0 {
			fmt.Fprintf(os.Stderr, "  ✓ %s.%s (%d row(s))\n", r.Server, r.Database, len(r.Rows.Rows))
			for _, row := range r.Rows.Rows {
				fmt.Fprintf(os.Stderr, "    %s\n", strings.Join(row, " │ "))
			}
			r.Rows = nil // don't print again in final summary
		} else if r.Status == StatusOK {
			fmt.Fprintf(os.Stderr, "  ✓ %s.%s\n", r.Server, r.Database)
		} else if r.Status == StatusError {
			fmt.Fprintf(os.Stderr, "  ✗ %s.%s  %s\n", r.Server, r.Database, r.Error)
		}

		results = append(results, r)
	}

	return results, nil
}

// promptYesNo asks a simple y/n question on stderr.
func promptYesNo(msg string) bool {
	fmt.Fprintf(os.Stderr, "  %s [y/N] ", msg)
	var resp string
	fmt.Scanln(&resp)
	resp = strings.TrimSpace(strings.ToLower(resp))
	return resp == "y" || resp == "yes"
}

// HasDestructiveKeywords checks if SQL contains destructive operations.
func HasDestructiveKeywords(sql string) bool {
	upper := strings.ToUpper(sql)
	patterns := []string{
		`\bDROP\b`,
		`\bTRUNCATE\b`,
		`\bDELETE\s+FROM\b`,
		`\bALTER\s+TABLE\b`,
	}
	for _, p := range patterns {
		matched, _ := regexp.MatchString(p, upper)
		if matched {
			return true
		}
	}
	return false
}

// runOncePerServer connects to each server once (without selecting a DB)
// and executes the SQL. Returns one result per server.
func runOncePerServer(conns []config.Connection, sqlContent string, cfg RunConfig, timeout int) ([]Result, error) {
	// Build per-server semaphores
	type semSlot struct {
		ch chan struct{}
	}
	serverSems := make(map[string]*semSlot, len(conns))
	for _, c := range conns {
		maxConn := c.MaxConnections
		if cfg.Concurrency > 0 && cfg.Concurrency < maxConn {
			maxConn = cfg.Concurrency
		}
		serverSems[c.Name] = &semSlot{ch: make(chan struct{}, maxConn)}
	}

	// Progress bar
	bar := buildProgressBar(len(conns), cfg.ShowBar, " Servers")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resultCh := make(chan Result, len(conns))
	var wg sync.WaitGroup

	for _, c := range conns {
		wg.Add(1)
		go func(conn config.Connection) {
			defer wg.Done()
			defer func() {
				if bar != nil {
					bar.Add(1)
				}
			}()

			slot := serverSems[conn.Name]
			slot.ch <- struct{}{}
			defer func() { <-slot.ch }()

			select {
			case <-ctx.Done():
				resultCh <- Result{Server: conn.Name, Database: "*", Status: StatusSkip}
				return
			default:
			}

			if cfg.DryRun {
				resultCh <- Result{Server: conn.Name, Database: "*", Status: StatusOK}
				return
			}

			start := time.Now()
			r := executeOnDBLikeDBRunner(ctx, conn, "*", sqlContent, timeout, cfg.NoTxn)
			r.Server = conn.Name
			r.Database = "*"
			r.Elapsed = time.Since(start).Round(time.Millisecond).String()
			resultCh <- r
		}(c)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var results []Result
	for r := range resultCh {
		results = append(results, r)
		if cfg.OnResult != nil {
			cfg.OnResult(r)
		}
	}

	if bar != nil {
		bar.Finish()
		fmt.Fprintln(os.Stderr)
	}

	return results, nil
}

// buildProgressBar creates a new progress bar if conditions are met.
func buildProgressBar(total int, show bool, label string) *progressbar.ProgressBar {
	if !show || total == 0 || !isTerminal() {
		return nil
	}
	return progressbar.NewOptions(total,
		progressbar.OptionSetDescription(" "+label),
		progressbar.OptionSetWriter(os.Stderr),
		progressbar.OptionShowCount(),
		progressbar.OptionThrottle(65*time.Millisecond),
		progressbar.OptionSpinnerType(14),
		progressbar.OptionFullWidth(),
		progressbar.OptionSetRenderBlankState(true),
	)
}

// isTerminal returns true if stderr is a terminal (for progress bar).
func isTerminal() bool {
	stat, _ := os.Stderr.Stat()
	return (stat.Mode() & os.ModeCharDevice) != 0
}
