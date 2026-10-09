---
type: Concept
title: Metrics and Cost
description: How bossman defines and computes each reported metric, from cost and its source through tokens, interventions, error rates, time, and aggregates.
tags: [metrics, cost, pricing, analytics]
verified:
  - by: owcli/v0.4.0
    at: "2026-10-09T15:47:50.104Z"
sources:
  - id: openwiki-source-4f22ab0c79d636fe0ca2b8b9
    resource: repo://internal/catalog/catalog.go
  - id: openwiki-source-40f02473f25984711e4a2563
    resource: repo://internal/cli/browse.go
  - id: openwiki-source-a8910515ddd14810ad43f5c1
    resource: repo://internal/config/config.go
  - id: openwiki-source-c04b809e997b6644e3320acf
    resource: repo://internal/model/model.go
  - id: openwiki-source-0a07e754760ec9f5cbce7d6c
    resource: repo://internal/parse/common.go
  - id: openwiki-source-195312e0d31b6667fb74eb0a
    resource: repo://internal/pricing/default.toml
  - id: openwiki-source-d00cad2416dd34b5ab641a43
    resource: repo://internal/pricing/pricing.go
  - id: openwiki-source-44a1f87e0577b42841944fde
    resource: repo://internal/pricing/pricing_test.go
  - id: openwiki-source-6f70615716e7cd05fe8db472
    resource: repo://internal/store/query.go
  - id: openwiki-source-213e0022dfc208535b4c26a9
    resource: repo://internal/web/static/app.js
  - id: openwiki-source-6dbe79f2b1613ac94797fd56
    resource: repo://internal/web/web.go
generated: { by: "owcli/v0.4.0", at: "2026-10-09T15:48:40.390Z" }
---

# Metrics and Cost

Every number bossman reports comes from a `model.Session` built by the parsers (see [Agent Log Formats and Parsing](agent-logs.md)). The session is priced in `catalog.Price` and stored in the `sessions` row and its child tables. This page defines each metric and the reasoning behind it.

## Tokens

`model.Usage` has the same meaning for both agents:

| Field | Meaning |
| --- | --- |
| `Input` | uncached input tokens (Codex: input − cached − cache-write) |
| `CacheWrite5m`, `CacheWrite1h` | tokens written to the prompt cache, by TTL (Claude) |
| `CacheRead` | tokens read from the cache (Codex: "cached input") |
| `Output` | output tokens, including reasoning |
| `Reasoning` | the reasoning or thinking share of `Output` |
| `Requests` | API responses (Claude messages; Codex token-count increments) |

Usage is kept per model in `session_models` and summed into the `sessions` row. Listings show "tokens" as input + cache write + cache read + output.

## Cost and its source

`pricing.Table.SessionCost` returns a cost and a **source**:

| Source | When |
| --- | --- |
| `agent` | The agent recorded its own cost (Claude Code `cost-state`). That figure is reported. |
| `table` | Every model with usage is in the pricing table. |
| `partial` | Some models are missing from the table. The sum covers only the priced ones, and listings show a trailing `+`. |
| `none` | Nothing could be priced. Listings show `—` or `?`. |

The table estimate is always stored as well (`table_cost_usd`), so `show` can print both. On real sessions the table comes in 3–8% below Claude's own figure. A likely reason is calls Claude does not log per message: `cost-state.modelUsage` lists a haiku model that never appears in assistant lines. When the source is `agent`, `catalog.Price` scales each model's table cost proportionally so the per-model costs add up to the agent's total. Model breakdowns therefore stay consistent with session totals.

## The pricing table

`internal/pricing/default.toml` is embedded in the binary and lists USD per million tokens for current Claude models. `Lookup` picks the **longest key that is a prefix** of the model id, so `claude-haiku-4-5` also prices `claude-haiku-4-5-20251001`, and `claude-sonnet-5-5` is not priced as `claude-sonnet-5`. Cache writes default to 1.25× input for 5-minute writes and 2× for 1-hour writes, unless `cache_write_5m` or `cache_write_1h` is set.

Codex records no cost, so Codex sessions are always priced from the table. It includes OpenAI list prices for the models Codex uses: `gpt-5.6-sol`, `gpt-5.6-terra`, `gpt-5.6-luna`, `gpt-5.5`, and `gpt-5.4`. These are the standard-tier short-context rates (≤272K input tokens), which apply because Codex's context window is about 258K. OpenAI charges no extra for cache writes, so `cache_write_5m` is set to the input rate. When Codex runs on a ChatGPT plan, this is an API-equivalent figure, not what was billed. Models missing from the table (e.g. `gpt-4o`) stay unpriced. `bossman prices --init` copies the default table to `<home>/pricing.toml`, which then replaces the built-in table entirely. Run `bossman index --force` after editing, or after upgrading bossman with new default rates, so existing sessions are repriced. See [Configuration and Operations](../operations/configuration.md).

## Human involvement

- **Prompts:** messages the human typed, slash commands included.
- **Interrupts:** turns the human stopped.
- **Rejections:** tool calls the human declined, including Codex commands "aborted by user".
- **Interventions** = (prompts − 1) + interrupts + rejections (`Session.Interventions`): every time the human steered after the opening request. A session that ran from one prompt to the end without help scores 0.

## Tool error rate

The error rate is tool errors divided by tool calls. Rejections are excluded, because a declined call is the human's choice, not a failure. For Codex, per-command records replace the shell wrappers when Codex logs them (see [Agent Log Formats and Parsing](agent-logs.md#codex-tool-calls-and-errors)). Lists and the UI flag rates of 10% or more.

**API errors** (overloads, auth expiry, stream errors) are counted separately.

## Time

- **Wall time**: from the first to the last event (`Session.WallSeconds`). Resumed sessions can span days.
- **Agent time**: the sum of turn durations the agent itself reported (Claude `turn_duration`, Codex `task_complete`/`turn_aborted`).
- **Active time**: the union of two sets of intervals. The first is the agent-reported turns. The second is every gap between consecutive events no longer than the idle cap (`idle_minutes`, default 5). The turns cover long builds and test runs that log nothing for a while. The gap rule covers the human reading and typing. Because both are merged as a union (`clock.span`), active time is never less than the time the agent says it worked. The parsers' clock ignores subagent lines and lines copied in by imports.

## Other counts

- **Compactions**: Claude `compact_boundary` lines (or compact summaries, whichever is higher), and Codex `compacted` lines.
- **Subagents**: Claude `subagents/*.jsonl` files.
- **Concluded**: the session ended with the session-catalogue-close marker, and no prompt came after it.

## Aggregates

`store.Stats(filter, by)` groups by `agent`, `project`, `model`, `tool`, `day`, `week` (local weeks starting Monday), or `month`, and always returns a total row too. `project` groups by the user's project override when one is set, else the project the agent recorded (see [Archive and Index](../architecture/archive-and-index.md#project-override)).

- **Session-level groups** sum the session columns. Their `unpriced` count is the number of sessions whose cost source is `none` or `partial`.
- **Model groups** sum `session_models`, counting a session once per model it used, with cost from the per-model shares.
- **Tool groups** sum `session_tools` calls and errors.
- **Codex copies of Claude sessions without their own prompts** are excluded from every aggregate, so the same work is not counted twice. Listings hide the same sessions by default, so what a listing shows is what the totals count. The UI shows the cost tile as "—" when every session in view is unpriced, rather than "$0".
