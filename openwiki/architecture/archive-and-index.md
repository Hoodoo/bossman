---
type: Architecture
title: Archive and Index
description: How sync copies agent session files into an append-only archive, how indexing decides what to reparse, and how the SQLite schema keeps derived data apart from user metadata.
tags: [archive, index, sqlite, sync]
verified:
  - by: owcli/v0.4.0
    at: "2026-10-09T15:57:19.417Z"
sources:
  - id: openwiki-source-17506c01deef3bc65f2fb2fc
    resource: repo://internal/archive/archive.go
  - id: openwiki-source-4f22ab0c79d636fe0ca2b8b9
    resource: repo://internal/catalog/catalog.go
  - id: openwiki-source-40f02473f25984711e4a2563
    resource: repo://internal/cli/browse.go
  - id: openwiki-source-316c7740ea0d2064293330cb
    resource: repo://internal/parse/codex.go
  - id: openwiki-source-7fabb846d2d38fede6287542
    resource: repo://internal/store/annotate.go
  - id: openwiki-source-6f70615716e7cd05fe8db472
    resource: repo://internal/store/query.go
  - id: openwiki-source-4a81fcd95533ed8ba5a77739
    resource: repo://internal/store/store.go
  - id: openwiki-source-6dbe79f2b1613ac94797fd56
    resource: repo://internal/web/web.go
generated: { by: "owcli/v0.4.0", at: "2026-10-09T15:57:59.118Z" }
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

`annotations` (display name, notes, project override), `links`, and `tags` hold what the user wrote. Nothing derives them, and `Put` never touches them, so `bossman index --force` and even deleting the archive leave them intact. The operations are in `internal/store/annotate.go`:

- `SetDisplayName` and `SetNotes` upsert into `annotations`.
- `SetProjectOverride` upserts `annotations.project_override`, trimming the value; a nil project stores NULL, which returns the session to the project the agent recorded.
- `AddLink` requires an absolute URL. It rejects `javascript:`, `data:`, and `vbscript:`, and re-adding an existing URL updates its label.
- `NormalizeTag` lowercases tags and rejects ones containing commas or whitespace.
- `ExportAnnotations` and `ImportAnnotations` back `bossman meta export|import`. Export includes the override as `project`. Import replaces names and notes, sets the override when `project` is present, and adds tags and links.

## Tag filters and sinks

`Filter.Tag` keeps sessions with a tag; `Filter.NotTag` drops sessions that carry one. Both compare against the lowercased tags `NormalizeTag` stores, and `NotTag` is lowercased before the query. A trailing `*` turns `NotTag` into a prefix match (`t.tag LIKE <prefix>%`), with `%`, `_`, and `\` in the prefix escaped by `escapeLike` so only the `*` acts as a wildcard.

A **sink** is the tag convention `sink:<name>` for sessions that belong to no project. The store knows nothing special about it: `NotTag: "sink:*"` hides every sink, and nothing applies that filter by default except the web list (see [Web UI and API](web-ui.md#front-end)). The stats page and `bossman stats` pass no `NotTag` unless asked, so sinks' cost is still counted.

## Project override

`sessions.project` is what the agent recorded. A NULL `annotations.project_override` means "use it"; any other value replaces it. Every query reads `COALESCE(a.project_override, s.project)`: the listed project in `rowColumns`, the `project` sort key, the `Project` filter, and `Stats` grouped by project. Overriding a session therefore moves it in listings, filters, facets, and aggregates alike. `Store.Get` also returns the agent's value as `detected_project` and sets `project_overridden` when an override exists, so the UI can offer a reset.

`CREATE TABLE IF NOT EXISTS` does not add columns to an existing database, so `Open` also runs `ALTER TABLE annotations ADD COLUMN project_override TEXT` and ignores only the "duplicate column" error. That is the catalogue's one in-place migration.

## Connection and concurrency

The database is opened with WAL journaling and a 10 s busy timeout, and `SetMaxOpenConns(1)` serialises all access through one connection. That is enough for one user's sessions and avoids writer contention between `serve`'s background sync and API writes.

## Empty imports are skipped

`Filter.SkipCopies` drops Codex threads imported from Claude sessions that have no prompts of their own (`imported_from != '' AND prompts = 0`). `Store.Stats` always sets it, so the same work is not counted twice. `List` applies it only when the caller asks, and both callers ask by default: `bossman ls` unless `--copies` is given, and `GET /api/sessions` unless `copies=1`. A session hidden from a listing is therefore exactly one the aggregates leave out, while a copy the user typed into in Codex stays visible and counted.
