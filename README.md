# propq

[![Go](https://img.shields.io/github/go-mod/go-version/v0id00/propq)](https://github.com/v0id00/propq)
[![Release](https://img.shields.io/github/v/release/v0id00/propq)](https://github.com/v0id00/propq/releases)
[![CI](https://github.com/v0id00/propq/actions/workflows/release.yml/badge.svg)](https://github.com/v0id00/propq/actions)

**Async SQL executor for MySQL/MariaDB** — run queries across multiple servers concurrently from the CLI.

```bash
# One-liner: run SQL on all prod databases
propq --sql "SELECT COUNT(*) FROM users" --server prod

# File with filter
propq -f migration.sql -d "^shop_"

# Pipe it
echo "SELECT NOW(), DATABASE()" | propq -s staging

# Stream results live
propq -s "www1|www6|www7" --sql "SELECT VERSION()" --stream

# CI-friendly JSON output
propq --sql "SHOW TABLE STATUS" -s prod --json

# See what would happen (dry run)
propq --sql "DROP TABLE temp" --dry-run
```

## Installation

### Go install (recommended)

```bash
go install github.com/v0id00/propq/cmd/propq@latest
```

### Download binary

Download the latest binary for your platform from the [releases page](https://github.com/v0id00/propq/releases):

```bash
# Linux amd64
curl -L https://github.com/v0id00/propq/releases/latest/download/propq-linux-amd64 -o propq
chmod +x propq
./propq --version
```

### Build from source

```bash
git clone https://github.com/v0id00/propq.git
cd propq
make build
# binary is in build/propq
```

### Shell completion

```bash
propq completion bash | sudo tee /etc/bash_completion.d/propq
propq completion zsh | sudo tee /usr/local/share/zsh/site-functions/_propq
```

## Features

- ⚡ **Async execution** — goroutine pool, per-server semaphores, default per-server mode (fast)
- 📥 **4 SQL sources** — inline (`--sql`), file (`-f`), pipe (stdin), editor (`-e`)
- 🎯 **Smart filtering** — regex on server names (`-s`), database names (`-d`), exclude (`-D`), tags (`-t`)
- 📊 **Multiple output formats** — table (human), `--json` (AI/scripts), `--csv` (Excel)
- 🛡️ **Safety first** — destructive SQL requires `--force`, no-filter warning, `--ask-for-commit`
- 🔄 **Retry** — `--retry N` with exponential backoff for flaky connections
- 📜 **Query history** — `--history` shows recent queries, editor template includes history
- 🔴 **Live streaming** — `--stream` prints results as they complete
- 📁 **File output** — `-o file` saves output to file (also prints to stdout)
- 🎛️ **Config-oriented** — all behaviour configurable in `propq.toml`, CLI overrides
- 📋 **Server list** — `propq servers` shows all servers with live DB counts
- 🧪 **Config check** — `propq config check` validates config and tests connections
- 🤖 **AI agent ready** — `--json` for structured output, `propq skill install` for agent skill

## Quick start

```bash
# Configure servers
cp propq.toml.example propq.toml
# edit propq.toml with your server details

# List servers
propq servers

# Check config
propq config check

# Run a query (default: per-server mode, fast)
propq -s local --sql "SELECT VERSION(), NOW()"

# Run on all databases
propq -s local -a -d "^test" --sql "SELECT COUNT(*) FROM users"

# Interactive editor + DB picker
propq -e -S

# Save results
propq --json -o results.json --sql "SELECT * FROM users"
```

## All flags

### SQL source (pick one)
| Flag | Description |
|------|-------------|
| `--sql QUERY` | Inline SQL |
| `-f, --file FILE` | Read SQL from file (`-` for stdin) |
| `-e, --edit` | Open `$EDITOR` to write SQL |

### Filtering
| Flag | Description |
|------|-------------|
| `-s, --server REGEX` | Server name regex filter |
| `-d, --dbfilter REGEX` | Database name regex filter (include) |
| `-D, --exclude-db REGEX` | Database name regex filter (exclude) |
| `-t, --tags TAGS` | Filter by tags (comma-separated, OR) |
| `-S, --select` | Open editor to pick databases interactively |

### Execution
| Flag | Description |
|------|-------------|
| `--timeout SEC` | Query timeout (default: 30) |
| `--concurrency N` | Concurrency limit (default: 5) |
| `--retry N` | Retry failed databases N times |
| `--force` | Allow destructive SQL |
| `--dry-run` | Preview targets without executing |
| `-a, --all` | Per-DB mode (default: once per server) |
| `--no-transaction` | Autocommit mode |
| `--ask-for-commit` | Show summary, ask before executing |

### Output
| Flag | Description |
|------|-------------|
| `--json` | JSON output |
| `--csv` | CSV output |
| `-o, --output FILE` | Save output to file |
| `--stream` | Print results live |
| `--history` | Show recent queries |
| `-N, --no-output` | Suppress results, show only summary |
| `--no-error` | Hide error entries |
| `--no-result` | Hide data rows, show only status |
| `-q, --quiet` | Suppress banners and progress |

### Config
| Flag | Description |
|------|-------------|
| `-c, --config FILE` | Config file path |

## Subcommands

| Command | Description |
|---------|-------------|
| `propq servers` | List servers with DB counts |
| `propq config check` | Validate config + test connections |
| `propq completion SHELL` | Generate shell completion |
| `propq skill install` | Install AI agent skill |

## Config file (`propq.toml`)

Search order: `-c PATH` → `./propq.toml` → `~/.config/propq/config.toml`

```toml
[defaults]
timeout = 30
concurrency = 5
force = false
dry_run = false
json = false
quiet = false
no_transaction = false
server = ""
dbfilter = ""
exclude_db = ""
confirm_without_filter = true
editor = "vim"

[connections.SERVER_NAME]
host = "db.example.com"
port = 3306
user = "myuser"
password = "mypassword"
max_connections = 3
tags = ["prod", "eu"]
```

## Examples

```bash
# Quick check (per-server mode, ~50ms per server)
propq -s "www1|www6|www7" --sql "SELECT VERSION()"

# Per-database with filter
propq -s www6 -a -d "^kwebticari_30" --sql "SELECT ps_no FROM tbpersonel LIMIT 5"

# Stream mode (results appear as they complete)
propq -s www6 -a -d "300" --sql "SELECT COUNT(*) FROM users" --stream

# CSV for Excel
propq -s local --csv --sql "SELECT id, name, email FROM users" > users.csv

# Save JSON to file
propq --json -o report.json --sql "SHOW TABLE STATUS" -s prod

# Ask before each database
propq -s www6 -a -d "300" --ask-for-commit --sql "DELETE FROM logs WHERE date < NOW() - INTERVAL 30 DAY"

# Retry flaky connections
propq -s "www1|www7" --retry 3 --sql "SELECT 1"

# Tags filter
propq -t prod --sql "SELECT COUNT(*) FROM users"

# Dry run + JSON (safe for CI/AI)
propq --json --dry-run --sql "DROP TABLE temp" -s prod

# Editor mode with query history
propq -e

# AI agent skill setup
propq skill install
```

## How it works

```
SQL → Scanner → Config → Runner → Display → stdout
                    ↓
               Filter (regex + tags)
                    ↓
               Async executor (goroutines + per-server semaphore)
                    ↓
               Result collection (with optional streaming)
```

1. **Scanner** reads SQL from argument, file, pipe, or editor (with history)
2. **Config** loads server connections from TOML (config-oriented)
3. **Filter** applies `-s` / `-d` / `-D` / `-t` regex and optional editor picker
4. **Runner** executes concurrently with per-server semaphores, retries on failure
5. **Display** formats results as table, JSON, or CSV

## Development

```bash
# Build
make build

# Cross-compile all platforms
make build-all

# Install to $GOPATH/bin
make install

# Run tests
make test

# Run with args
make run ARGS="--sql \"SELECT 1\" -s local"
```

## License

MIT
