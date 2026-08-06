# Security Policy

## Supported versions

Only the latest release is supported. Security fixes land on `main` and are shipped in the next release.

## Reporting a vulnerability

**Do not open a public issue for security problems.**

Report privately via the GitHub Security Advisory:

https://github.com/v0id00/propq/security/advisories/new

Please include:

- Affected version(s)
- Impact — what an attacker can do, and under what conditions
- Reproduction steps (config sketch, redacted credentials, command)
- Suggested fix, if you have one

You'll get an acknowledgement within a few days and a timeline for the fix and release.

## Security notes for users

- **Credentials**: `propq.toml` holds database passwords. Never commit it — the repo only ships `propq.toml.example` with placeholder values. Restrict file permissions (`chmod 600 propq.toml`).
- **Destructive SQL**: propq is a tool for running SQL against production databases by design. DELETE/DROP/TRUNCATE/ALTER and similar statements require `--force`; treat that flag as a loaded weapon. Always verify with `--dry-run` first, and use `--ask-for-commit` for multi-target runs.
- **Config from untrusted sources**: only load config files you control. propq does not execute config-driven code, but a malicious config can point at arbitrary hosts with arbitrary credentials.
