---
type: Concept
title: Agent Log Formats and Parsing
description: What Claude Code and Codex write to disk, and the rules bossman's parsers apply to turn those logs into accurate per-session metrics.
tags: [parsing, claude-code, codex, jsonl]
verified:
  - by: owcli/ff31f70
    at: "2026-10-02T15:01:19.902Z"
sources:
  - id: openwiki-source-e8dc539cc499715db5d6b44f
    resource: repo://internal/parse/claude.go
  - id: openwiki-source-316c7740ea0d2064293330cb
    resource: repo://internal/parse/codex.go
  - id: openwiki-source-0a07e754760ec9f5cbce7d6c
    resource: repo://internal/parse/common.go
generated: { by: "owcli/ff31f70", at: "2026-10-02T15:04:05.474Z" }
---

# Agent Log Formats and Parsing

`internal/parse` turns each agent's JSONL logs into a `model.Session`, which carries the metrics, and optionally a transcript of `model.Event`s. Each agent has a discovery function and a parser. Neither agent documents its log format, so every rule below comes from real logs, and the fixtures in `internal/parse/*_test.go` reproduce the observed shapes. [Metrics and Cost](metrics.md) explains how the parsed numbers are reported.

Shared helpers in `common.go`:

- `eachLine` reads lines of any length with a buffered reader instead of `bufio.Scanner`'s line limit. Tool output can make single lines very long. A truncated last line, left by an agent mid-write, simply fails to decode and is skipped.
- `clock` collects event times and agent turn intervals for active time.
- `catalogueSummary` extracts the away-summary from the conclusion marker.

## Claude Code: discovery

`DiscoverClaude(root)` scans `<root>/<project dir>/*.jsonl`. The project directory name is the cwd with `/` replaced by `-`, and each `.jsonl` file is one session whose id is the file name. The optional `<id>/` sidecar directory belongs to the session, and every file in it is listed. It holds `subagents/agent-*.jsonl` transcripts (with `.meta.json`) and `tool-results/*.txt`. Directories such as `memory/` contain no `.jsonl` at the top level and are ignored.

## Claude Code: parsing rules

`ParseClaude` reads the main log, then every `subagents/*.jsonl`, counting each as a subagent.

- **Usage is deduplicated per message id.** Claude Code writes an assistant message as one line per content block, and each line repeats the full `message.usage`. Usage is stored in a map keyed by `message.id` (last line wins) and summed only in `finish`. Summing per line would roughly double the tokens. Messages from the `<synthetic>` model (API-error placeholders) are ignored.
- **Cache writes are split** into `ephemeral_5m_input_tokens` and `ephemeral_1h_input_tokens` when present. Otherwise all of `cache_creation_input_tokens` counts as 5-minute.
- **Human prompts** are `user` lines that meet all of these conditions:
  - not `isMeta`, `isSidechain`, or `isCompactSummary`;
  - containing no `tool_result`;
  - with `origin.kind` either absent or `human`, so a `task-notification` origin does not count.

  `humanPrompt` additionally rejects injected text starting with `<local-command-`, `<task-notification>`, `<system-reminder>`, or the local-command caveat. Slash commands (`<command-name>/x</command-name>` plus `<command-args>`) count, rendered as `/x args`.
- **Interrupts** are user text containing `[Request interrupted by user`.
- **Rejections** are tool results containing "The user doesn't want to proceed with this tool use". They count as interventions and are not tool errors, even though Claude marks them `is_error`.
- **Tool errors** are `tool_result` blocks with `is_error`, attributed to a tool through the `tool_use` id. Tool calls are deduplicated by `tool_use` id.
- **System lines** (`subtype`):
  - `turn_duration` adds agent working time (`durationMs`, stamped at the turn's end) and a turn interval for active time.
  - `away_summary` is a recap, with the trailing "(disable recaps in /config)" stripped.
  - `compact_boundary` counts a compaction.
  - `api_error` counts an API error. Assistant lines with `isApiErrorMessage` count as API errors too.
- **Titles:** `custom-title` (the user's `/rename`) beats the last `ai-title`. Older logs have `summary` lines, kept as summaries.
- **Cost:** the last `cost-state.totalCostUSD` becomes `ReportedCostUSD`, Claude Code's own figure.
- **Sidechain and subagent lines** add usage, tool calls, and errors, but never prompts, interrupts, or time.

## Codex: discovery and side files

`DiscoverCodex(root)` walks `<root>/sessions/**/rollout-*.jsonl` and takes the session id from the UUID at the end of the file name. `LoadCodexMeta(root)` reads two side files:

- `session_index.jsonl`, to which Codex appends `{id, thread_name}` on every rename. The last entry wins and becomes the title.
- `external_agent_session_imports.json`, which lists Codex threads imported from Claude Code sessions (Codex Desktop can do this). Each record maps the thread id to its source path, title, and `imported_at` time. A source under `/.claude/` becomes `imported_from = claude:<id>`.

Both files are archived alongside the sessions (`CodexMetaFiles`).

## Codex: parsing rules

Each line is `{timestamp, type, payload}`.

- **Metadata:** `session_meta` gives the cwd, CLI version, originator, and git branch. `turn_context` sets the model in effect.
- **Tokens are cumulative.** `event_msg/token_count.info.total_token_usage` is a running total. `tokens()` attributes each increase to the current model, treats a decrease as a counter reset, and ignores repeats. OpenAI `input_tokens` include `cached_input_tokens`, so uncached input = input − cached − cache_write.
- **Prompts** appear as `event_msg/user_message` in older versions and also as `item_completed/UserMessage` in newer ones. The parser collects both and uses only one source: the item records if any exist, otherwise the events. `response_item` messages with role `user` carry injected context (environment, AGENTS.md) and are ignored.
- **Interrupts:** `turn_aborted` with reason `interrupted`. Both `task_complete` and `turn_aborted` add `duration_ms` of agent time and a turn interval.
- **Compactions:** `compacted` lines.
- **API errors:** `event_msg` of type `error` or `stream_error`.
- **MCP tool calls:** `item_completed/McpToolCall` items, named `mcp:<server>/<tool>`, which are errors when `status` is `failed`.

## Codex: tool calls and errors

Shell commands run through wrapper tools: `exec_command` and `shell` in older versions, and the code-mode `exec` in newer ones, which runs a script of many commands. A script's own output reads "Script completed" even when commands inside it failed. Newer Codex logs every command it ran as `item_completed/CommandExecution` with a `status`, and those records are the better measure.

While parsing, wrapper calls are tallied under reserved names: a `wrapperPrefix`, and `commandRecords` for the command items. `finish` then reconciles them:

- If any `CommandExecution` records exist, they become the `command` tool, and the wrapper tallies are dropped.
- Otherwise the wrappers are restored under their real names.

For outputs, `codexFailed` checks the first 400 characters:

- "aborted by user" is a rejection.
- "Script failed" or "verification failed" (from `apply_patch`) is an error.
- Otherwise the first exit code matching `exited with code N`, `Exit code: N`, or `"exit_code":N` decides, and non-zero is an error.

On real sessions, the wrapper outputs showed 0 errors where Codex's command records showed 9 failures out of 97 commands.

## Codex: imported threads

An imported rollout starts with a copy of the Claude conversation, all stamped at import time. When a session is in the imports file, every line stamped no later than `imported_at + 2 minutes` (`importGrace`) is parsed into a scratch session that is then discarded. Those lines still appear in the transcript, but they add no prompts, tokens, tools, or time. Only work done in Codex afterwards counts. The first copied prompt is kept as `FirstPrompt` for display when nothing was typed afterwards.

## Conclusion marker

`CatalogueMarker` is `<!-- cc-catalogue:session-concluded`, the HTML-comment form written by the session-catalogue-close skill. A bare mention of the marker string does not count. When an assistant text contains the marker, the session is `Concluded`, and the indented `away-summary: |` block becomes a `conclusion` summary. A human prompt stamped after the marker clears `Concluded`, because the session went on. Both parsers apply this rule.

## Transcripts

With `events=true`, both parsers also emit `model.Event`s (user, assistant, tool, result, system), sorted by time. Tool inputs are condensed to the most telling field (command, file path, pattern, URL, …). Subagent events carry `Sidechain`. `bossman show --transcript` and the UI's transcript panel call this through `Catalog.Transcript`.
