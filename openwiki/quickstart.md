---
type: Guide
title: Quickstart
description: What bossman is, how to build and run it, and which wiki page answers each common question.
tags: [quickstart, overview, navigation]
verified:
  - by: owcli/ff31f70
    at: "2026-10-02T15:04:02.525Z"
sources:
  - id: openwiki-source-0542b60281e3aea77c59392e
    resource: repo://docs/design.md
  - id: openwiki-source-4f22ab0c79d636fe0ca2b8b9
    resource: repo://internal/catalog/catalog.go
  - id: openwiki-source-da21f52d07ab623ce6a4f0a7
    resource: repo://internal/cli/cli.go
  - id: openwiki-source-624bec8caa72beb0cfc3a9ff
    resource: repo://internal/cli/serve.go
  - id: openwiki-source-012f2c78e3b1446dfc35803f
    resource: repo://Makefile
generated: { by: "owcli/ff31f70", at: "2026-10-02T15:04:05.474Z" }
---

# Quickstart

bossman keeps, browses, and measures local coding-agent sessions from Claude Code and Codex. It copies the agents' JSONL logs into an append-only archive (Claude Code deletes sessions after 30 days by default) and indexes them into SQLite. From there it reports cost, tokens, prompts, interventions, tool error rates, and time, from the CLI or a local web UI. Users can give sessions display names, tags, notes, and links, all stored in bossman only. It never writes to the agents' directories.

## Build and run

```sh
make build                 # bin/bossman (make install puts it in ~/.local/bin)
make check                 # go vet + go test
./bin/bossman sync         # archive ~/.claude/projects and ~/.codex, then index
./bin/bossman ls           # recent sessions
./bin/bossman show <id-prefix> --transcript
./bin/bossman stats -b project
./bin/bossman serve --open # web UI on http://127.0.0.1:7788/
```

To experiment without touching your real bossman data, set `BOSSMAN_HOME` to a scratch directory.

## Where to look

| If you need to… | Read |
| --- | --- |
| understand the packages and the data flow | [Architecture Overview](architecture/overview.md) |
| change how files are copied, or how indexing and storage work | [Archive and Index](architecture/archive-and-index.md) |
| fix or extend how Claude or Codex logs are read | [Agent Log Formats and Parsing](concepts/agent-logs.md) |
| understand or change a metric, the cost source, or pricing | [Metrics and Cost](concepts/metrics.md) |
| look up a command or flag | [CLI Reference](reference/cli.md) |
| work on `serve`, the JSON API, or the UI | [Web UI and API](architecture/web-ui.md) |
| configure paths, pricing, or scheduled syncs | [Configuration and Operations](operations/configuration.md) |
| run or extend tests, or add a new agent | [Testing and Extending](workflows/testing.md) |

## Source map

| Path | What lives there |
| --- | --- |
| `cmd/bossman/` | `main` |
| `internal/cli/` | cobra commands (`cli.go` root, `sync.go`, `browse.go`, `meta.go`, `serve.go`) |
| `internal/catalog/` | sync and index orchestration, pricing assignment, transcripts |
| `internal/archive/` | the append-only mirror |
| `internal/parse/` | discovery and parsers (`claude.go`, `codex.go`, `common.go`) |
| `internal/model/` | `Session`, `Usage`, `Event` types |
| `internal/pricing/` | `default.toml` and cost computation |
| `internal/store/` | SQLite schema, queries, annotations |
| `internal/web/` | HTTP server and the embedded `static/` UI |
| `internal/config/` | paths and settings |
| `docs/design.md` | design note with the reasoning behind formats and metrics |

## Key ideas

- **The archive is the source of truth.** Parsing reads only the archive, so sessions stay available after the agents delete their originals.
- **The index can be rebuilt; annotations cannot.** `bossman index --force` regenerates every derived table and never touches names, notes, tags, or links.
- **Agents' own numbers first.** Claude Code's recorded cost is preferred to the pricing table. Codex has no recorded cost and is unpriced until its models are added to `pricing.toml`.
- **Logs are tricky.** Claude repeats usage on every line of a message, Codex token counts are cumulative, and Codex can import Claude sessions. The parsers handle each case, and tests pin them down.
