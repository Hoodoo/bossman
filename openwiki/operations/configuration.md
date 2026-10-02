---
type: Guide
title: Configuration and Operations
description: Where bossman keeps its data, how configuration, environment variables, and the pricing file are resolved, and how to keep archiving running before the agents clean up.
tags: [configuration, operations, paths, cron]
verified:
  - by: owcli/ff31f70
    at: "2026-10-02T15:02:25.003Z"
sources:
  - id: openwiki-source-7bd911fdd3026b7b031a01e3
    resource: repo://go.mod
  - id: openwiki-source-da21f52d07ab623ce6a4f0a7
    resource: repo://internal/cli/cli.go
  - id: openwiki-source-624bec8caa72beb0cfc3a9ff
    resource: repo://internal/cli/serve.go
  - id: openwiki-source-6c06034f3c69a4e3631c4874
    resource: repo://internal/cli/sync.go
  - id: openwiki-source-a8910515ddd14810ad43f5c1
    resource: repo://internal/config/config.go
  - id: openwiki-source-4a81fcd95533ed8ba5a77739
    resource: repo://internal/store/store.go
  - id: openwiki-source-012f2c78e3b1446dfc35803f
    resource: repo://Makefile
generated: { by: "owcli/ff31f70", at: "2026-10-02T15:04:05.474Z" }
---

# Configuration and Operations

bossman needs no configuration to start: it reads the agents' default directories and writes to an XDG data directory. `config.Load` in `internal/config/config.go` resolves everything below. `bossman paths` prints the effective locations.

## The bossman home

The home directory holds everything bossman writes. It is resolved in this order:

1. The `--home` flag (on every command).
2. The `BOSSMAN_HOME` environment variable.
3. `$XDG_DATA_HOME/bossman`, else `~/.local/share/bossman`.

Inside it:

| Path | Contents |
| --- | --- |
| `archive/` | Verbatim copies of agent files; see [Archive and Index](../architecture/archive-and-index.md) |
| `bossman.db` | SQLite: the derived index and the user annotations (WAL mode, so `-wal` and `-shm` files appear alongside) |
| `config.toml` | Optional settings |
| `pricing.toml` | Optional pricing table that replaces the built-in one |

Back up the archive and the database together. `bossman meta export` is a small JSON backup of names, notes, tags, and links alone, which `meta import` merges back.

## config.toml and environment

`config.toml` accepts three keys, decoded with BurntSushi/toml. A missing file is fine.

```toml
claude_dir = "~/.claude/projects"   # Claude Code projects directory
codex_dir = "~/.codex"              # Codex home (sessions/ and index files)
idle_minutes = 5                    # active-time gap cap
```

`BOSSMAN_CLAUDE_DIR` and `BOSSMAN_CODEX_DIR` override the file. A leading `~/` is expanded. A non-positive `idle_minutes` falls back to 5. The tests use these environment variables to point bossman at fixture directories.

## Pricing

`bossman prices` shows the effective table. `bossman prices --init` writes the built-in table to `<home>/pricing.toml` and refuses to overwrite an existing file. Edit that file to add Codex models or to change rates, then run `bossman index --force` to reprice. Rates are USD per million tokens. Keys match model ids by longest prefix. See [Metrics and Cost](../concepts/metrics.md).

## Keeping the archive current

Archiving only helps if it runs before the agents delete their files. Claude Code removes sessions older than `cleanupPeriodDays` (30 by default). Either:

- run `bossman sync` from cron or a systemd timer, e.g. hourly:
  `17 * * * * $HOME/.local/bin/bossman sync >/dev/null`, or
- leave `bossman serve --sync-every 30m` running.

`sync` is incremental. Unchanged files are skipped by size and mtime, and unchanged sessions by their signature, so frequent runs are cheap. A full first sync of about 180 MB of logs took a few seconds in development. `bossman scan` shows what the next sync would pick up without copying anything, plus how many sessions now survive only in the archive.

## Install and build

`make install` builds with the git-described version and installs to `~/.local/bin/bossman` (override with `PREFIX` or `BINDIR`). The binary is self-contained: the UI and default pricing are embedded, and SQLite is the pure-Go `modernc.org/sqlite`, so no cgo or runtime dependencies are needed.

## Recovering

- **Wrong numbers after a parser change:** `bossman index --force` reparses every archived session. Annotations are untouched.
- **Corrupt or lost database:** delete `bossman.db*` and run `bossman index`. The index is rebuilt from the archive, but names, notes, tags, and links are lost unless you exported them.
- **A source file was rewritten:** the archive keeps the previous copy as `<name>.~<timestamp>`. Inspect it by hand if needed. bossman indexes only the current file.
