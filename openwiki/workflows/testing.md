---
type: Workflow
title: Testing and Extending
description: How the test suite is organized, how fixtures reproduce real agent log shapes, how to check changes against real data, and how to add support for another agent.
tags: [testing, fixtures, extending, adapters]
verified:
  - by: owcli/v0.3.0
    at: "2026-10-05T09:11:03.303Z"
sources:
  - id: openwiki-source-4f22ab0c79d636fe0ca2b8b9
    resource: repo://internal/catalog/catalog.go
  - id: openwiki-source-62c7ded5341e2014f256fe46
    resource: repo://internal/catalog/catalog_test.go
  - id: openwiki-source-c04b809e997b6644e3320acf
    resource: repo://internal/model/model.go
  - id: openwiki-source-d787c32a588ea9f3a15d1244
    resource: repo://internal/parse/claude_test.go
  - id: openwiki-source-398ec2ac7812c24b1f68756a
    resource: repo://internal/parse/codex_test.go
  - id: openwiki-source-44a1f87e0577b42841944fde
    resource: repo://internal/pricing/pricing_test.go
  - id: openwiki-source-eb4688fc0fba2b62687b137d
    resource: repo://internal/web/web_test.go
  - id: openwiki-source-012f2c78e3b1446dfc35803f
    resource: repo://Makefile
generated: { by: "owcli/v0.3.0", at: "2026-10-05T09:11:03.488Z" }
---

# Testing and Extending

`make check` runs `go vet ./...` and `go test ./...`. The tests are fast and self-contained: they write fixtures to `t.TempDir()`, point bossman at them through `BOSSMAN_CLAUDE_DIR`, `BOSSMAN_CODEX_DIR`, or an explicit home, and never touch `~/.claude`, `~/.codex`, or the real bossman home.

## What is covered

| Package | Tests | What they pin down |
| --- | --- | --- |
| `parse` | `TestParseClaude`, `TestDiscoverClaude`, `TestClaudeConclusion`, `TestHumanPrompt` | Usage counted once per message id; prompts vs. injected text; interrupts, rejections, and errors; recap footer stripped; subagents; reported cost; active time; conclusion marker (and its cancellation by a later prompt) |
| `parse` | `TestParseCodexLegacy`, `TestParseCodexCommandRecords`, `TestParseCodexImport`, `TestParseCodexImportResync`, `TestCodexFailed` | Cumulative tokens split per model; prompts counted from one source; command records replacing wrappers; exit-code classification; Claude copies counting only work done in Codex, including work between two import syncs |
| `parse` | `TestClockActive`, `TestCatalogueSummary` | Active time as a union with agent turns; marker parsing |
| `archive` | `TestMirror`, `TestMirrorSingleFile` | New, grown, rewritten (kept as a version), and deleted-at-source files |
| `store` | `TestAnnotationsSurviveReindex`, `TestResolve`, `TestListAndStats` | Annotations untouched by `Put`; export/import round trip; reference resolution; filters; aggregates skip empty imports |
| `pricing` | `TestDefaultTable`, `TestOpusCostMatchesAgent`, `TestSessionCostSources`, `TestUserTable` | Prefix lookup; the table reproduces a real Claude-recorded cost; cost sources; user override |
| `catalog` | `TestSyncArchivesAndSurvivesDeletion`, `TestSessionGrowthIsReindexed` | End-to-end sync, incremental reindex, sessions surviving the agent's deletion, transcripts from the archive |
| `web` | `TestAPI`, `TestRequestGuards`, `TestBehindProxy` | Every route, the host, origin, and content-type guards, and running behind a proxy with a trusted user header |

The UI JavaScript has no automated tests. See [Web UI and API](../architecture/web-ui.md#changing-the-ui).

## Fixtures mirror real logs

The parser fixtures (`claudeMain`, `claudeSubagent`, `codexLegacy`, `codexCommands`, and the import rollout) are hand-written JSONL in the exact shapes observed in real Claude Code 2.1.28x and Codex 0.130–0.156 logs. They include the awkward cases: the same assistant message on two lines, `<synthetic>` API-error messages, injected caveats and task notifications, a truncated final line, duplicated token counts, and prompts logged twice. When an agent changes its format, add the new shape to these fixtures before changing the parser. `TestOpusCostMatchesAgent` uses token counts from a real session whose `cost-state` recorded $41.70.

## Checking against real data

Unit tests cannot catch a format the fixtures do not know about. After parser or metric changes, index real logs into a throwaway home:

```sh
make build
BOSSMAN_HOME=$(mktemp -d) ./bin/bossman sync
BOSSMAN_HOME=… ./bin/bossman ls -n 0
BOSSMAN_HOME=… ./bin/bossman stats -b tool
```

Useful cross-checks used during development:

- **Claude cost:** compare `cost_usd` with `table_cost_usd` (`ls --json`) for sessions whose source is `agent`. They should be within a few percent.
- **Codex errors:** count `item_completed` `CommandExecution` items with status `failed` in a rollout using `jq`, and compare with `show <id>`.
- **Sessions with zero tokens** usually mean an unrecognised shape, or a Claude copy made by Codex Desktop's import sync (`ls` flags those as `[Claude copy]`).

## Adding an agent

An agent adapter is a discovery function and a parser in `internal/parse`. To add one:

1. Collect real sample logs and note where the agent records tokens, prompts, tool calls and results, errors, interrupts, timing, titles, and any cost.
2. Add an agent constant in `internal/model` and write `Discover<Agent>(root) []Candidate`. Every file of a session goes in `Files`, so the signature and archive cover it.
3. Write `Parse<Agent>` returning a `model.Session` with `Usage` normalised to bossman's meaning (input excludes cached tokens; output includes reasoning), and transcript events when asked. Reuse `eachLine`, `clock`, and `catalogueSummary`.
4. Add an archive mapping in `Catalog.mappings`, discovery in `Catalog.discover`, and a case in `Catalog.parseEvents`. Add a config key and environment variable if the agent has a home directory.
5. Add pricing entries if the agent records no cost, and fixtures and tests in the style of `codex_test.go`.

Background on the existing adapters is in [Agent Log Formats and Parsing](../concepts/agent-logs.md), and on where they plug in, in [Architecture Overview](../architecture/overview.md).
