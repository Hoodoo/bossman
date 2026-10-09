---
type: Reference
title: CLI Reference
description: Every bossman command and flag, how sessions are referenced, the filter and time syntax, and how output is formatted.
tags: [cli, reference, commands]
verified:
  - by: owcli/v0.4.0
    at: "2026-10-09T15:57:58.995Z"
sources:
  - id: openwiki-source-40f02473f25984711e4a2563
    resource: repo://internal/cli/browse.go
  - id: openwiki-source-da21f52d07ab623ce6a4f0a7
    resource: repo://internal/cli/cli.go
  - id: openwiki-source-7927adc9a1bf9dfedf6b2fc8
    resource: repo://internal/cli/meta.go
  - id: openwiki-source-6c06034f3c69a4e3631c4874
    resource: repo://internal/cli/sync.go
  - id: openwiki-source-7fabb846d2d38fede6287542
    resource: repo://internal/store/annotate.go
  - id: openwiki-source-6f70615716e7cd05fe8db472
    resource: repo://internal/store/query.go
  - id: openwiki-source-4a81fcd95533ed8ba5a77739
    resource: repo://internal/store/store.go
generated: { by: "owcli/v0.4.0", at: "2026-10-09T15:57:59.118Z" }
---

# CLI Reference

The CLI is built with cobra in `internal/cli`. Each command opens a `catalog.Catalog` through `app.withCatalog`, does its work, and closes it. Errors are printed as `bossman: <error>` and exit with status 1.

## Global flags

| Flag | Effect |
| --- | --- |
| `--home <dir>` | Use another bossman home; see [Configuration and Operations](../operations/configuration.md) |
| `--json` | Print JSON instead of tables (supported by every command that prints data) |
| `--version` | Print the build version |

## Session references

Commands that take `<session>` resolve it with `store.Resolve`, trying in order:

1. An exact key (`claude:<id>`) or exact id.
2. A unique prefix of the id or key, at least four characters long.
3. An exact display name.

An ambiguous reference fails and lists up to five matches. Codex ids are time-ordered UUIDs, so sessions started together share long prefixes. `ls` therefore prints the shortest id prefix (at least 8 characters) that tells the listed rows apart (`shortIDWidth`).

## Archiving and indexing

| Command | What it does |
| --- | --- |
| `sync [--force]` | Archive, then index. `--force` reparses every session. |
| `archive` | Copy new and grown files into the archive only. |
| `index [--force]` | Index the archive only. `--force` rebuilds the derived tables; annotations are kept. |
| `scan [--all]` | Read-only comparison of the agents' directories with the archive: sessions that are `new`, `changed`, or (with `--all`) already `archived`, plus a count of sessions kept only in the archive. |

See [Archive and Index](../architecture/archive-and-index.md) for what these do internally.

## Browsing

### Filters

`ls` and `stats` accept the same filters:

| Flag | Effect |
| --- | --- |
| `--agent claude\|codex` | Only one agent |
| `-p, --project <substr>` | Project path contains this (the project override when one is set) |
| `-s, --search <substr>` | Matches title, display name, first prompt, summary, notes, or key |
| `-t, --tag <tag>` | Has this tag |
| `--not-tag <tag>` | Does not have this tag; a trailing `*` matches a prefix, so `--not-tag 'sink:*'` leaves out every sink |
| `--since`, `--until <when>` | Start time bounds |
| `--archived-only` | Only sessions the agent has deleted |

`<when>` is a date (`2026-09-01`, local time), an RFC 3339 time, or a duration back from now: `36h`, `7d`, or `2w` (`parseWhen`).

### ls

`ls` lists sessions. Its own flags are `--sort` (`started` by default; also `ended`, `cost`, `wall`, `active`, `prompts`, `interventions`, `tools`, `errors`, `tokens`, `output`, `project`, `name`), `--asc`, `-n/--limit` (30 by default; 0 means all), and `--copies`.

By default `ls` hides Claude copies that have no prompts of their own, the same sessions `stats` never counts; `--copies` lists them too. A copy someone typed into in Codex is always listed. The flag exists only on `ls`, because `stats` excludes these sessions regardless. Consumers of `ls --json`, such as goatlassian, see the same default.

Each row shows the start time, agent, project, cost, tokens, prompts, error rate, active time, and name. The name is the display name, else the agent's title, else the first prompt. Flags such as `[archived]`, `[concluded]`, and `[Claude copy]` (a Codex thread that Codex Desktop copied from a Claude session) and the tags follow the name. Cost is `-` when the session has no usage, `?` when its usage could not be priced, and ends in `+` when partially priced. `show` names the source of a copy on its `copy of` line.

### show

`show <session> [--transcript [--full]]` prints all metrics, the cost with its source (and the pricing-table estimate when the agent recorded its own cost), links, notes, the first prompt, the agents' summaries, and model and tool tables. `--transcript` adds the conversation, with messages shortened unless `--full` is given.

### stats

`stats [-b agent|project|model|tool|day|week|month]` aggregates by one dimension (`project` by default). Session dimensions print sessions, cost, tokens, prompts, interventions, tool calls, error rate, and active and wall time, followed by a total row. `model` prints token columns and cost. `tool` prints calls, errors, and error rate. See [Metrics and Cost](../concepts/metrics.md).

## Your metadata

| Command | Effect |
| --- | --- |
| `name <session> <name…>` | Set the display name; `-` clears it |
| `note <session> [text…\|-] [-a]` | With no text, print the notes. `-` reads stdin; `-a` appends a line |
| `tag <session> [tag…] [--rm tag]` | Add tags, remove them with `--rm` (repeatable or comma-separated); with neither, list them |
| `link add <session> <url> [-l label]` | Attach a URL. Re-adding updates the label |
| `link rm <session> <id\|url>` | Detach a link |
| `link ls <session>` | List links |
| `meta export` | Print all annotations as JSON, including any project override as `project` |
| `meta import <file\|->` | Merge exported annotations |

These write only the annotation tables, never the agents' files. A session's project override has no CLI command; set it from the session page or `PUT /api/sessions/{key}/project` (see [Web UI and API](../architecture/web-ui.md)), and move it between machines with `meta export|import`.

## Other commands

| Command | Effect |
| --- | --- |
| `serve [--addr host:port] [--open] [--sync-every 30m] [--no-sync]` | Web UI; see [Web UI and API](../architecture/web-ui.md) |
| `prices [--init]` | Show the pricing table, or write it to `<home>/pricing.toml` for editing |
| `paths` | Print the home, database, archive, config, pricing, and source paths |
