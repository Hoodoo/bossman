---
type: Architecture
title: Archive and Index
description: How sync copies agent session files into an append-only archive, how indexing decides what to reparse, and how the SQLite schema keeps derived data apart from user metadata.
tags: [archive, index, sqlite, sync]
verified:
  - by: owcli/ff31f70
    at: "2026-10-02T14:59:15.849Z"
sources:
  - id: openwiki-source-17506c01deef3bc65f2fb2fc
    resource: repo://internal/archive/archive.go
  - id: openwiki-source-4f22ab0c79d636fe0ca2b8b9
    resource: repo://internal/catalog/catalog.go
  - id: openwiki-source-316c7740ea0d2064293330cb
    resource: repo://internal/parse/codex.go
  - id: openwiki-source-7fabb846d2d38fede6287542
    resource: repo://internal/store/annotate.go
  - id: openwiki-source-6f70615716e7cd05fe8db472
    resource: repo://internal/store/query.go
  - id: openwiki-source-4a81fcd95533ed8ba5a77739
    resource: repo://internal/store/store.go
generated: { by: "owcli/ff31f70", at: "2026-10-02T15:04:05.474Z" }
---

# Archive and Index

`bossman sync` runs two passes: **archive** mirrors the agents' session directories into bossman's own directory, then **index** parses the archived sessions into SQLite. The archive is the source of truth. The index can always be rebuilt from it, and the user's annotations live in tables that indexing never touches. See [Architecture Overview](overview.md) for where these passes sit in the whole system.

## Archive layout and mappings

`Catalog.mappings` in `internal/catalog/catalog.go` lists what gets mirrored:

| Source | Archive destination |
| --- | --- |
| `ClaudeDir` (default `~/.claude/projects`) | `<home>/archive/claude/` |
| `<CodexDir>/sessions` | `<home>/archive/codex/sessions/` |
| `<CodexDir>/session_index.jsonl` | `<home>/archive/codex/session_index.jsonl` |
| `<CodexDir>/external_agent_session_imports.json` | `<home>/archive/codex/external_agent_session_imports.json` |

The archive mirrors each agent's own layout. As a result, `parse.DiscoverClaude` and `parse.DiscoverCodex` work unchanged on both the live directories and the archive, which `Catalog.Scan` relies on to compare them.

## Mirroring rules

`archive.Mirror` walks each mapping and calls `mirrorFile` for every regular file. A mapping whose source is missing is skipped silently, since the agent may simply not be installed. Per file:

- **New**: the file is copied.
- **Same size and mtime**: skipped as unchanged.
- **Grown**: the archived bytes are a prefix of the source, checked by SHA-256 of the first `len(archived)` bytes in `hasPrefix`. The file is copied over the archive copy. This is the normal case, because agent logs are append-only.
- **Rewritten or shrunk**: the old copy is renamed to `<name>.~<UTC timestamp>` (format `20060102T150405Z`) and the new content is copied. `archive.IsVersion` recognises these files, and discovery ignores them because their extension is no longer `.jsonl`.

`copyFile` writes to a temporary file in the destination directory, renames it into place, and copies the source mtime. An interrupted sync therefore never leaves a half-written log. The archive never deletes files. A session the agent has cleaned up stays in the archive and is reported as "kept only in the archive".

Errors on individual files are collected in `Stats.Errors`, and the pass continues.

## Indexing

`Catalog.Index(force)`:

1. Loads the pricing table (see [Metrics and Cost](../concepts/metrics.md)).
2. Discovers every session in the archive and loads `Store.Signatures()`.
3. For each session, builds a signature: the total size of all its files plus the latest mtime, stored in `src_size` and `src_mtime`. An unchanged signature is skipped unless `--force` is given. Otherwise the session is parsed and written with `Store.Put`.
4. Discovers the live agent directories and calls `Store.SetInSource`, so `in_source = 0` marks sessions the agents have deleted.

Parsing details per agent are in [Agent Log Formats and Parsing](../concepts/agent-logs.md).

## Schema: derived tables

`internal/store/store.go` creates the schema on open. The derived tables are:

- `sessions`: one row per session key (`agent:id`) with every metric, the cost and its source, `imported_from`, `in_source`, and the source signature.
- `session_models`: token usage and cost share per model.
- `session_tools`: calls and errors per tool.
- `session_summaries`: the agents' own titles, recaps, compaction summaries, and conclusions, in order.

`Store.Put` replaces a session's rows in one transaction: it deletes the per-session child rows and runs `INSERT OR REPLACE` on `sessions`. It carries the existing `in_source` value forward, so a reindex does not flip it. The `summary` column holds the best single summary. `latestSummary` prefers a conclusion, then the latest recap, then an old-style summary, then the latest compaction summary.

## Schema: user tables

`annotations` (display name, notes), `links`, and `tags` hold what the user wrote. Nothing derives them, and `Put` never touches them, so `bossman index --force` and even deleting the archive leave them intact. The operations are in `internal/store/annotate.go`:

- `SetDisplayName` and `SetNotes` upsert into `annotations`.
- `AddLink` requires an absolute URL. It rejects `javascript:`, `data:`, and `vbscript:`, and re-adding an existing URL updates its label.
- `NormalizeTag` lowercases tags and rejects ones containing commas or whitespace.
- `ExportAnnotations` and `ImportAnnotations` back `bossman meta export|import`. Import replaces names and notes and adds tags and links.

## Connection and concurrency

The database is opened with WAL journaling and a 10 s busy timeout, and `SetMaxOpenConns(1)` serialises all access through one connection. That is enough for one user's sessions and avoids writer contention between `serve`'s background sync and API writes.

## Aggregates skip empty imports

`Store.Stats` always sets `Filter.SkipCopies`. This drops Codex threads imported from Claude sessions that have no prompts of their own, so the same work is not counted twice. `List` does not skip them, so they still appear in listings.
