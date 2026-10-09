"use strict";

// ---------- helpers ----------

const $ = (sel, root = document) => root.querySelector(sel);
const view = $("#view");
const tooltip = $("#tooltip");

// h builds DOM without innerHTML, so session text can never inject markup.
function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "dataset") Object.assign(el.dataset, v);
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

const SVGNS = "http://www.w3.org/2000/svg";
function s(tag, attrs, ...children) {
  const el = document.createElementNS(SVGNS, tag);
  for (const [k, v] of Object.entries(attrs || {})) if (v != null) el.setAttribute(k, v);
  for (const c of children.flat()) if (c != null) el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  return el;
}

async function api(path, opts = {}) {
  const init = { headers: {} , ...opts };
  if (opts.body !== undefined) {
    init.headers["Content-Type"] = "application/json";
    init.body = JSON.stringify(opts.body);
  }
  const res = await fetch(path, init);
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

const fmt = {
  money(v, source) {
    if (source === "none") return "—";
    let out;
    if (!v) out = "$0";
    else if (v < 0.01) out = "<$0.01";
    else if (v < 1000) out = "$" + v.toFixed(2);
    else out = "$" + Math.round(v).toLocaleString();
    return source === "partial" ? out + "+" : out;
  },
  count(n) {
    if (n >= 1e9) return (n / 1e9).toFixed(1) + "B";
    if (n >= 1e6) return (n / 1e6).toFixed(1) + "M";
    if (n >= 1e4) return Math.round(n / 1e3) + "k";
    if (n >= 1e3) return (n / 1e3).toFixed(1) + "k";
    return String(n);
  },
  dur(sec) {
    if (!sec || sec <= 0) return "—";
    if (sec < 60) return Math.round(sec) + "s";
    if (sec < 3600) return Math.round(sec / 60) + "m";
    const hrs = Math.floor(sec / 3600), m = Math.round((sec % 3600) / 60);
    if (hrs < 48) return hrs + "h " + String(m).padStart(2, "0") + "m";
    return Math.floor(hrs / 24) + "d " + (hrs % 24) + "h";
  },
  pct(part, whole) {
    if (!whole) return "—";
    return (100 * part / whole).toFixed(1) + "%";
  },
  when(ts) {
    if (!ts) return "—";
    const d = new Date(ts);
    return d.toLocaleDateString(undefined, { year: "numeric", month: "short", day: "numeric" }) +
      " " + d.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" });
  },
  path(p) { return (p || "").replace(/^\/home\/[^/]+/, "~").replace(/^\/Users\/[^/]+/, "~"); },
  tokens(r) { return (r.input || 0) + (r.cache_write || 0) + (r.cache_read || 0) + (r.output || 0); },
};

function sessionName(r) {
  for (const v of [r.display_name, r.title, r.first_prompt]) {
    if (v && v.trim()) return v.trim().replace(/\s+/g, " ");
  }
  return r.id;
}

function agentChip(agent) {
  return h("span", { class: "chip agent-" + agent }, h("span", { class: "dot " + agent }), agent);
}

function showTip(evt, head, rows) {
  tooltip.replaceChildren(
    h("div", { class: "t-head" }, head),
    ...rows.map(([k, v, color]) => h("div", { class: "t-row" },
      h("span", {}, color ? h("span", { class: "dot", style: "background:" + color }) : null, k),
      h("b", {}, v))),
  );
  tooltip.hidden = false;
  const pad = 14, w = tooltip.offsetWidth, ht = tooltip.offsetHeight;
  let x = evt.clientX + pad, y = evt.clientY + pad;
  if (x + w > window.innerWidth - 8) x = evt.clientX - w - pad;
  if (y + ht > window.innerHeight - 8) y = evt.clientY - ht - pad;
  tooltip.style.left = x + "px";
  tooltip.style.top = y + "px";
}
function hideTip() { tooltip.hidden = true; }

function tile(label, value, sub) {
  return h("div", { class: "tile" },
    h("div", { class: "label" }, label),
    h("div", { class: "value" }, value),
    sub ? h("div", { class: "sub" }, sub) : null);
}

function inlineBar(value, max, label) {
  const w = max > 0 ? Math.max(0, Math.min(100, 100 * value / max)) : 0;
  return h("div", { class: "inline-bar" },
    h("div", { class: "track" }, h("div", { class: "fill", style: `width:${w}%` })),
    h("span", { class: "v" }, label));
}

function errorBox(err) {
  return h("div", { class: "error-box" }, String(err.message || err));
}

// Remembered UI preferences; harmless if storage is unavailable.
const prefs = {
  get(k, d) { try { const v = localStorage.getItem("bossman." + k); return v == null ? d : JSON.parse(v); } catch { return d; } },
  set(k, v) { try { localStorage.setItem("bossman." + k, JSON.stringify(v)); } catch { /* ignore */ } },
};

// ---------- theme & sync ----------

function applyTheme(t) {
  if (t) document.documentElement.dataset.theme = t;
  else delete document.documentElement.dataset.theme;
}
applyTheme(prefs.get("theme", null));
$("#theme").addEventListener("click", () => {
  const dark = document.documentElement.dataset.theme
    ? document.documentElement.dataset.theme === "dark"
    : matchMedia("(prefers-color-scheme: dark)").matches;
  const next = dark ? "light" : "dark";
  prefs.set("theme", next);
  applyTheme(next);
  route();
});

let facets = { projects: [], tags: {}, last_sync: "" };
async function loadFacets() {
  facets = await api("/api/facets");
  $("#sync-status").textContent = facets.last_sync ? "synced " + fmt.when(facets.last_sync) : "";
}

$("#sync").addEventListener("click", async () => {
  const btn = $("#sync");
  btn.disabled = true;
  btn.textContent = "Syncing…";
  try {
    const st = await api("/api/sync", { method: "POST", body: {} });
    const a = st.archive, i = st.index;
    $("#sync-status").textContent =
      `${a.copied + a.grown} files archived, ${i.indexed} sessions reindexed`;
    await loadFacets();
    route();
  } catch (e) {
    $("#sync-status").textContent = "sync failed: " + e.message;
  } finally {
    btn.disabled = false;
    btn.textContent = "Sync";
  }
});

// ---------- routing ----------

function route() {
  hideTip();
  const hash = location.hash.replace(/^#/, "") || "/";
  const [path, query] = hash.split("?");
  const params = new URLSearchParams(query || "");
  for (const a of document.querySelectorAll("[data-nav]")) {
    a.classList.toggle("active",
      (a.dataset.nav === "stats" && path.startsWith("/stats")) ||
      (a.dataset.nav === "sessions" && !path.startsWith("/stats")));
  }
  if (path.startsWith("/s/")) return renderDetail(decodeURIComponent(path.slice(3)));
  if (path.startsWith("/stats")) return renderStats(params);
  return renderSessions(params);
}
window.addEventListener("hashchange", route);

function setQuery(base, params) {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v) q.set(k, v);
  const next = "#" + base + (q.toString() ? "?" + q : "");
  if (location.hash !== next) location.hash = next;
  else route();
}

// ---------- filters ----------

function filterBar(params, base, opts = {}) {
  const state = Object.fromEntries(params);
  const apply = (k, v) => { state[k] = v; setQuery(base, state); };
  const projectSel = h("select", { onchange: e => apply("project", e.target.value), "aria-label": "Project" },
    h("option", { value: "" }, "All projects"),
    facets.projects.map(p => h("option", { value: p, selected: p === state.project }, fmt.path(p) || "(none)")));
  const tags = Object.keys(facets.tags).sort();
  const children = [];
  if (opts.search) {
    let timer;
    children.push(h("input", {
      type: "search", placeholder: "Search names, prompts, summaries, notes…", value: state.q || "",
      "aria-label": "Search",
      oninput: e => { clearTimeout(timer); timer = setTimeout(() => apply("q", e.target.value), 300); },
    }));
  }
  children.push(
    h("select", { onchange: e => apply("agent", e.target.value), "aria-label": "Agent" },
      h("option", { value: "" }, "All agents"),
      ["claude", "codex"].map(a => h("option", { value: a, selected: a === state.agent }, a))),
    projectSel,
  );
  if (tags.length) {
    children.push(h("select", { onchange: e => apply("tag", e.target.value), "aria-label": "Tag" },
      h("option", { value: "" }, "All tags"),
      tags.map(t => h("option", { value: t, selected: t === state.tag }, "#" + t + " (" + facets.tags[t] + ")"))));
  }
  if (opts.range) {
    const ranges = [["7", "Last 7 days"], ["30", "Last 30 days"], ["90", "Last 90 days"], ["all", "All time"]];
    children.push(h("select", { onchange: e => apply("range", e.target.value), "aria-label": "Time range" },
      ranges.map(([v, l]) => h("option", { value: v, selected: v === (state.range || opts.range) }, l))));
  } else {
    children.push(h("label", {}, "since",
      h("input", { type: "date", value: state.since || "", onchange: e => apply("since", e.target.value) })));
  }
  if (opts.archived) {
    children.push(h("label", {},
      h("input", { type: "checkbox", checked: state.archived === "1", onchange: e => apply("archived", e.target.checked ? "1" : "") }),
      "only deleted by agent"));
  }
  if (opts.sinks && tags.some(t => t.startsWith("sink:"))) {
    children.push(h("label", { title: "Sessions tagged sink:… belong to no project; stats still count them" },
      h("input", { type: "checkbox", checked: state.sinks === "1", onchange: e => apply("sinks", e.target.checked ? "1" : "") }),
      "show sinks"));
  }
  if (opts.copies) {
    children.push(h("label", { title: "Codex Desktop's copies of Claude sessions with no prompts of their own; stats never count them" },
      h("input", { type: "checkbox", checked: state.copies !== "1", onchange: e => apply("copies", e.target.checked ? "" : "1") }),
      "hide Claude copies"));
  }
  return h("div", { class: "filters" }, children);
}

function apiFilter(params, extra = {}) {
  const q = new URLSearchParams();
  for (const k of ["agent", "project", "q", "tag", "since", "archived", "copies", "sort", "dir"]) {
    if (params.get(k)) q.set(k, params.get(k));
  }
  for (const [k, v] of Object.entries(extra)) if (v) q.set(k, v);
  return q.toString();
}

// ---------- sessions list ----------

const columns = [
  { key: "started", label: "Started", cls: "" },
  { key: "name", label: "Session", cls: "" },
  { key: null, label: "Agent", cls: "" },
  { key: "project", label: "Project", cls: "" },
  { key: "cost", label: "Cost", cls: "num" },
  { key: "tokens", label: "Tokens", cls: "num" },
  { key: "prompts", label: "Prompts", cls: "num", title: "Messages the human typed" },
  { key: "interventions", label: "Interv.", cls: "num", title: "Follow-up prompts + interrupts + rejected tool calls" },
  { key: "errors", label: "Tool err.", cls: "num", title: "Failed tool calls / all tool calls" },
  { key: "active", label: "Active", cls: "num", title: "Time with activity, idle gaps over the cap excluded" },
];

async function renderSessions(params) {
  const sort = params.get("sort") || "started";
  const dir = params.get("dir") || "desc";
  view.replaceChildren(filterBar(params, "/", { search: true, archived: true, copies: true, sinks: true }), h("div", { class: "empty" }, "Loading…"));
  let rows;
  try {
    // Sinks stay hidden unless asked for, or unless the tag filter picks one.
    const hideSinks = params.get("sinks") !== "1" && !(params.get("tag") || "").startsWith("sink:");
    rows = await api("/api/sessions?" + apiFilter(params, { limit: "500", notag: hideSinks ? "sink:*" : "" }));
  } catch (e) {
    view.replaceChildren(errorBox(e));
    return;
  }
  const head = h("tr", {}, columns.map(c => {
    const active = c.key === sort;
    return h("th", {
      class: (c.cls + (c.key ? " sortable" : "")).trim(), title: c.title,
      onclick: c.key ? () => {
        const state = Object.fromEntries(params);
        state.dir = active && dir === "desc" ? "asc" : "desc";
        state.sort = c.key;
        setQuery("/", state);
      } : null,
    }, c.label, active ? h("span", { class: "arrow" }, dir === "asc" ? " ↑" : " ↓") : null);
  }));
  const body = h("tbody", {}, rows.map(r => {
    const name = sessionName(r);
    const sub = r.display_name && r.title ? r.title : (r.summary || r.first_prompt || "");
    const badges = [];
    if (!r.in_source) badges.push(h("span", { class: "chip", title: "The agent deleted it; bossman's archive still has it" }, "archived"));
    if (r.concluded) badges.push(h("span", { class: "chip", title: "Ended with the catalogue marker" }, "concluded"));
    if (r.imported_from) badges.push(h("span", { class: "chip", title: copyNote(r.imported_from) }, "Claude copy"));
    const errRate = r.tool_calls ? r.tool_errors / r.tool_calls : 0;
    return h("tr", { class: "clickable", onclick: () => { location.hash = "#/s/" + encodeURIComponent(r.key); } },
      h("td", { class: "num secondary" }, fmt.when(r.started_at)),
      h("td", { class: "name" },
        h("div", { class: "title" }, name),
        sub && sub !== name ? h("div", { class: "sub" }, sub) : null,
        badges, r.tags.map(t => h("span", { class: "chip tag" }, t))),
      h("td", {}, agentChip(r.agent)),
      h("td", { class: "secondary", title: r.project }, fmt.path(r.project).split("/").slice(-2).join("/")),
      h("td", { class: "num", title: r.cost_source === "agent" ? "Recorded by the agent" : r.cost_source === "none" ? "No pricing for this model" : "From the pricing table" }, fmt.money(r.cost_usd, r.cost_source)),
      h("td", { class: "num" }, fmt.count(fmt.tokens(r))),
      h("td", { class: "num" }, r.prompts),
      h("td", { class: "num" }, r.interventions),
      h("td", { class: "num" + (errRate >= 0.1 ? " status-bad" : "") }, fmt.pct(r.tool_errors, r.tool_calls)),
      h("td", { class: "num" }, fmt.dur(r.active_s)),
    );
  }));
  const total = rows.reduce((a, r) => (a.cost += r.cost_usd, a.tokens += fmt.tokens(r), a), { cost: 0, tokens: 0 });
  view.replaceChildren(
    filterBar(params, "/", { search: true, archived: true, copies: true, sinks: true }),
    h("p", { class: "muted" }, `${rows.length}${rows.length === 500 ? "+" : ""} sessions · ${fmt.money(total.cost)} · ${fmt.count(total.tokens)} tokens`),
    rows.length
      ? h("div", { class: "table-wrap" }, h("table", {}, h("thead", {}, head), body))
      : h("div", { class: "empty" }, "No sessions match. Run Sync to archive and index your agents' sessions."),
  );
  const search = $("input[type=search]", view);
  if (params.get("q") && search) { search.focus(); search.setSelectionRange(search.value.length, search.value.length); }
}

// ---------- session detail ----------

async function renderDetail(key) {
  view.replaceChildren(h("div", { class: "empty" }, "Loading…"));
  let d;
  try {
    d = await api("/api/sessions/" + encodeURIComponent(key));
  } catch (e) {
    view.replaceChildren(h("p", {}, h("a", { href: "#/" }, "← Sessions")), errorBox(e));
    return;
  }
  const rerender = next => { d = next; draw(); };
  const save = async (path, method, body) => {
    try {
      rerender(await api("/api/sessions/" + encodeURIComponent(key) + path, { method, body }));
      loadFacets();
    } catch (e) { alert(e.message); }
  };

  function draw() {
    const nameInput = h("input", { type: "text", value: d.display_name, placeholder: d.title || d.first_prompt?.slice(0, 80) || d.id, "aria-label": "Display name" });
    const nameForm = h("form", { class: "name-edit", onsubmit: e => { e.preventDefault(); save("/meta", "PUT", { display_name: nameInput.value }); } },
      nameInput, h("button", { type: "submit" }, "Rename"));

    const head = h("div", { class: "detail-head" },
      h("div", {},
        h("p", { style: "margin:0 0 6px" }, h("a", { href: "#/" }, "← Sessions")),
        nameForm,
        h("div", { class: "muted", style: "margin-top:6px" },
          agentChip(d.agent), " ", h("code", {}, d.key),
          !d.in_source ? h("span", { class: "chip" }, "archived only") : null,
          d.concluded ? h("span", { class: "chip" }, "concluded") : null)),
    );

    const errRate = d.tool_calls ? d.tool_errors / d.tool_calls : 0;
    const tiles = h("div", { class: "tiles" },
      tile("Cost", fmt.money(d.cost_usd, d.cost_source),
        d.cost_source === "agent" ? `recorded by agent · table ${fmt.money(d.table_cost_usd)}` :
        d.cost_source === "none" ? (fmt.tokens(d) ? "model not in pricing table" : "no usage recorded") : "from pricing table"),
      tile("Tokens", fmt.count(fmt.tokens(d)), `${fmt.count(d.output)} output · ${d.requests} requests`),
      tile("Active time", fmt.dur(d.active_s), `${fmt.dur(d.wall_s)} wall · ${fmt.dur(d.agent_s)} agent working`),
      tile("Human", `${d.prompts} prompts`, `${d.interventions} interventions (${d.interrupts} interrupts, ${d.rejections} rejections)`),
      tile("Tool calls", d.tool_calls, h("span", { class: errRate >= 0.1 ? "status-bad" : "" }, `${d.tool_errors} errors (${fmt.pct(d.tool_errors, d.tool_calls)})`)),
      tile("Context", `${d.compactions} compactions`, `${d.subagents} subagents · ${d.api_errors} API errors`),
    );

    const projectList = "project-options";
    const projectInput = h("input", { type: "text", value: d.project, list: projectList,
      "aria-label": "Project", placeholder: "Project path" });
    const projectEditor = h("div", { class: "row" }, projectInput,
      h("datalist", { id: projectList }, facets.projects.map(p => h("option", { value: p }))),
      h("button", { type: "button", class: "ghost", onclick: () => save("/project", "PUT", { project: projectInput.value }) }, "Save"),
      d.project_overridden ? h("button", { type: "button", class: "link",
        title: `Use detected project: ${d.detected_project || "(none)"}`,
        onclick: () => save("/project", "PUT", { reset: true }) }, "Reset to detected") : null);

    const facts = h("dl", { class: "kv" },
      ...[
        ["Agent title", d.title],
        ["Project", projectEditor],
        ["Branch", d.git_branch],
        ["Agent", [d.agent, d.version, d.entrypoint].filter(Boolean).join(" ")],
        ["Models", d.models.split(",").join(", ")],
        ["Started", fmt.when(d.started_at)],
        ["Ended", fmt.when(d.ended_at)],
        ["Copy of", d.imported_from ? h("span", {}, h("a", { href: "#/s/" + encodeURIComponent(d.imported_from) }, d.imported_from),
          h("div", { class: "muted" }, copyNote(d.imported_from))) : null],
        ["Archived as", d.path],
      ].filter(([, v]) => v).flatMap(([k, v]) => [h("dt", {}, k), h("dd", {}, v)]),
    );

    // Tags
    const tagInput = h("input", { type: "text", placeholder: "add tag", "aria-label": "Add tag", size: 12 });
    const tagsBox = h("div", {},
      h("div", { class: "row" },
        d.tags.map(t => h("span", { class: "chip tag removable" }, t,
          h("button", { type: "button", title: "Remove", onclick: () => save("/tags", "PUT", { tags: d.tags.filter(x => x !== t) }) }, "×"))),
        h("form", { class: "row", onsubmit: e => {
          e.preventDefault();
          const add = tagInput.value.split(/[\s,]+/).map(t => t.replace(/^#/, "").toLowerCase()).filter(Boolean);
          if (add.length) save("/tags", "PUT", { tags: [...new Set([...d.tags, ...add])] });
        } }, tagInput, h("button", { type: "submit", class: "ghost" }, "Add"))));

    // Links
    const urlInput = h("input", { type: "url", placeholder: "https://…", required: true, "aria-label": "Link URL" });
    const labelInput = h("input", { type: "text", placeholder: "label (optional)", "aria-label": "Link label" });
    const links = h("div", {},
      d.link_list.length ? h("ul", { class: "links" }, d.link_list.map(l => h("li", {},
        safeLink(l.url, l.label || l.url), l.label ? h("span", { class: "muted" }, " " + l.url) : null,
        h("button", { type: "button", class: "link", title: "Remove link", onclick: () => save("/links/" + l.id, "DELETE") }, "remove")))) :
        h("p", { class: "muted" }, "No links yet. Attach PRs, issues, or docs."),
      h("form", { class: "row", onsubmit: e => { e.preventDefault(); save("/links", "POST", { url: urlInput.value, label: labelInput.value }); } },
        urlInput, labelInput, h("button", { type: "submit", class: "ghost" }, "Add link")));

    // Notes
    const notes = h("textarea", { "aria-label": "Notes", placeholder: "Notes about this session…" }, d.notes);
    const notesBox = h("div", {}, notes,
      h("div", { class: "row", style: "margin-top:6px" },
        h("button", { type: "button", class: "ghost", onclick: () => save("/meta", "PUT", { notes: notes.value }) }, "Save notes")));

    const summaries = d.summaries.filter(s => s.kind !== "title");
    const summaryBox = summaries.length
      ? summaries.map(s => h("div", { class: "summary" },
        h("div", { class: "kind" }, s.kind, s.at && !s.at.startsWith("0001") ? " · " + fmt.when(s.at) : ""),
        h("div", { class: "text" }, s.text)))
      : h("p", { class: "muted" }, "The agent wrote no summaries for this session.");

    const maxCalls = Math.max(1, ...d.tool_usage.map(t => t.calls));
    const toolTable = h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, "Tool"), h("th", {}, "Calls"), h("th", { class: "num" }, "Errors"))),
      h("tbody", {}, d.tool_usage.map(t => h("tr", {},
        h("td", {}, h("code", {}, t.tool)),
        h("td", {}, inlineBar(t.calls, maxCalls, t.calls)),
        h("td", { class: "num" + (t.calls && t.errors / t.calls >= 0.1 ? " status-bad" : "") }, t.errors ? `${t.errors} (${fmt.pct(t.errors, t.calls)})` : "—")))));

    const activityOrder = ["planning", "documentation", "testing", "implementation", "source-control", "inspection", "environment", "other"];
    const activityLabels = { "source-control": "Source control" };
    const commandGroups = (d.command_usage || []).reduce((groups, command) => {
      (groups[command.activity] ||= []).push(command);
      return groups;
    }, {});
    const commandBreakdown = (d.command_usage || []).length
      ? h("div", { class: "command-breakdown" },
        h("h3", {}, "Shell activity"),
        h("p", { class: "muted" }, "Rudimentary classification; expand a category to inspect the original calls."),
        ...activityOrder.filter(a => commandGroups[a]?.length).map(activity => {
          const commands = commandGroups[activity];
          const errors = commands.filter(c => c.error).length;
          const label = activityLabels[activity] || activity[0].toUpperCase() + activity.slice(1);
          return h("details", { class: "command-group" },
            h("summary", {}, `${label} · ${commands.length} call${commands.length === 1 ? "" : "s"}${errors ? ` · ${errors} errors` : ""}`),
            h("ol", {}, commands.map(c => h("li", { class: c.error ? "command-error" : "" },
              h("span", { class: "muted" }, c.tool + " "), h("code", {}, c.command)))));
        }))
      : null;

    const modelTable = h("table", {},
      h("thead", {}, h("tr", {}, ["Model", "Requests", "Input", "Cache write", "Cache read", "Output", "Cost"].map((c, i) => h("th", { class: i ? "num" : "" }, c)))),
      h("tbody", {}, d.model_usage.map(m => h("tr", {},
        h("td", {}, h("code", {}, m.model)),
        h("td", { class: "num" }, m.requests),
        h("td", { class: "num" }, fmt.count(m.input)),
        h("td", { class: "num" }, fmt.count(m.cache_write_5m + m.cache_write_1h)),
        h("td", { class: "num" }, fmt.count(m.cache_read)),
        h("td", { class: "num" }, fmt.count(m.output)),
        h("td", { class: "num" }, m.cost_usd == null ? "—" : fmt.money(m.cost_usd))))));

    const transcript = h("div", { class: "transcript" },
      h("button", { type: "button", class: "ghost", onclick: e => loadTranscript(e.target.parentElement) }, "Load transcript"));

    view.replaceChildren(
      head,
      tiles,
      h("div", { class: "grid2" },
        h("div", { class: "card stack" }, h("h2", {}, "Details"), facts,
          d.first_prompt ? h("div", {}, h("h2", {}, "First prompt"), h("div", { class: "summary" }, h("div", { class: "text" }, d.first_prompt.slice(0, 1500)))) : null),
        h("div", { class: "card stack" },
          h("div", {}, h("h2", {}, "Tags"), tagsBox),
          h("div", {}, h("h2", {}, "Links"), links),
          h("div", {}, h("h2", {}, "Notes"), notesBox))),
      h("div", { class: "card", style: "margin-top:16px" }, h("h2", {}, "Summaries written by the agent"), summaryBox),
      h("div", { class: "grid2", style: "margin-top:16px" },
        h("div", { class: "card" }, h("h2", {}, "Models"), d.model_usage.length ? modelTable : h("p", { class: "muted" }, "No usage recorded.")),
        h("div", { class: "card" }, h("h2", {}, "Tools"), d.tool_usage.length ? toolTable : h("p", { class: "muted" }, "No tool calls."),
          commandBreakdown,
          !commandBreakdown && d.tool_usage.some(t => ["Bash", "exec", "exec_command", "command", "shell", "local_shell"].includes(t.tool))
            ? h("p", { class: "muted command-backfill" }, "Shell details are not indexed for this session. Run bossman index --force to backfill them.") : null)),
      h("div", { class: "card", style: "margin-top:16px" }, h("h2", {}, "Transcript"), transcript),
    );
  }

  async function loadTranscript(box) {
    box.replaceChildren(h("p", { class: "muted" }, "Parsing the archived log…"));
    let events;
    try {
      events = await api("/api/sessions/" + encodeURIComponent(key) + "/transcript");
    } catch (e) {
      box.replaceChildren(errorBox(e));
      return;
    }
    const filter = h("label", { class: "muted" },
      h("input", { type: "checkbox", onchange: e => box.classList.toggle("hide-tools", e.target.checked) }), " hide tool calls");
    const style = h("style", {}, ".transcript.hide-tools .ev.tool, .transcript.hide-tools .ev.result { display: none; }");
    box.replaceChildren(style, filter, ...events.map(ev => {
      const text = ev.text || "";
      const long = (ev.role === "result" || ev.role === "tool") && text.length > 600;
      const el = h("div", { class: `ev ${ev.role}${ev.sidechain ? " side" : ""}${ev.is_error ? " err" : ""}` },
        h("div", { class: "who" },
          h("b", {}, ev.role + (ev.tool ? " · " + ev.tool : "") + (ev.is_error ? " · error" : "")),
          ev.sidechain ? h("span", {}, "subagent") : null,
          ev.at && !ev.at.startsWith("0001") ? h("span", {}, new Date(ev.at).toLocaleTimeString()) : null),
        text ? h("div", { class: "body" }, long ? text.slice(0, 4000) : text) : null);
      if (long) {
        el.append(h("button", { type: "button", class: "link", onclick: e => {
          el.classList.toggle("expanded");
          e.target.textContent = el.classList.contains("expanded") ? "show less" : "show more";
        } }, "show more"));
      }
      return el;
    }));
    if (!events.length) box.append(h("p", { class: "muted" }, "Empty transcript."));
  }

  draw();
}

// copyNote explains Codex threads that Codex Desktop copied from Claude.
function copyNote(from) {
  return `Codex Desktop copied this from ${from} (external-agent-import-sync-enabled in ` +
    "~/.codex/config.toml). Only work done in Codex counts here.";
}

function safeLink(url, label) {
  let ok = false;
  try { ok = ["http:", "https:", "file:", "mailto:"].includes(new URL(url).protocol); } catch { /* invalid */ }
  return ok ? h("a", { href: url, target: "_blank", rel: "noopener noreferrer" }, label) : h("span", {}, label);
}

// ---------- analytics ----------

const metrics = {
  cost: { label: "Cost", get: g => g.cost_usd, fmt: v => fmt.money(v) },
  tokens: { label: "Tokens", get: g => g.input + g.cache_write + g.cache_read + g.output, fmt: fmt.count },
  active: { label: "Active time", get: g => g.active_s, fmt: fmt.dur },
  sessions: { label: "Sessions", get: g => g.sessions, fmt: String },
  prompts: { label: "Prompts", get: g => g.prompts, fmt: String },
};
const agentColor = a => getComputedStyle(document.documentElement).getPropertyValue(a === "claude" ? "--series-1" : "--series-2").trim();

function rangeSince(range) {
  if (range === "all") return "";
  const d = new Date();
  d.setDate(d.getDate() - Number(range) + 1);
  return d.toISOString().slice(0, 10);
}

async function renderStats(params) {
  const range = params.get("range") || "30";
  const metric = metrics[params.get("metric")] ? params.get("metric") : prefs.get("metric", "cost");
  const by = params.get("by") || "project";
  const since = rangeSince(range);
  const base = apiFilter(params, { since });
  view.replaceChildren(filterBar(params, "/stats", { range: "30" }), h("div", { class: "empty" }, "Loading…"));
  let total, daily, groups;
  try {
    const agents = params.get("agent") ? [params.get("agent")] : ["claude", "codex"];
    const [all, ...perAgent] = await Promise.all([
      api("/api/stats?by=agent&" + base),
      ...agents.map(a => api("/api/stats?by=day&" + apiFilter(params, { since, agent: a }))),
    ]);
    total = all.total;
    daily = agents.map((a, i) => ({ agent: a, groups: perAgent[i].groups }));
    groups = await api(`/api/stats?by=${by}&` + base);
  } catch (e) {
    view.replaceChildren(errorBox(e));
    return;
  }

  const unpriced = total.unpriced;
  const tiles = h("div", { class: "tiles" },
    tile("Cost", unpriced && unpriced === total.sessions ? "—" : fmt.money(total.cost_usd),
      unpriced ? `${unpriced} of ${total.sessions} sessions unpriced` : "all sessions priced"),
    tile("Sessions", total.sessions, `${total.prompts} prompts`),
    tile("Tokens", fmt.count(total.input + total.cache_write + total.cache_read + total.output),
      `${fmt.pct(total.cache_read, total.input + total.cache_write + total.cache_read)} of input from cache`),
    tile("Active time", fmt.dur(total.active_s), `${fmt.dur(total.agent_s)} agent working`),
    tile("Interventions", total.interventions,
      total.sessions ? `${(total.interventions / total.sessions).toFixed(1)} per session` : ""),
    tile("Tool errors", fmt.pct(total.tool_errors, total.tool_calls), `${total.tool_errors} of ${total.tool_calls} calls`),
  );

  const metricSeg = h("div", { class: "seg", role: "group", "aria-label": "Metric" },
    Object.entries(metrics).map(([k, m]) => h("button", {
      type: "button", class: k === metric ? "on" : "",
      onclick: () => { prefs.set("metric", k); setQuery("/stats", { ...Object.fromEntries(params), metric: k }); },
    }, m.label)));

  const chartCard = h("div", { class: "card" },
    h("div", { class: "chart-head" }, h("h2", {}, metrics[metric].label + " per day"), metricSeg),
    metric === "cost" && unpriced ? h("p", { class: "muted", style: "margin:0 0 6px" },
      `${unpriced} sessions use models missing from the pricing table and are left out; see \`bossman prices\`.`) : null,
    dailyChart(daily, metrics[metric], since));

  const groupSeg = h("div", { class: "seg", role: "group", "aria-label": "Group by" },
    [["project", "Projects"], ["model", "Models"], ["tool", "Tools"], ["week", "Weeks"]].map(([k, l]) => h("button", {
      type: "button", class: k === by ? "on" : "",
      onclick: () => setQuery("/stats", { ...Object.fromEntries(params), by: k }),
    }, l)));

  view.replaceChildren(
    filterBar(params, "/stats", { range: "30" }),
    tiles,
    chartCard,
    h("div", { class: "card", style: "margin-top:16px" },
      h("div", { class: "chart-head" }, h("h2", {}, "Breakdown"), groupSeg),
      groupTable(by, groups.groups, params)),
  );
}

function dailyChart(series, metric, since) {
  // Fill every day of the range so gaps show as gaps.
  const byDay = new Map();
  for (const s of series) for (const g of s.groups) {
    if (!byDay.has(g.key)) byDay.set(g.key, {});
    byDay.get(g.key)[s.agent] = metric.get(g);
  }
  if (!byDay.size) return h("div", { class: "empty" }, "No sessions in this range.");
  const keys = [...byDay.keys()].sort();
  const start = new Date((since || keys[0]) + "T00:00:00");
  const end = new Date();
  const days = [];
  for (let d = new Date(start); d <= end; d.setDate(d.getDate() + 1)) {
    days.push(d.getFullYear() + "-" + String(d.getMonth() + 1).padStart(2, "0") + "-" + String(d.getDate()).padStart(2, "0"));
  }
  const agents = series.map(s => s.agent);
  const totals = days.map(d => agents.reduce((a, ag) => a + ((byDay.get(d) || {})[ag] || 0), 0));
  const max = Math.max(...totals, 0);
  const W = 1000, H = 260, L = 56, R = 8, T = 10, B = 26;
  const pw = W - L - R, ph = H - T - B;
  const step = pw / days.length;
  const bw = Math.max(2, Math.min(28, step - 2));
  const ticks = niceTicks(max);
  const top = ticks[ticks.length - 1] || 1;
  const y = v => T + ph - (v / top) * ph;

  const svg = s("svg", { viewBox: `0 0 ${W} ${H}`, role: "img",
    "aria-label": `${metric.label} per day, stacked by agent` });
  const grid = s("g", { class: "grid" });
  const axis = s("g", { class: "axis" });
  for (const t of ticks) {
    grid.append(s("line", { x1: L, x2: W - R, y1: y(t), y2: y(t) }));
    axis.append(s("text", { x: L - 6, y: y(t) + 4, "text-anchor": "end" }, metric.fmt(t)));
  }
  const labelEvery = Math.ceil(days.length / 10);
  days.forEach((d, i) => {
    if (i % labelEvery === 0) {
      const dt = new Date(d + "T00:00:00");
      axis.append(s("text", { x: L + i * step + step / 2, y: H - 6, "text-anchor": "middle" },
        dt.toLocaleDateString(undefined, { month: "short", day: "numeric" })));
    }
  });
  svg.append(grid, axis);
  const bars = s("g", {});
  days.forEach((d, i) => {
    const vals = byDay.get(d) || {};
    let acc = 0;
    const x = L + i * step + (step - bw) / 2;
    const present = agents.filter(a => (vals[a] || 0) > 0);
    present.forEach((a, j) => {
      const v = vals[a];
      const y0 = y(acc), y1 = y(acc + v);
      // 2px surface gap between stacked segments; round only the top end.
      const gap = j > 0 ? 2 : 0;
      const hgt = Math.max(0, y0 - y1 - gap);
      const isTop = j === present.length - 1;
      bars.append(s("path", { d: barPath(x, y1, bw, hgt, isTop ? Math.min(4, bw / 2, hgt) : 0), fill: agentColor(a) }));
      acc += v;
    });
    // Hit target: the whole column.
    const hit = s("rect", { x: L + i * step, y: T, width: step, height: ph, fill: "transparent" });
    hit.addEventListener("mousemove", e => showTip(e,
      new Date(d + "T00:00:00").toLocaleDateString(undefined, { weekday: "short", month: "short", day: "numeric" }),
      [...agents.map(a => [a, metric.fmt(vals[a] || 0), agentColor(a)]),
        ...(agents.length > 1 ? [["total", metric.fmt(totals[i])]] : [])]));
    hit.addEventListener("mouseleave", hideTip);
    bars.append(hit);
  });
  svg.append(bars, s("line", { class: "baseline", x1: L, x2: W - R, y1: T + ph, y2: T + ph }));
  const legend = h("div", { class: "legend" }, agents.map(a =>
    h("span", {}, h("span", { class: "sw", style: "background:" + agentColor(a) }), a)));
  return h("div", { class: "chart" }, agents.length > 1 ? legend : null, svg);
}

function barPath(x, y, w, hgt, r) {
  if (hgt <= 0) return "";
  if (!r) return `M${x},${y}h${w}v${hgt}h${-w}z`;
  return `M${x},${y + hgt}V${y + r}Q${x},${y} ${x + r},${y}H${x + w - r}Q${x + w},${y} ${x + w},${y + r}V${y + hgt}z`;
}

function niceTicks(max) {
  if (!(max > 0)) return [0, 1];
  const raw = max / 4;
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const stepv = [1, 2, 2.5, 5, 10].map(m => m * mag).find(s => s >= raw);
  const out = [];
  for (let v = 0; v <= max + stepv * 0.999; v += stepv) out.push(+v.toFixed(10));
  return out;
}

function groupTable(by, groups, params) {
  if (!groups.length) return h("div", { class: "empty" }, "Nothing to show.");
  if (by === "tool") {
    const max = Math.max(...groups.map(g => g.tool_calls));
    return h("div", { class: "table-wrap" }, h("table", {},
      h("thead", {}, h("tr", {}, h("th", {}, "Tool"), h("th", { class: "num" }, "Sessions"), h("th", {}, "Calls"), h("th", { class: "num" }, "Errors"), h("th", { class: "num" }, "Error rate"))),
      h("tbody", {}, groups.slice(0, 60).map(g => h("tr", {},
        h("td", {}, h("code", {}, g.key)),
        h("td", { class: "num" }, g.sessions),
        h("td", {}, inlineBar(g.tool_calls, max, fmt.count(g.tool_calls))),
        h("td", { class: "num" }, g.tool_errors),
        h("td", { class: "num" + (g.tool_calls && g.tool_errors / g.tool_calls >= 0.1 ? " status-bad" : "") }, fmt.pct(g.tool_errors, g.tool_calls)))))));
  }
  if (by === "model") {
    const max = Math.max(...groups.map(g => g.output));
    return h("div", { class: "table-wrap" }, h("table", {},
      h("thead", {}, h("tr", {}, ["Model", "Sessions", "Requests", "Input", "Cache write", "Cache read"].map((c, i) => h("th", { class: i ? "num" : "" }, c)), h("th", {}, "Output"), h("th", { class: "num" }, "Cost"))),
      h("tbody", {}, groups.map(g => h("tr", {},
        h("td", {}, h("code", {}, g.key)),
        h("td", { class: "num" }, g.sessions),
        h("td", { class: "num" }, g.requests),
        h("td", { class: "num" }, fmt.count(g.input)),
        h("td", { class: "num" }, fmt.count(g.cache_write)),
        h("td", { class: "num" }, fmt.count(g.cache_read)),
        h("td", {}, inlineBar(g.output, max, fmt.count(g.output))),
        h("td", { class: "num", title: g.unpriced ? "Not in the pricing table" : "" }, g.unpriced ? "—" : fmt.money(g.cost_usd)))))));
  }
  const max = Math.max(...groups.map(g => g.cost_usd));
  const label = { project: "Project", week: "Week of" }[by] || by;
  const rows = by === "week" ? [...groups].reverse() : groups;
  return h("div", { class: "table-wrap" }, h("table", {},
    h("thead", {}, h("tr", {}, h("th", {}, label), h("th", { class: "num" }, "Sessions"), h("th", {}, "Cost"),
      ["Tokens", "Prompts", "Interv./session", "Tool err.", "Active"].map(c => h("th", { class: "num" }, c)))),
    h("tbody", {}, rows.map(g => h("tr", by === "project" ? {
      class: "clickable", title: "Show these sessions",
      onclick: () => setQuery("/", { project: g.key, agent: params.get("agent") }),
    } : {},
      h("td", { title: g.key }, by === "project" ? fmt.path(g.key) || "(none)" : g.key),
      h("td", { class: "num" }, g.sessions),
      h("td", { title: g.unpriced ? `${g.unpriced} of ${g.sessions} sessions have no price` : "" },
        inlineBar(g.cost_usd, max, g.unpriced === g.sessions ? "—" : fmt.money(g.cost_usd) + (g.unpriced ? "+" : ""))),
      h("td", { class: "num" }, fmt.count(g.input + g.cache_write + g.cache_read + g.output)),
      h("td", { class: "num" }, g.prompts),
      h("td", { class: "num" }, g.sessions ? (g.interventions / g.sessions).toFixed(1) : "—"),
      h("td", { class: "num" }, fmt.pct(g.tool_errors, g.tool_calls)),
      h("td", { class: "num" }, fmt.dur(g.active_s)))))));
}

// ---------- start ----------

loadFacets().catch(() => {}).finally(route);
