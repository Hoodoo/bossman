# bossman design

bossman archives, indexes, and analyzes local coding-agent sessions. It
was inspired by [sessionlog](https://github.com/npow/sessionlog) (Python,
SQLite of raw entries) but differs in three ways: it keeps verbatim copies
of the logs because the agents delete them, it indexes per session rather
than per raw entry, and it stores user-written metadata that survives
reindexing.

## Data flow

```
~/.claude/projects ─┐            archive/          parse        bossman.db
~/.codex/sessions  ─┼─ Mirror ─▶ claude/, codex/ ─────────▶ sessions, session_models,
~/.codex/*.json(l) ─┘                                       session_tools, session_summaries
                                                            annotations, links, tags  ◀── user
```

- `internal/archive` mirrors files. Logs are append-only, so a file is
  copied when new or grown (its old bytes are a prefix of the new file).
  If the existing bytes changed, the old copy is renamed
  `<name>.~<UTC timestamp>` before copying. Nothing is deleted.
- `internal/parse` discovers sessions and parses them. Discovery works the
  same on the agents' directories and on the archive, because the archive
  mirrors their layout.
- `internal/catalog` runs archive → index. Index reparses a session only
  when the total size or latest mtime of its files changed, and marks
  sessions the agents no longer have (`in_source = 0`).
- `internal/store` holds the derived index (safe to rebuild with
  `index --force`) and the user tables, which nothing derives.
- `internal/web` serves an embedded vanilla-JS UI and a JSON API. It
  binds to loopback, rejects foreign `Host` headers (DNS rebinding) and
  foreign `Origin`s, and requires JSON bodies on writes, so another web
  page cannot drive it.

## Claude Code logs

`~/.claude/projects/<cwd with / → ->/<session id>.jsonl`, plus an optional
`<session id>/` directory with `subagents/agent-*.jsonl` (and
`.meta.json`) and `tool-results/*.txt` sidecars. The whole directory is
archived.

Facts the parser relies on (verified on Claude Code 2.1.28x logs):

- **Usage is repeated.** An assistant message is written as one line per
  content block, and every line carries the full `message.usage`. Usage is
  therefore keyed by `message.id` (last line wins). Summing lines inflates
  tokens about twofold.
- `usage.cache_creation.ephemeral_5m_input_tokens` and
  `ephemeral_1h_input_tokens` split cache writes, which are priced
  differently (1.25× and 2× input).
- `cost-state` lines carry `totalCostUSD`, Claude Code's own cumulative
  cost. bossman prefers it to the pricing table. On real sessions the
  table lands 3–8% lower, plausibly because of calls not logged per
  message (`cost-state.modelUsage` lists a haiku model that never appears
  in assistant lines).
- Human prompts are `type: user` lines that are not `isMeta`,
  `isSidechain`, or `isCompactSummary`, contain no `tool_result`, have no
  non-human `origin.kind` (e.g. `task-notification`), and are not injected
  text (`<local-command-…>`, `<system-reminder>`, …). Slash commands
  (`<command-name>`) count as prompts.
- Interrupts are user text containing `[Request interrupted by user`.
  Rejections are tool results containing "The user doesn't want to proceed
  with this tool use"; these are interventions, not tool errors.
- `system` subtypes: `turn_duration` (agent working time, `durationMs`,
  stamped at the turn's end), `away_summary` (recap; the trailing
  "(disable recaps in /config)" is stripped), `compact_boundary`,
  `api_error`. Assistant lines with `isApiErrorMessage` are API errors
  and carry the `<synthetic>` model, which is ignored for usage.
- Titles: `custom-title` (user rename) wins over the last `ai-title`.
  Older logs have `summary` lines.

## Codex logs

`~/.codex/sessions/YYYY/MM/DD/rollout-<timestamp>-<uuid>.jsonl`. Each line
is `{timestamp, type, payload}`.

- `session_meta`: id, cwd, cli_version, originator, git branch.
  `turn_context`: the model in effect.
- **Tokens are cumulative.** `event_msg/token_count.info.total_token_usage`
  is a running total; bossman attributes each increase to the current
  model. Summing events overcounts. OpenAI `input_tokens` include
  `cached_input_tokens`, so uncached input is the difference.
- Prompts appear as `event_msg/user_message` (older) and, in newer
  versions, also as `item_completed/UserMessage`. bossman counts one
  source, never both. `response_item` messages with role `user` include
  injected context and are ignored.
- Shell commands run through wrapper tools (`exec_command`, and the
  code-mode `exec` that runs a script of many commands). Newer Codex logs
  every command as `item_completed/CommandExecution` with a status. When
  those exist, they replace the wrappers in tool counts. A code-mode
  script's own output ("Script completed") hides its failed commands:
  counting wrappers found 0 errors where Codex logged 9 failed commands
  out of 97. Otherwise a wrapper output's exit code ("Process exited with
  code N", "Exit code: N", `"exit_code":N`), "Script failed", or "apply_patch
  verification failed" marks an error, and "aborted by user" marks a
  rejection.
- `turn_aborted` with reason `interrupted` is an interrupt.
  `task_complete`/`turn_aborted` `duration_ms` is agent working time.
  `compacted` lines are compactions.
- `~/.codex/session_index.jsonl` appends `{id, thread_name}` on every
  rename; the last wins.
- `~/.codex/external_agent_session_imports.json` lists Codex threads
  imported from Claude Code sessions. Their rollouts start with a copy of
  the Claude conversation, all stamped at import time. bossman records
  `imported_from` and ignores lines up to two minutes after `imported_at`.
  Aggregates also skip imports with no later activity, so the same work is
  not counted twice.
- Codex records no cost. Its models are absent from the default pricing
  table, so Codex sessions show as unpriced until the user adds rates.

## Metrics

- **Active time** is the union of agent-reported turn intervals and of
  gaps between consecutive events no longer than `idle_minutes` (default
  5). The turns cover long builds that log nothing for a while. The gap
  rule covers the human reading and typing.
- **Interventions** = (prompts − 1) + interrupts + rejections: every time
  the human steered after the opening request.
- **Concluded** requires the `<!-- cc-catalogue:session-concluded` comment
  written by the session-catalogue-close skill (a bare mention does not
  count). A human prompt after it means the session went on, and the flag
  is cleared.
- **Cost source** per session: `agent` (recorded by the agent), `table`,
  `partial` (some models unpriced), or `none`. Model-level costs are scaled
  to the agent's figure when it exists, so model totals add up.

## Not done yet

- LLM-written summaries. Only the agents' own summaries are used for now;
  a `summarize` command could run `claude -p` on sessions without one.
- Other agents (Cursor, Gemini CLI, …): each needs its own adapter, like
  `parse/claude.go` and `parse/codex.go`.
- Writing names back to the agents (Claude `custom-title`, Codex
  `session_index`). bossman names are deliberately bossman-only.
