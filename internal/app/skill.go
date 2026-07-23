// Package app — propq skill subcommand for AI agent setup.
package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// skillContent is the SKILL.md embedded in the binary.
// Updated with: propq skill install --update
var skillContent = `---
name: propq
description: "Async MySQL/MariaDB SQL executor — multi-server, concurrent, CLI-first. TOML config, regex filters, editor mode, JSON/CSV output."
tags: [mysql, mariadb, sql, database, cli, go, async, propq]
---

# propq CLI

Async SQL executor for MySQL/MariaDB. Runs queries across multiple servers concurrently.
Uses go-sql-driver/mysql with goroutine pool and per-server semaphores.

**Binary:** ` + "`propq`" + ` (Go ~10MB, single binary, no deps)

## AI Agent usage (ALWAYS use --json)

` + "```" + `bash
propq --json --sql "SELECT COUNT(*) FROM users" -s local          # structured output
propq servers --json                                               # list servers
propq --json --dry-run --sql "DROP TABLE x" -s local               # safe check
propq --json --csv --sql "SELECT * FROM users" -o users.csv        # export
propq --history                                                    # recent queries
propq --json -s "www1|www6" --sql "SELECT version()" --stream      # live streaming
` + "```" + `

## Core flags

### SQL source (pick one)
| Flag | Description |
|------|-------------|
| --sql QUERY | Inline SQL query |
| -f, --file FILE | Read SQL from file |
| -e, --edit | Open editor to write SQL |
| stdin pipe | echo 'SELECT 1' | propq |

### Filtering
| Flag | Description |
|------|-------------|
| -s, --server REGEX | Filter servers by name regex |
| -d, --dbfilter REGEX | Include DBs matching regex |
| -D, --exclude-db REGEX | Exclude DBs matching regex |
| -t, --tags TAGS | Filter by tags (comma-separated, OR) |
| -S, --select | Pick DBs interactively via editor |

### Execution
| Flag | Description |
|------|-------------|
| --timeout SEC | Query timeout (default: 30) |
| --concurrency N | Concurrency limit (default: 5) |
| --retry N | Retry failed DBs N times (exponential backoff) |
| --force | Allow destructive SQL (DROP/TRUNCATE/DELETE/ALTER) |
| --dry-run | Preview targets without executing |
| -a, --all | Per-DB mode (default: once per server) |
| --no-transaction | Disable transaction wrapping |
| --ask-for-commit | Show summary + ask before executing |

### Output
| Flag | Description |
|------|-------------|
| --json | JSON output (for AI/scripts) |
| --csv | CSV output (for Excel) |
| -o, --output FILE | Save output to file (stdout also printed) |
| --stream | Print results live as they complete |
| --history | Show recent queries |
| -N, --no-output | Suppress rows, show only summary |
| --no-error | Hide error entries |
| --no-result | Hide data rows, show only status |
| -q, --quiet | Suppress banners and progress |

### Config
| Flag | Description |
|------|-------------|
| -c, --config FILE | Config file path |

## Subcommands

| Command | Description |
|---------|-------------|
| propq servers [-s regex] | List servers with live DB counts |
| propq config check [-c FILE] | Validate config + test all connections |
| propq config validate [-c FILE] | Validate config file syntax |
| propq config generate [-o PATH] | Generate default config file |
| propq completion bash|zsh|fish|powershell | Generate shell completion |
| propq skill install | Install/update AI agent skill |

## Config file

Search: -c PATH -> ./propq.toml -> ~/.config/propq/config.toml (Linux/macOS) or %%APPDATA%%/propq/config.toml (Windows)

` + "```" + `toml
[defaults]
timeout = 30
concurrency = 5
confirm_without_filter = true
editor = "vim"

[connections.local]
host = "127.0.0.1"
port = 3306
user = "user"
password = "pass"
max_connections = 3
tags = ["dev"]
` + "```" + `

## Safety

- DROP/TRUNCATE/DELETE/ALTER/UPDATE require --force
- No DB filter → confirmation prompt (configurable)
- Default per-server mode (fast, one connection per server)
- --dry-run previews targets without side effects
- --ask-for-commit executes in transaction, asks before finalizing
- -d or -D automatically enables --all mode
`

func newSkillCmd() *cobra.Command {
	var updateMode bool

	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Manage AI agent skill for propq",
		Long: `Manage the AI agent skill file for propq.

The skill file teaches AI agents (like Hermes, Claude Code, etc.)
how to use propq effectively. Install it so agents can discover
and use propq in their workflows.`,
	}

	installCmd := &cobra.Command{
		Use:   "install",
		Short: "Install/update propq AI agent skill",
		Long: `Install or update the propq skill file for AI agents.

Copies the skill definition to the appropriate location so that
AI assistants can discover propq and use it correctly.

With --update, regenerates the embedded skill from the source
SKILL.md file before installing.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSkillInstall(updateMode)
		},
	}
	installCmd.Flags().BoolVarP(&updateMode, "update", "u", false, "Regenerate skill from source file")

	showCmd := &cobra.Command{
		Use:   "show",
		Short: "Show the propq skill content",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Println(skillContent)
			return nil
		},
	}

	cmd.AddCommand(installCmd, showCmd)
	return cmd
}

func runSkillInstall(updateMode bool) error {
	content := skillContent

	if updateMode {
		// Try to read from source repo
		sourcePaths := []string{
			"/home/webticari/.hermes/skills/software-development/propq/SKILL.md",
			"/home/webticari/Belgeler/propadb/.hermes/skills/software-development/propq/SKILL.md",
		}
		for _, p := range sourcePaths {
			data, err := os.ReadFile(p)
			if err == nil {
				content = string(data)
				break
			}
		}
		if content == skillContent {
			fmt.Fprintf(os.Stderr, "Note: source SKILL.md not found, using embedded version\n")
		}
	}

	// Determine target path
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("home dir: %w", err)
	}

	targetDir := filepath.Join(home, ".hermes", "skills", "software-development", "propq")
	targetFile := filepath.Join(targetDir, "SKILL.md")

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("create dir: %w", err)
	}

	if err := os.WriteFile(targetFile, []byte(content), 0644); err != nil {
		return fmt.Errorf("write skill: %w", err)
	}

	fmt.Fprintf(os.Stderr, "  ✓ propq skill installed to %s\n", targetFile)

	// Also try to ensure binary is in PATH
	if _, err := exec.LookPath("propq"); err != nil {
		fmt.Fprintf(os.Stderr, "  ⚠ propq not found in PATH. Install with: cd %s && go install ./cmd/propq/\n",
			"/home/webticari/Belgeler/propadb")
	} else {
		fmt.Fprintf(os.Stderr, "  ✓ propq binary found in PATH\n")
	}

	// Check if go/bin is in PATH
	path := os.Getenv("PATH")
	goBin := filepath.Join(home, "go", "bin")
	if !strings.Contains(path, goBin) {
		fmt.Fprintf(os.Stderr, "  ⚠ ~/go/bin not in PATH. Add: export PATH=\"$HOME/go/bin:$PATH\"\n")
	}

	return nil
}
