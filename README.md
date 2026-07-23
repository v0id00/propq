# propq

**Async SQL executor for MySQL/MariaDB** — run queries across multiple servers concurrently from the CLI.

```bash
# One-liner: run SQL on all prod databases
propq --sql "SELECT COUNT(*) FROM users" --server prod

# File with filter
propq -f migration.sql -d "^shop_"

# Pipe it
echo "SELECT NOW(), DATABASE()" | propq -s staging

# Interactive editor mode
propq -e

# CI-friendly JSON output
propq --sql "SHOW TABLES" --server prod-* --json

# See what would happen (dry run)
propq --sql "DROP TABLE temp" --dry-run
```

## Features

- **Async execution** — goroutine pool with per-server connection limits
- **4 SQL sources** — inline (`--sql`), file (`-f`), pipe (stdin), editor (`-e`)
- **Smart filtering** — regex on server names (`-s`), database names (`-d`), or exclude (`--exclude-db`)
- **Human + machine output** — colorful tables by default, `--json` for scripts/AI
- **Safety first** — destructive SQL (DROP/TRUNCATE/DELETE/ALTER) requires `--force`
- **Dry run** — preview targets without executing
- **Transaction support** — runs queries in a transaction by default; `--no-transaction` for autocommit
- **No-db-filter warning** — prompts for confirmation when targeting all databases

## Installation

### From source

```bash
go install github.com/v0id00/propq/cmd/propq@latest
```

### Or build locally

```bash
git clone https://github.com/v0id00/propq.git
cd propq
make build
# binary is in build/propq
```

## Configuration

Create a TOML config file at `./propq.toml` or `~/.config/propq/config.toml`:

```toml
[defaults]
timeout = 30

[connections.prod-eu-1]
host = "db1.example.com"
port = 3306
user = "myuser"
password = "mypassword"
max_connections = 3
tags = ["prod", "eu"]

[connections.staging]
host = "staging.example.com"
port = 3306
user = "testuser"
password = "testpass"
```

Config search order:
1. `-c, --config` explicit path
2. `./propq.toml` (current directory)
3. `~/.config/propq/config.toml`

## Usage

```text
SQL sources (pick one):
  --sql QUERY            Inline SQL
  -f, --file FILE        Read from file ('-' for stdin)
  -e, --edit             Open $EDITOR to write SQL
  stdin pipe             echo 'SELECT 1' | propq

Filtering:
  -s, --server REGEX     Filter servers by name regex
  -d, --dbfilter REGEX   Filter databases by name regex
  --exclude-db REGEX     Exclude databases matching regex

Execution:
  --timeout SECONDS      Query timeout (default: 30)
  --concurrency N        Global concurrency limit
  --force                Allow destructive SQL
  --dry-run              Preview targets
  --no-transaction       Autocommit mode

Output:
  --json                 JSON output (default: table)
  -q, --quiet            Suppress banners and progress

Config:
  -c, --config FILE      Config file path
```

## Examples

```bash
# Quick check on all prod databases
propq --sql "SELECT 1" -s prod

# Run a migration file on shop databases
propq -f 001_create_users.sql -d "^shop_"

# Skip system backup databases
propq -f cleanup.sql --exclude-db "backup|archive"

# Preview without executing
propq --sql "DELETE FROM logs WHERE date < NOW() - INTERVAL 30 DAY" --dry-run

# JSON for AI/script consumption
propq --sql "SHOW TABLE STATUS" -s prod --json

# Run on specific server, all databases (with confirmation prompt)
propq -s staging -f schema_change.sql

# Editor mode: write SQL in vim, select DBs in vim
propq -e -s prod

# Override timeout for long queries
propq --timeout 120 -f report.sql -s analytics

# Quiet mode for cron
propq -q --json -s prod --sql "SELECT COUNT(*) FROM users"
```

## How it works

```
SQL → Scanner → Config → Runner → Display → stdout
                    ↓
               Filter (regex on servers + databases)
                    ↓
               Async executor (goroutines with semaphore)
                    ↓
               Result collection
```

1. **Scanner** reads SQL from argument, file, pipe, or editor
2. **Config** loads server connections from TOML
3. **Filter** applies `--server` / `--dbfilter` / `--exclude-db` regexes
4. **Runner** fetches database lists from each server, then executes SQL concurrently using per-server semaphores
5. **Display** formats results as a table (human) or JSON (machine)

## Development

```bash
# Build
make build

# Install to $GOPATH/bin
make install

# Run with args
make run ARGS="--sql \"SELECT 1\" -s prod"
```

## License

MIT
