---
type: Architecture
title: Web UI and API
description: How bossman serve works, covering the embedded single-page UI, the JSON API routes, the request guards that keep other web pages out, and the front-end views.
tags: [web, api, ui, security]
verified:
  - by: owcli/v0.4.0
    at: "2026-10-09T22:00:54.504Z"
sources:
  - id: openwiki-source-624bec8caa72beb0cfc3a9ff
    resource: repo://internal/cli/serve.go
  - id: openwiki-source-6f70615716e7cd05fe8db472
    resource: repo://internal/store/query.go
  - id: openwiki-source-213e0022dfc208535b4c26a9
    resource: repo://internal/web/static/app.js
  - id: openwiki-source-688beee3d4edc09e1fc7e646
    resource: repo://internal/web/static/style.css
  - id: openwiki-source-6dbe79f2b1613ac94797fd56
    resource: repo://internal/web/web.go
  - id: openwiki-source-eb4688fc0fba2b62687b137d
    resource: repo://internal/web/web_test.go
generated: { by: "owcli/v0.4.0", at: "2026-10-09T22:01:37.013Z" }
---

# Web UI and API

`bossman serve` runs an HTTP server with two parts: a static single-page UI, embedded in the binary from `internal/web/static/`, and a JSON API over the same `catalog.Catalog` the CLI uses. The default address is `127.0.0.1:7788`. The UI has no build step and no external assets: it is plain HTML, CSS, and one `app.js`.

## Starting the server

`internal/cli/serve.go`:

- It warns on stderr when `--addr` is not a loopback address and no `--user-header` is set, because anyone who can reach that address can read every session.
- It runs one sync at start-up unless `--no-sync` is given. With `--sync-every <duration>`, a goroutine syncs on that interval.
- `--open` launches the browser (`xdg-open`, `open`, or `rundll32`).

`web.Server.Sync` serialises syncs with a mutex, so the background ticker and `POST /api/sync` never run concurrently. It also records the last successful sync time, which `/api/facets` reports to the UI.

## API routes

Registered in `web.New` using Go 1.22 method-and-pattern routing:

| Route | Purpose |
| --- | --- |
| `GET /api/sessions` | List. Query parameters: `agent`, `project`, `q`, `tag`, `since`/`until` (YYYY-MM-DD), `archived=1`, `copies=1` (include Claude copies with no prompts of their own, hidden otherwise), `notag` (leave out sessions with this tag; a trailing `*` matches a prefix), `sort`, `dir=asc`, `limit` |
| `GET /api/sessions/{key}` | `store.Detail` for one session |
| `GET /api/sessions/{key}/transcript` | Transcript events, reparsed from the archive |
| `PUT /api/sessions/{key}/meta` | `{display_name?, notes?}` |
| `PUT /api/sessions/{key}/project` | `{project}` sets the project override; `{reset: true}` clears it; a body with neither is a 400 |
| `PUT /api/sessions/{key}/tags` | `{tags: [...]}`, which replaces the set |
| `POST /api/sessions/{key}/links` | `{url, label}` |
| `DELETE /api/sessions/{key}/links/{id}` | Remove a link |
| `GET /api/stats?by=…` | Groups plus a total; `by` is agent, project, model, tool, day, week, or month (default day) |
| `GET /api/facets` | Project list, tag counts, and last sync time, for the filter controls |
| `POST /api/sync` | Archive and index now |

Write endpoints return the updated `Detail`, so the UI re-renders from the response. They return 404 for keys that are not indexed. Errors are returned as `{"error": "..."}`, with 400 for invalid input (`badRequestError`), 404 for unknown sessions, and 500 for anything else. Request bodies are capped at 1 MiB.

## Request guards

The API holds private data and can change it, so `Server.ServeHTTP` applies three checks before routing:

1. **Host allow-list.** The `Host` header must be `localhost`, `127.0.0.1`, `::1`, the listen host, or a name given with `--allow-host` (compared case-insensitively). Anything else gets 403. This defeats DNS rebinding, where a hostile page's domain is re-pointed to 127.0.0.1.
2. **Origin check on writes.** A non-GET request that carries an `Origin` header must come from the same host.
3. **JSON-only writes.** Non-GET requests must send `Content-Type: application/json`, otherwise 415. A cross-site HTML form cannot send that content type, and a script that tries triggers a CORS preflight, which the server never approves.

It also sets `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`. `internal/web/web_test.go` (`TestRequestGuards`) covers each guard.

## Behind a reverse proxy

`web.Options` (from `serve --allow-host` and `--user-header`) lets the UI run behind a proxy that signs people in, such as Google IAP:

- `AllowHosts` adds the proxy's public names to the Host allow-list.
- `UserHeader` names a header the proxy sets to the signed-in user (`X-Goog-Authenticated-User-Email` for IAP). `Server.Viewer` reads it and strips everything up to the last `:`, which removes IAP's `accounts.google.com:` prefix. When `UserHeader` is set, a request without it gets 401 before routing, so traffic that bypasses the proxy fails closed.
- `GET /api/viewer` returns `{"user": ...}`, empty when no header is configured.

The header is only as trustworthy as the network: anyone who can reach the listen address directly could send it, so it is meant for an address only the proxy can reach. The Origin and content-type guards apply unchanged; behind a proxy that preserves the Host header, the UI's own requests still match it. `TestBehindProxy` covers the public name, the 401, the viewer, and same-origin and foreign-origin writes.

## Front end

`app.js` is a hash-routed single-page app:

- `#/` shows sessions. A filter bar offers search, agent, project, tag, a since date, "only deleted by agent", "hide Claude copies" (ticked by default; unticking it adds `copies=1`), and "show sinks". Sinks are sessions tagged `sink:<name>` that belong to no project. The list asks the API for `notag=sink:*` unless "show sinks" is ticked (`sinks=1` in the URL) or the tag filter picks a `sink:` tag, so choosing a sink always shows its sessions. The checkbox appears only once some session has a `sink:` tag, and the stats page never hides sinks. Clicking a column header changes the sort. Filters and sort are kept in the URL.
- `#/s/<key>` shows one session: metric tiles, an editable project (a text field suggesting known projects, with "Reset to detected" when overridden), editable display name, tags, links, and notes, the agents' summaries, per-model and per-tool tables, and a transcript that loads on demand. When `command_usage` is present, the Tools card also groups shell calls into activity categories; each category expands to the original command text and marks failed calls. If aggregate shell counts exist but command rows do not, the card recommends `bossman index --force` to backfill older indexed sessions.
- `#/stats` shows analytics: a time range, KPI tiles, a per-day bar chart stacked by agent (metric selectable: cost, tokens, active time, sessions, prompts), and breakdowns by project, model, tool, or week.

All DOM is built with the `h()` helper, which creates elements and text nodes and never uses `innerHTML`. Session content, which includes arbitrary tool output, therefore cannot inject markup. User links are rendered as anchors only for `http`, `https`, `file`, and `mailto` URLs (`safeLink`), on top of the server-side scheme check in `store.AddLink`.

The chart is hand-written SVG (`dailyChart`). Claude is series 1 (blue) and Codex series 2 (orange), defined as CSS custom properties with separate light and dark values. Each day column has a hover tooltip, and a legend appears when more than one agent is shown. The theme follows the OS setting and can be toggled from the header. The choice, and the last chart metric, are stored in `localStorage` under `bossman.*`. Storage failures are ignored.

## Changing the UI

Edit the files in `internal/web/static/` and rebuild. They are embedded with `//go:embed static`, so a running server needs a restart to pick up changes. No tests cover the UI, so check it in a browser (`bossman serve --open`) in both themes and at a narrow width.
