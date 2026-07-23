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
	Once        bool        // run once per server, not per database
}

// Run executes the SQL on all matching databases.
func Run(conns []config.Connection, sqlContent string, cfg RunConfig) ([]Result, error) {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30
	}

	var filtered []config.Connection

	// ── Once mode: run SQL once per server (no per-DB iteration) ──
	if cfg.Once {
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

	// Build connection map
	connMap := make(map[string]config.Connection, len(filtered))
	for _, c := range filtered {
		connMap[c.Name] = c
	}

	// Global worker pool (single semaphore for all servers)
	poolSize := cfg.Concurrency
	if poolSize <= 0 {
		poolSize = 5
	}
	pool := make(chan struct{}, poolSize)

	// Progress bar
	totalTasks := len(targets)
	bar := buildProgressBar(totalTasks, cfg.ShowBar, "Executing")

	// Channels for cancellation and results
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	resultCh := make(chan Result, totalTasks)
	var wg sync.WaitGroup

	// Launch goroutines — all targets go into one shared pool
	for _, t := range targets {
		conn, ok := connMap[t.server]
		if !ok {
			resultCh <- Result{
				Server: t.server, Database: t.database,
				Status: StatusError, Error: fmt.Sprintf("unknown server: %s", t.server),
			}
			if bar != nil {
				bar.Add(1)
			}
			continue
		}

		wg.Add(1)
		go func(conn config.Connection, t target) {
			defer wg.Done()
			defer func() {
				if bar != nil {
					bar.Add(1)
				}
			}()

			// Acquire global pool slot
			pool <- struct{}{}
			defer func() { <-pool }()

			// Check cancellation
			select {
			case <-ctx.Done():
				resultCh <- Result{
					Server:   conn.Name,
					Database: t.database,
					Status:   StatusSkip,
				}
				return
			default:
			}

			if cfg.DryRun {
				resultCh <- Result{
					Server:   conn.Name,
					Database: t.database,
					Status:   StatusOK,
				}
				return
			}

			start := time.Now()
			r := executeOnDB(ctx, conn, t.database, sqlContent, timeout, cfg.NoTxn)
			r.Server = conn.Name
			r.Database = t.database
			r.Elapsed = time.Since(start).Round(time.Millisecond).String()
			resultCh <- r
		}(conn, t)
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

// executeOnDB connects to a database and runs the SQL.
func executeOnDB(ctx context.Context, conn config.Connection, dbName, sqlStr string, timeout int, noTxn bool) Result {
	dsn := conn.DSN(dbName, timeout)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("connect: %v", err)}
	}
	defer db.Close()

	db.SetConnMaxLifetime(time.Duration(timeout) * time.Second)
	db.SetMaxOpenConns(1)

	queryCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	if !noTxn {
		tx, err := db.BeginTx(queryCtx, nil)
		if err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("begin txn: %v", err)}
		}

		res, err := db.ExecContext(queryCtx, sqlStr)
		if err != nil {
			tx.Rollback()
			return Result{Status: StatusError, Error: fmt.Sprintf("execute: %v", err)}
		}

		if err := tx.Commit(); err != nil {
			return Result{Status: StatusError, Error: fmt.Sprintf("commit: %v", err)}
		}

		affected, _ := res.RowsAffected()
		return Result{Status: StatusOK, Affected: affected}
	}

	// Autocommit mode
	res, err := db.ExecContext(queryCtx, sqlStr)
	if err != nil {
		return Result{Status: StatusError, Error: fmt.Sprintf("execute: %v", err)}
	}
	affected, _ := res.RowsAffected()
	return Result{Status: StatusOK, Affected: affected}
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

			// Connect without selecting a database
			dsn := fmt.Sprintf("%s:%s@tcp(%s:%d)/?timeout=%ds&readTimeout=%ds&writeTimeout=%ds&multiStatements=true",
				conn.User, conn.Password, conn.Host, conn.Port, timeout, timeout, timeout)

			db, err := sql.Open("mysql", dsn)
			if err != nil {
				resultCh <- Result{
					Server: conn.Name, Database: "*", Status: StatusError,
					Error: fmt.Sprintf("connect: %v", err), Elapsed: time.Since(start).Round(time.Millisecond).String(),
				}
				return
			}
			db.SetConnMaxLifetime(time.Duration(timeout) * time.Second)
			db.SetMaxOpenConns(1)

			queryCtx, cancel2 := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
			defer cancel2()

			res, err := db.ExecContext(queryCtx, sqlContent)
			if err != nil {
				resultCh <- Result{
					Server: conn.Name, Database: "*", Status: StatusError,
					Error: fmt.Sprintf("execute: %v", err), Elapsed: time.Since(start).Round(time.Millisecond).String(),
				}
				db.Close()
				return
			}
			db.Close()

			affected, _ := res.RowsAffected()
			resultCh <- Result{
				Server: conn.Name, Database: "*", Status: StatusOK,
				Affected: affected, Elapsed: time.Since(start).Round(time.Millisecond).String(),
			}
		}(c)
	}

	go func() {
		wg.Wait()
		close(resultCh)
	}()

	var results []Result
	for r := range resultCh {
		results = append(results, r)
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
