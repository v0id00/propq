# Contributing to propq

Thanks for helping out! propq is a small, focused tool — it executes SQL against many MySQL/MariaDB servers at once, often **production** ones. That shapes every rule below: correctness and safety matter more than speed.

## Before you start

1. **Search first.** Check [issues](https://github.com/v0id00/propq/issues) for an existing report or request before opening a new one.
2. **Ask before big changes.** If a feature changes CLI surface, config format, or output format, open an issue and discuss the design before writing code. Small fixes and docs are welcome directly.
3. **One concern per PR.** A PR that fixes a bug and refactors a package is two PRs.

## Reporting bugs

Open an issue with:

- **propq version** (`propq --version`) and OS
- **The exact command** you ran
- **Config** (redacted — no real passwords/hosts), or say which keys are set
- **Expected vs. actual output**, including the error
- SQL and target servers if relevant (sanitize data)

## Feature requests

Open an issue describing:

- The **problem** you're solving (not just the feature)
- How it fits propq's existing flags/config — propq prefers config-oriented design, CLI overrides
- Alternatives you considered

## Development setup

Requirements: Go 1.24+, `make`.

```bash
git clone https://github.com/v0id00/propq.git
cd propq
make build    # binary in build/propq
make test     # go test ./...
make lint     # requires golangci-lint
```

Project layout:

```
cmd/propq/    entrypoint
internal/app/ subcommands and glue
pkg/          public packages (config, runner, display, scanner, history)
```

## Code style

- `gofmt` clean, standard library first, existing patterns followed
- Keep the public `pkg/` API deliberate — anything there is importable by others
- Add or update tests for behavior changes (see `pkg/runner/spinner_test.go`)
- Comments in English, matching the codebase

## Commit conventions

Use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat: add --stream live output
fix: respect --no-pager when stdout is piped
docs: clarify --force behavior
refactor: move runner into pkg/
test: cover spinner ETA rendering
chore: bump go.mod deps
```

Imperative mood, lowercase scope when useful (`feat(display): ...`).

## Pull requests

1. Branch from `main`: `git checkout -b fix/spinner-overflow`
2. Make the change, run `make test` and `make lint` until green
3. Push and open a PR against `main` — the CI workflow (`test`) runs tests with a real MySQL 8.0 service container
4. Fill in the PR template; keep the description factual

## Safety checklist (important)

This tool talks to production databases by design. Before submitting:

- [ ] Destructive SQL (DELETE/DROP/TRUNCATE/ALTER) is covered by `--force` — never silently allowed
- [ ] No real credentials committed — config goes in `propq.toml.example` only, with placeholder values
- [ ] Defaults stay fail-closed: new risky behavior must default to *off*
- [ ] `--dry-run`, `--json`, and quiet modes documented if behavior changed
- [ ] README updated if flags, config keys, or output changed

## Code of conduct

All contributors are expected to follow the [Code of Conduct](CODE_OF_CONDUCT.md). Report unacceptable behavior through the channels listed there.
