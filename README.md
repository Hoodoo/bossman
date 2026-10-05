# bossman

Keep, browse, and measure your local coding-agent sessions.

bossman copies Claude Code and Codex session logs into its own archive
(Claude Code deletes sessions older than `cleanupPeriodDays`, 30 by
default), indexes them into SQLite, and reports cost, tokens, turns, tool
error rates, wall-clock and active time, and human interventions. You can
give sessions your own display names, tags, notes, and links from the CLI
or a local web UI. It never modifies the agents' files.

A single Go binary; no runtime dependencies.

## Install

```sh
go install github.com/Hoodoo/bossman/cmd/bossman@latest
# or, from a clone:
make install          # builds and installs ~/.local/bin/bossman
```

For Claude Code, install the `session-catalogue-close` skill so you can end
a session with "conclude this session" and bossman reports it as concluded
rather than interrupted:

```sh
mkdir -p ~/.claude/skills/session-catalogue-close
cp skills/session-catalogue-close/SKILL.md ~/.claude/skills/session-catalogue-close/
```

## Use

```sh
bossman sync          # archive new/grown session files, then index them
bossman ls            # recent sessions
bossman ls -p owcli --since 7d --sort cost
bossman show 211383a8 --transcript
bossman stats --by project      # or agent, model, tool, day, week, month
bossman serve --open            # web UI on http://127.0.0.1:7788/
```

Sessions are referred to by id, a unique id prefix (4+ characters),
`agent:id`, or their display name.

### Your metadata

```sh
bossman name 211383a8 "owcli bootstrap"     # "-" clears it
bossman tag 211383a8 owcli setup --rm wip
bossman link add 211383a8 https://github.com/me/owcli/pull/3 -l "PR #3"
bossman note 211383a8 "Decided to vendor the parser"   # -a appends, - reads stdin
bossman meta export > bossman-meta.json               # backup; `meta import` merges
```

Names, tags, notes, and links live in tables that indexing never touches,
so `bossman index --force` rebuilds everything else safely.

### Keep archiving

Archiving only helps if it runs before the agents clean up. Run `sync`
regularly, e.g. from cron:

```
17 * * * * $HOME/.local/bin/bossman sync >/dev/null
```

or leave `bossman serve --sync-every 30m` running.

### Behind a reverse proxy

`serve` stays on loopback by default. To publish the UI through a proxy that
signs people in, such as Google IAP, let it listen where the proxy reaches
it, accept the public name, and trust the proxy's user header:

```sh
bossman serve --addr 0.0.0.0:7788 --allow-host bossman.example.com \
  --user-header X-Goog-Authenticated-User-Email
```

Requests without the header are refused, and `GET /api/viewer` reports who
is signed in. Only trust the header when nothing but the proxy can reach the
address (on GCP, a firewall that admits only the load balancer).

## What is measured

| Metric | Meaning |
| --- | --- |
| cost | Claude Code's own recorded cost when present; otherwise tokens × the pricing table (Claude and OpenAI list prices; for Codex on a ChatGPT plan this is the API-equivalent cost). `bossman prices --init` lets you edit the rates |
| tokens | input, cache write (5m/1h), cache read, output (incl. reasoning), per model |
| prompts | messages the human typed (slash commands count; injected context does not) |
| interventions | follow-up prompts + interrupts + rejected tool calls |
| tool errors | failed tool calls / all tool calls; Codex shell commands are counted per command when Codex logs them |
| active time | union of the agent's reported turns and of event gaps up to `idle_minutes` (5) |
| wall time | first to last event |
| compactions, subagents, API errors | as logged |
| concluded | the session ended with the `session-catalogue-close` marker |
| summaries | what the agents wrote: Claude titles, recaps and compaction summaries, Codex thread names, the catalogue away-summary |

Codex Desktop can copy Claude Code sessions into Codex threads on its own
(`external-agent-import-sync-enabled` under `[desktop]` in
`~/.codex/config.toml`). bossman marks those threads as "Claude copy",
links them to their source, and counts only work done in Codex.

See [docs/design.md](docs/design.md) for the log formats and the reasoning
behind each metric.

## Files

`bossman paths` prints them. By default:

- `~/.local/share/bossman/archive/` — verbatim copies (`claude/` mirrors
  `~/.claude/projects`, `codex/` mirrors `~/.codex/sessions` plus
  `session_index.jsonl` and `external_agent_session_imports.json`). A file
  that shrinks or changes is kept as `<name>.~<timestamp>`; nothing is
  ever deleted.
- `~/.local/share/bossman/bossman.db` — index and your metadata.
- `~/.local/share/bossman/config.toml` — optional: `claude_dir`,
  `codex_dir`, `idle_minutes`.
- `~/.local/share/bossman/pricing.toml` — optional pricing override.

`BOSSMAN_HOME`, `BOSSMAN_CLAUDE_DIR`, and `BOSSMAN_CODEX_DIR` override the
locations.
