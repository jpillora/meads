// Minimal vanilla UI for meads. No framework, no build step.
// Third-party dependencies are vendored so a token-bearing page never executes
// code from another origin.
import { marked } from "./web_vendor/marked.esm.js";
import DOMPurify from "./web_vendor/purify.es.mjs";
import { GitHubMeads } from "./github.js";
import { meadsCore } from "./wasm.js";

const qs = new URLSearchParams(location.search);
const savedSlug = qs.get("repo") || localStorage.getItem("meads.github.repo") || "jpillora/meads";
const [initialOwner, initialRepo] = savedSlug.includes("/") ? savedSlug.split("/", 2) : ["jpillora", "meads"];
const tokenKey = (owner, repo) => `meads.github.token:${owner}/${repo}`;
const initialToken = sessionStorage.getItem(tokenKey(initialOwner, initialRepo)) || "";
const github = new GitHubMeads({ owner: initialOwner, repo: initialRepo, token: initialToken, core: meadsCore });

let state = {
  file: null,
  tasks: [],
  tasksById: new Map(),
  canWrite: false,
  loading: true,
  cached: false,
  filter: "",
  editing: null, // task being edited, or null for new task
  focusedId: null, // id of the keyboard-focused card, or null
  // sort: "default" (status then priority), "priority", "id", or "updated"
  sortBy: localStorage.getItem("meads.sortBy") || "default",
  // groupByStatus: when true, insert a header row between each status bucket
  groupByStatus: localStorage.getItem("meads.groupByStatus") === "1",
  // showClosed: when false (default), tasks with status="closed" are hidden
  showClosed: localStorage.getItem("meads.showClosed") === "1",
  // compact: render one-line table rows instead of cards
  compact: localStorage.getItem("meads.compact") === "1",
};

// pendingDeletes holds task ids in their undo window: hidden from the list, the
// real DELETE fires only when the window lapses (id -> timeout handle).
const pendingDeletes = new Map();

// --- API ---------------------------------------------------------------

const addTask = (task) => github.addTask(task);
const updateTask = (id, task) => github.updateTask(id, task);
const deleteTask = (id) => github.deleteTask(id);
const addDep = (id, parent) => github.addDependency(id, parent);
const removeDep = (id, parent) => github.removeDependency(id, parent);

// --- Render ------------------------------------------------------------

function el(tagOrTpl, ...children) {
  const n = typeof tagOrTpl === "string" ? document.createElement(tagOrTpl) : tagOrTpl;
  for (const c of children) {
    if (c == null || c === false) continue;
    n.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return n;
}

function chip(label, cls) {
  const c = el("span", label);
  c.className = "chip " + cls;
  return c;
}

// taskTags normalises a task's tags to a lowercase string array. The API
// returns them as an array, but a task that has none omits the key entirely,
// and `md` itself accepts a CSV string on the way in - so accept both rather
// than trusting one shape.
function taskTags(t) {
  const raw = t.tags;
  if (!raw) return [];
  const list = Array.isArray(raw) ? raw : String(raw).split(",");
  return list.map((s) => String(s).trim().toLowerCase()).filter(Boolean);
}

// --- Icons -------------------------------------------------------------
// Inline Material Design Icon path data (no build step, no icon font). Used by
// the compact view; the strings are constants — never user input — so building
// SVG markup with them below is safe.
const MDI = {
  circle: "M12,2A10,10 0 0,0 2,12A10,10 0 0,0 12,22A10,10 0 0,0 22,12A10,10 0 0,0 12,2Z",
  bug: "M14,12H10V10H14M14,16H10V14H14M20,8H17.19C16.74,7.22 16.12,6.55 15.37,6.04L17,4.41L15.59,3L13.42,5.17C12.96,5.06 12.5,5 12,5C11.5,5 11.04,5.06 10.59,5.17L8.41,3L7,4.41L8.62,6.04C7.88,6.55 7.26,7.22 6.81,8H4V10H6.09C6.04,10.33 6,10.66 6,11V12H4V14H6V15C6,15.34 6.04,15.67 6.09,16H4V18H6.81C7.85,19.79 9.78,21 12,21C14.22,21 16.15,19.79 17.19,18H20V16H17.91C17.96,15.67 18,15.34 18,15V14H20V12H18V11C18,10.66 17.96,10.33 17.91,10H20V8Z",
  feature: "M12,15.39L8.24,17.66L9.23,13.38L5.91,10.5L10.29,10.13L12,6.09L13.71,10.13L18.09,10.5L14.77,13.38L15.76,17.66M22,9.24L14.81,8.63L12,2L9.19,8.63L2,9.24L7.45,13.97L5.82,21L12,17.27L18.18,21L16.54,13.97L22,9.24Z",
  task: "M19,19H5V5H15V3H5C3.89,3 3,3.89 3,5V19A2,2 0 0,0 5,21H19A2,2 0 0,0 21,19V11H19M7.91,10.08L6.5,11.5L11,16L21,6L19.59,4.58L11,13.17L7.91,10.08Z",
  idea: "M12,2A7,7 0 0,1 19,9C19,11.38 17.81,13.47 16,14.74V17A1,1 0 0,1 15,18H9A1,1 0 0,1 8,17V14.74C6.19,13.47 5,11.38 5,9A7,7 0 0,1 12,2M9,21V20H15V21A1,1 0 0,1 14,22H10A1,1 0 0,1 9,21M12,4A5,5 0 0,0 7,9C7,11.05 8.23,12.81 10,13.58V16H14V13.58C15.77,12.81 17,11.05 17,9A5,5 0 0,0 12,4Z",
};
function typeIconPath(type) { return MDI[type] || MDI.task; }
function svgIcon(path, fill) {
  return `<svg viewBox="0 0 24 24" aria-hidden="true"${fill ? ` fill="${fill}"` : ""}><path d="${path}"></path></svg>`;
}
// statusColor maps a status to a theme variable. A closed task that carries a
// status_reason renders in the danger colour so it reads differently from a
// clean close.
function statusColor(status, hasReason) {
  switch (status) {
    case "open": return "var(--success)";
    case "inprogress": return "var(--accent)";
    case "blocked": return "var(--danger)";
    case "closed": return hasReason ? "var(--danger)" : "var(--muted)";
    default: return "var(--muted)";
  }
}

// --- Markdown ----------------------------------------------------------

// renderMarkdown parses CommonMark/GFM with marked, then sanitises the HTML
// with DOMPurify so any embedded markup can never inject scripts. Cards and the
// editor preview both call this, so they render identically. marked + DOMPurify
// are vendored ESM (see web_vendor/).
marked.setOptions({ gfm: true, breaks: true });
// Open links in a new, isolated tab.
DOMPurify.addHook("afterSanitizeAttributes", (node) => {
  if (node.tagName === "A") {
    node.setAttribute("target", "_blank");
    node.setAttribute("rel", "noopener noreferrer");
  }
});
function renderMarkdown(src) {
  if (!src) return "";
  return DOMPurify.sanitize(marked.parse(src));
}

// unmetDeps returns the parent tasks that still block t: they exist and are not
// closed. Missing parents are treated as satisfied (closed tasks may be pruned
// from the file), mirroring `md ready`.
function unmetDeps(t) {
  if (!Array.isArray(t.depends_on)) return [];
  return t.depends_on
    .map((id) => state.tasksById.get(id))
    .filter((p) => p && (p.status || "open") !== "closed");
}

// isDepBlocked flags an open/inprogress task held back purely by unmet
// dependencies — the readiness state `md ready` filters on, which a manual
// status="blocked" does not capture.
function isDepBlocked(t) {
  const s = t.status || "open";
  if (s !== "open" && s !== "inprogress") return false;
  return unmetDeps(t).length > 0;
}

function taskCard(t) {
  const tpl = document.getElementById("task-card");
  const node = tpl.content.firstElementChild.cloneNode(true);
  const status = t.status || "open";
  const priority = t.priority || "P2";
  const type = t.type || "task";
  node.dataset.id = t.id;
  node.dataset.status = status;
  node.dataset.priority = priority;
  node.id = "task-" + t.id;
  node.tabIndex = 0;
  node.setAttribute("role", "listitem");
  node.setAttribute("aria-label", `Task #${t.id}: ${t.title || "untitled"} — ${status}, ${priority}`);

  node.querySelector(".id").textContent = "#" + t.id;
  node.querySelector(".title").textContent = t.title || "(no title)";

  // Dependency-blocked: unmet (non-closed) parents hold this task back even
  // though its own status is open/inprogress. Distinct from a manual "blocked".
  const blocking = isDepBlocked(t) ? unmetDeps(t) : [];
  if (blocking.length) node.dataset.depBlocked = "true";

  // Status leads (most important state), then priority, then type.
  const chips = node.querySelector(".chips");
  const statusChip = chip(status, "status-" + status + " editable");
  statusChip.dataset.facet = "status";
  statusChip.title = (t.status_reason && (status === "blocked" || status === "closed")) ? t.status_reason : "Change status";
  chips.append(statusChip);
  if (blocking.length) {
    const c = chip("blocked by deps", "dep-blocked");
    c.title = "Not ready — waiting on " + blocking.map((p) => "#" + p.id).join(", ");
    chips.append(c);
  }
  const prChip = chip(priority, priority.toLowerCase() + " editable");
  prChip.dataset.facet = "priority";
  prChip.title = "Change priority";
  chips.append(prChip);
  const typeChip = chip(type, "type-" + type + " editable");
  typeChip.dataset.facet = "type";
  typeChip.title = "Change type";
  chips.append(typeChip);
  // Tags trail the fixed facets: there can be any number of them, and unlike
  // status/priority/type they are not editable in place (a chip menu of every
  // tag in the repo is the wrong shape) - clicking one filters by it instead.
  // No "#" prefix, tempting as it reads: "#" already means a task id here
  // (the .id cell renders "#3", and "#3" is an id filter token), and a tag is
  // allowed to be all digits - so a task tagged "2" would wear a chip reading
  // "#2" that looks exactly like a reference to task 2. The outline style
  // distinguishes these well enough on its own.
  for (const tag of taskTags(t)) {
    const tagChip = chip(tag, "tag");
    tagChip.dataset.tag = tag;
    tagChip.title = `Filter by tag:${tag}`;
    chips.append(tagChip);
  }

  const deps = node.querySelector(".deps");
  if (Array.isArray(t.depends_on)) {
    for (const dep of t.depends_on) {
      const parent = state.tasksById.get(dep);
      // A missing parent has most likely been closed and pruned from the file.
      const satisfied = !parent || (parent.status || "open") === "closed";
      const wrap = el("span");
      wrap.className = "dep" + (satisfied ? " satisfied" : " unmet");
      const label = parent && parent.title ? `↳ #${dep} ${parent.title}` : `↳ #${dep}`;
      const b = el("button", label);
      b.className = "dep-link";
      b.dataset.dep = dep;
      b.title = parent
        ? `#${dep} ${parent.title || ""} — ${parent.status || "open"}`
        : `#${dep} — not in the current list (likely closed)`;
      const x = el("button", "×");
      x.className = "dep-remove";
      x.dataset.action = "remove-dep";
      x.dataset.dep = dep;
      x.title = `Remove dependency on #${dep}`;
      x.setAttribute("aria-label", `Remove dependency on #${dep}`);
      wrap.append(b, x);
      deps.append(wrap);
    }
  }
  // Reverse links: tasks that depend on this one ("blocks #X").
  const blocks = state.tasks.filter((x) => Array.isArray(x.depends_on) && x.depends_on.includes(t.id));
  if (blocks.length) {
    const wrap = el("span");
    wrap.className = "rdep";
    wrap.append(Object.assign(el("span", "blocks"), { className: "rdep-label" }));
    for (const child of blocks) {
      const b = el("button", "#" + child.id);
      b.className = "dep-link rev";
      b.dataset.dep = child.id;
      b.title = `Blocks #${child.id} ${child.title || ""}`;
      b.setAttribute("aria-label", `Blocks #${child.id} ${child.title || ""}`);
      wrap.append(b);
    }
    deps.append(wrap);
  }

  // Primary contextual action (Start/Done/Reopen/…) — not the old blind cycle.
  // Disabled while dependency-blocked; the status menu/editor allow overrides.
  const advance = node.querySelector('[data-action="status-next"]');
  if (advance) {
    advance.textContent = FORWARD_LABEL[status] || "Advance";
    advance.title = `Mark #${t.id} as "${nextStatus(status)}"`;
    if (blocking.length) {
      advance.disabled = true;
      advance.title = "Blocked by unmet dependencies: " + blocking.map((p) => "#" + p.id).join(", ");
    }
  }
  // Explicit status menu: set any status directly (a deliberate override).
  const menu = node.querySelector('[data-action="set-status"]');
  if (menu) {
    for (const s of STATUS_ALL) {
      const opt = el("option", s);
      opt.value = s;
      if (s === status) opt.selected = true;
      menu.append(opt);
    }
    menu.addEventListener("change", () => setStatus(t, menu.value));
  }

  // Description renders full markdown; renderMarkdown sanitises with
  // DOMPurify so innerHTML is safe.
  // Surface the status reason (captured when a task was blocked/closed) above
  // the description; it is otherwise invisible after the prompt.
  if (t.status_reason && (status === "blocked" || status === "closed")) {
    const r = el("div", t.status_reason);
    r.className = "status-reason";
    node.querySelector(".description").before(r);
  }
  node.querySelector(".description").innerHTML = renderMarkdown(t.description || "");
  const ts = taskTimestamp(t);
  if (ts) {
    const span = el("span", ts.label);
    span.className = "card-time";
    span.title = ts.tip;
    node.querySelector("footer").append(span);
  }
  if (!state.canWrite) {
    node.dataset.readonly = "true";
    node.querySelectorAll(".chip.editable").forEach((item) => item.classList.remove("editable"));
    node.querySelectorAll("footer button, footer select, .dep-remove").forEach((control) => {
      control.disabled = true;
      control.title = "Connect a GitHub token with Contents: write permission to edit";
    });
  }
  return node;
}

// colShow toggles the optional compact columns; recomputed each render.
let colShow = { priority: true, tag: false, dep: true };

function tcell(cls, text) {
  const s = el("span", text);
  s.className = "tcell " + cls;
  return s;
}

// taskRow renders one compact one-line row. It mirrors taskCard's data
// attributes (id, status, dep-blocked) so keyboard focus and styling carry over.
// Clicking the row opens the editor, serving as the detail view.
function taskRow(t) {
  const status = t.status || "open";
  const priority = t.priority || "P2";
  const type = t.type || "task";
  const row = el("div");
  row.className = "trow";
  row.dataset.id = t.id;
  row.dataset.status = status;
  row.id = "task-" + t.id;
  row.tabIndex = 0;
  row.setAttribute("role", "listitem");
  row.setAttribute("aria-label", `Task #${t.id}: ${t.title || "untitled"} — ${status}, ${priority}`);

  const blocking = isDepBlocked(t) ? unmetDeps(t) : [];
  if (blocking.length) row.dataset.depBlocked = "true";

  row.append(tcell("tid", "#" + t.id));
  const title = tcell("ttitle", t.title || "(no title)");
  title.title = t.title || "";
  row.append(title);

  const typeCell = tcell("ttype type-" + type, "");
  typeCell.title = type;
  typeCell.setAttribute("aria-label", "type: " + type);
  typeCell.innerHTML = svgIcon(typeIconPath(type));
  row.append(typeCell);

  const statusCell = tcell("tstatus", "");
  const reasonTip = t.status_reason ? " — " + t.status_reason : "";
  statusCell.title = status + reasonTip;
  statusCell.setAttribute("aria-label", "status: " + status + reasonTip);
  statusCell.innerHTML = svgIcon(MDI.circle, statusColor(status, !!t.status_reason));
  row.append(statusCell);

  if (colShow.priority) row.append(tcell("tpri " + priority.toLowerCase(), priority));
  if (colShow.tag) {
    const tags = taskTags(t);
    const cell = tcell("ttag", tags.join(" "));
    if (tags.length) cell.title = "tags: " + tags.join(", ");
    row.append(cell);
  }
  if (colShow.dep) {
    const deps = Array.isArray(t.depends_on) ? t.depends_on : [];
    const cell = tcell("tdep" + (blocking.length ? " unmet" : ""), deps.length ? deps.join(", ") : "");
    if (deps.length) cell.title = "depends on " + deps.map((d) => "#" + d).join(", ");
    row.append(cell);
  }

  row.addEventListener("click", () => {
    if (state.canWrite) openEditor(t);
    else openConnection("Connect a write token to edit tasks.");
  });
  return row;
}

// compactHeader is the sticky column-label row for the compact table.
function compactHeader() {
  const h = el("div");
  h.className = "trow thead";
  h.append(tcell("tid", "ID"), tcell("ttitle", "Title"), tcell("ttype", ""), tcell("tstatus", ""));
  if (colShow.priority) h.append(tcell("tpri", "Pri"));
  if (colShow.tag) h.append(tcell("ttag", "Tags"));
  if (colShow.dep) h.append(tcell("tdep", "Dep"));
  return h;
}

// relativeTime formats an ISO-8601 string as "just now", "Nm ago", "Nh ago",
// "Nd ago", or a locale date for anything older than a month.
function relativeTime(iso) {
  if (!iso) return null;
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return null;
  const ms = Date.now() - t;
  const s = Math.round(ms / 1000);
  if (s < 60) return "just now";
  const m = Math.round(s / 60);
  if (m < 60) return m + "m ago";
  const h = Math.round(m / 60);
  if (h < 24) return h + "h ago";
  const d = Math.round(h / 24);
  if (d < 30) return d + "d ago";
  return new Date(t).toLocaleDateString();
}

// taskTimestamp returns the most useful per-card time. Prefers the update
// stamp when distinct from created (e.g. imported tasks), falls back to
// created, returns null when neither is set.
function taskTimestamp(t) {
  const meta = t.meta || {};
  if (meta.updated && meta.updated !== meta.created) {
    return { label: "updated " + relativeTime(meta.updated), tip: meta.updated };
  }
  if (meta.created) {
    return { label: "created " + relativeTime(meta.created), tip: meta.created };
  }
  return null;
}

// Visual sort order: active work first, blocked, draft, then closed.
const STATUS_RANK = { inprogress: 0, open: 1, blocked: 2, draft: 3, closed: 4 };

function priorityNum(t) { return parseInt((t.priority || "P2").slice(1), 10); }
function statusRank(t) { return STATUS_RANK[t.status] ?? 1; }
function updatedAt(t) {
  // Per-task meta only tracks created today; beads-imported tasks may have updated.
  return (t.meta && (t.meta.updated || t.meta.created)) || "";
}

// sortTasks orders tasks by the user-chosen key. The "default" key is
// priority-within-status (the original behavior). groupByStatus prepends
// status to any other sort so cards bucket cleanly under headers.
function sortTasks(tasks) {
  const byStatus = (a, b) => statusRank(a) - statusRank(b);
  const byPriority = (a, b) => priorityNum(a) - priorityNum(b);
  const byId = (a, b) => a.id - b.id;
  const byUpdatedDesc = (a, b) => {
    const ua = updatedAt(a), ub = updatedAt(b);
    if (ua === ub) return 0;
    if (!ua) return 1;
    if (!ub) return -1;
    return ub.localeCompare(ua);
  };
  const within = {
    priority: [byPriority],
    id: [byId],
    updated: [byUpdatedDesc],
  }[state.sortBy] || [byStatus, byPriority]; // default
  const groupStatus = state.groupByStatus && within[0] !== byStatus;
  const cmps = groupStatus ? [byStatus, ...within, byId] : [...within, byId];
  return tasks.slice().sort((a, b) => {
    for (const c of cmps) {
      const r = c(a, b);
      if (r !== 0) return r;
    }
    return 0;
  });
}

// clampDescription collapses a tall card description to a few lines with a fade
// and a Show more / Show less toggle, leaving short ones untouched. Markdown
// stays fully rendered; expanding just lifts the max-height.
function clampDescription(desc) {
  if (!desc || !desc.textContent.trim()) return;
  desc.classList.add("clamped");
  if (desc.scrollHeight <= desc.clientHeight + 2) {
    desc.classList.remove("clamped"); // already fits — no toggle needed
    return;
  }
  const btn = el("button", "Show more");
  btn.type = "button";
  btn.className = "show-more";
  btn.addEventListener("click", () => {
    const nowClamped = desc.classList.toggle("clamped");
    btn.textContent = nowClamped ? "Show more" : "Show less";
  });
  desc.after(btn);
}

// parseQuery turns the filter string into combinable facets + free-text terms.
// Tokens: status:/s:, type:/t:, priority:/pri:/p:, is:ready|blocked|open|closed,
// tag:a or tag:a,b, id:N or #N. Bare words are free-text (also matched against
// facet values).
//
// "t:" stays type, as it always has; tags get no one-letter form, because
// reassigning "t" would silently change what every existing muscle-memory
// filter means - "t:bug" would stop matching bug-typed tasks and start
// looking for a tag nobody has.
function parseQuery(str) {
  const q = { status: [], type: [], priority: [], is: [], tags: [], ids: [], terms: [] };
  for (const tok of str.trim().toLowerCase().split(/\s+/).filter(Boolean)) {
    let m;
    if ((m = /^(?:status|s):(.+)$/.exec(tok))) q.status.push(m[1]);
    else if ((m = /^(?:type|t):(.+)$/.exec(tok))) q.type.push(m[1]);
    else if ((m = /^(?:priority|pri|p):(.+)$/.exec(tok))) q.priority.push(/^\d$/.test(m[1]) ? "p" + m[1] : m[1]);
    else if ((m = /^is:(.+)$/.exec(tok))) q.is.push(m[1]);
    else if ((m = /^tags?:(.+)$/.exec(tok))) q.tags.push(...m[1].split(",").map((s) => s.trim()).filter(Boolean));
    else if ((m = /^id:(\d+)$/.exec(tok))) q.ids.push(m[1]);
    else if ((m = /^#(\d+)$/.exec(tok))) q.ids.push(m[1]);
    else q.terms.push(tok);
  }
  return q;
}

// matchesQuery ANDs across facets and free-text terms, ORs within one facet.
//
// Tags are the exception: they AND within the facet too, so `tag:api,web-ui`
// wants a task carrying BOTH - matching `md list --tag=a,b`, whose whole point
// is narrowing. ORing them would make the two disagree about the same syntax.
function matchesQuery(t, q) {
  const status = (t.status || "open").toLowerCase();
  const type = (t.type || "task").toLowerCase();
  const pri = (t.priority || "P2").toLowerCase();
  const tags = taskTags(t);
  if (q.status.length && !q.status.includes(status)) return false;
  if (q.type.length && !q.type.includes(type)) return false;
  if (q.priority.length && !q.priority.includes(pri)) return false;
  if (q.tags.length && !q.tags.every((want) => tags.includes(want))) return false;
  if (q.ids.length && !q.ids.includes(String(t.id))) return false;
  for (const v of q.is) {
    if (v === "ready" && !(status === "open" && !isDepBlocked(t))) return false;
    if (v === "blocked" && !(isDepBlocked(t) || status === "blocked")) return false;
    if (v === "open" && status === "closed") return false;
    if (v === "closed" && status !== "closed") return false;
  }
  for (const term of q.terms) {
    const hay = `${t.title || ""} ${t.description || ""}`.toLowerCase();
    if (!(hay.includes(term) || type === term || status === term || pri === term ||
      tags.includes(term) || String(t.id) === term)) return false;
  }
  return true;
}

function renderList() {
  const list = document.getElementById("list");
  list.innerHTML = "";
  const q = parseQuery(state.filter);
  // matches ignores the show-closed filter so closed hits can be reported.
  const matches = state.tasks.filter((t) => !pendingDeletes.has(t.id) && matchesQuery(t, q));
  const visible = matches.filter((t) => state.showClosed || (t.status || "open") !== "closed");
  renderMeta(visible.length);
  if (visible.length === 0) {
    const empty = el("div");
    empty.className = "empty";
    const hiddenClosed = matches.length - visible.length;
    if (state.tasks.length === 0) {
      empty.textContent = "No tasks yet. Add one with the button above.";
    } else if (hiddenClosed > 0) {
      empty.append(`No open matches — ${hiddenClosed} closed ${hiddenClosed === 1 ? "task is" : "tasks are"} hidden. `);
      const b = el("button", "Show closed");
      b.className = "link-btn";
      b.addEventListener("click", () => {
        state.showClosed = true;
        const cb = document.getElementById("show-closed");
        if (cb) cb.checked = true;
        localStorage.setItem("meads.showClosed", "1");
        renderList();
      });
      empty.append(b);
    } else {
      empty.textContent = "No matches.";
    }
    list.append(empty);
    state.focusedId = null;
    return;
  }
  const sorted = sortTasks(visible);
  // Compact mode: switch the row renderer and recompute the optional columns
  // (hide priority when every visible task shares one; hide tags and deps when
  // none - a repo that uses neither should not pay two empty columns for them).
  list.classList.toggle("compact", state.compact);
  if (state.compact) {
    const firstPri = visible[0].priority || "P2";
    colShow.priority = visible.some((t) => (t.priority || "P2") !== firstPri);
    colShow.tag = visible.some((t) => taskTags(t).length > 0);
    colShow.dep = visible.some((t) => Array.isArray(t.depends_on) && t.depends_on.length > 0);
    list.append(compactHeader());
  }
  const renderTask = state.compact ? taskRow : taskCard;
  if (state.groupByStatus) {
    let prev = null;
    for (const t of sorted) {
      const s = t.status || "open";
      if (s !== prev) {
        const header = el("div", `${s} (${sorted.filter((x) => (x.status || "open") === s).length})`);
        header.className = "group-header";
        header.dataset.status = s;
        list.append(header);
        prev = s;
      }
      list.append(renderTask(t));
    }
  } else {
    for (const t of sorted) list.append(renderTask(t));
  }
  // Clamp long card descriptions (compact rows have none).
  if (!state.compact) {
    for (const desc of list.querySelectorAll(".card .description")) clampDescription(desc);
  }
  // Drop stale focus, then re-paint the focus attribute on the new DOM.
  if (state.focusedId != null && !visible.some((t) => t.id === state.focusedId)) {
    state.focusedId = null;
  }
  updateFocusVisual();
}

function renderMeta(matched) {
  document.getElementById("file-path").textContent = state.file ? state.file.path : "TASKS.md";
  const meta = document.getElementById("file-meta");
  if (!state.file) { meta.textContent = ""; return; }
  const total = state.file.task_count;
  const partial = matched != null && matched !== total;
  const count = partial
    ? `${matched} of ${total} task${total === 1 ? "" : "s"}`
    : `${total} task${total === 1 ? "" : "s"}`;
  const rel = relativeTime(state.file.updated_at);
  meta.textContent = `· ${count}${rel ? " · updated " + rel : ""}${state.cached ? " · cached" : ""}`;
  meta.title = state.file.updated_at || "";
}

// --- Actions -----------------------------------------------------------

// All statuses, in workflow order — used to populate the per-card status menu.
const STATUS_ALL = ["draft", "open", "inprogress", "blocked", "closed"];
// FORWARD is the primary-action transition for each status: a sensible linear
// flow that never lands on "blocked" (a deliberate side state reachable only via
// the status menu) and never wraps closed back to draft.
const FORWARD = {
  draft: "open",
  open: "inprogress",
  inprogress: "closed",
  blocked: "inprogress",
  closed: "open",
};
// Verb shown on the primary action button for each status.
const FORWARD_LABEL = {
  draft: "Open",
  open: "Start",
  inprogress: "Done",
  blocked: "Resume",
  closed: "Reopen",
};
function nextStatus(cur) { return FORWARD[cur] || "open"; }

// applyStatus PATCHes a task to an explicit status. Moving to "blocked" or
// "closed" prompts for an optional reason (Cancel aborts and returns false; an
// empty string leaves the existing reason untouched — status_reason is
// omitempty server-side).
async function applyStatus(task, status) {
  const body = { id: task.id, status };
  if (status === "blocked" || status === "closed") {
    const prior = task.status_reason || "";
    const reason = await promptDialog({
      title: `Mark #${task.id} ${status}`,
      label: "Reason (optional)",
      value: prior,
      rows: 3,
    });
    if (reason === null) return false; // user cancelled
    if (reason.trim() !== "") body.status_reason = reason.trim();
  }
  await updateTask(task.id, body);
  await reload();
  toast(`Task #${task.id} → ${status}`);
  return true;
}

// advanceStatus runs the primary contextual action (Start/Done/Reopen/…). It is
// refused while the task has unmet dependencies; use the status menu or the
// editor for a deliberate override.
async function advanceStatus(task) {
  if (isDepBlocked(task)) {
    toast(`#${task.id} is blocked by unmet dependencies: ${unmetDeps(task).map((p) => "#" + p.id).join(", ")}`, "err");
    return;
  }
  await applyStatus(task, nextStatus(task.status || "open"));
}

// setStatus applies an explicit status chosen from the per-card menu; re-renders
// on cancel so the <select> snaps back to the task's real status.
async function setStatus(task, status) {
  if (status === (task.status || "open")) return;
  const ok = await applyStatus(task, status);
  if (!ok) renderList();
}

// updateReasonVisibility shows the editor's status-reason field only when the
// chosen status is blocked or closed.
function updateReasonVisibility() {
  const form = document.getElementById("editor-form");
  const field = document.getElementById("reason-field");
  if (!form || !field) return;
  const s = form.status.value;
  field.hidden = !(s === "blocked" || s === "closed");
}

// deriveTitleDesc splits an editor body into title + description, matching
// `md add`: the title is the text before the first ". " (period-space) or
// newline; the rest is the description.
function deriveTitleDesc(body) {
  const b = (body || "").trim();
  const m = /\. |\n/.exec(b);
  if (!m) return { title: b, description: "" };
  return { title: b.slice(0, m.index).trim(), description: b.slice(m.index + m[0].length).trim() };
}

// composeBody joins a task's title + description back into one editable body.
function composeBody(task) {
  if (!task) return "";
  const title = task.title || "";
  const desc = task.description || "";
  return desc ? `${title}\n${desc}` : title;
}

const DRAFT_KEY = "meads.draft";

// saveDraft persists the in-progress New task form so an accidental close or
// reload does not lose work. Only the New task form is saved (not edits), and
// only when it differs from a pristine form.
function saveDraft() {
  if (state.editing) return;
  const form = document.getElementById("editor-form");
  if (!form) return;
  const d = {
    body: form.description.value,
    type: form.type.value,
    priority: form.priority.value,
    status: form.status.value,
    tags: form.tags.value,
  };
  if (d.body.trim() || d.tags.trim() || d.type !== "task" || d.priority !== "P2" || d.status !== "open") {
    localStorage.setItem(DRAFT_KEY, JSON.stringify(d));
  } else {
    localStorage.removeItem(DRAFT_KEY);
  }
}

// loadDraft restores a saved New task draft into the form.
function loadDraft() {
  try {
    const raw = localStorage.getItem(DRAFT_KEY);
    if (!raw) return;
    const d = JSON.parse(raw);
    const form = document.getElementById("editor-form");
    if (typeof d.body === "string") form.description.value = d.body;
    if (d.type) form.type.value = d.type;
    if (d.priority) form.priority.value = d.priority;
    if (d.status) form.status.value = d.status;
    if (typeof d.tags === "string") form.tags.value = d.tags;
  } catch { /* ignore a malformed draft */ }
}

function clearDraft() { localStorage.removeItem(DRAFT_KEY); }

// allTags is every tag currently in use, sorted - the vocabulary the editor
// offers as suggestions. Derived from the loaded tasks rather than fetched:
// there is no tags endpoint, and a tag only exists by being on a task.
//
// Filtered to what the server would accept, unlike taskTags, which shows
// whatever is stored. Tags are validated on write and not on read
// (pkg/meads/tags.go), so a hand-edited TASKS.md can hold a tag no write will
// ever accept - offering it as a suggestion would be inviting a 400.
function allTags() {
  const seen = new Set();
  for (const t of state.tasks) {
    for (const tag of taskTags(t)) if (/^[a-z0-9-]+$/.test(tag)) seen.add(tag);
  }
  return [...seen].sort();
}

// refreshTagSuggestions repopulates the editor's datalist. A datalist rather
// than a custom typeahead: the browser gives free filtering and keyboard
// handling, and this is one input, not the dep picker's multi-select.
function refreshTagSuggestions() {
  const list = document.getElementById("tag-suggestions");
  if (!list) return;
  list.innerHTML = "";
  for (const tag of allTags()) {
    const opt = el("option");
    opt.value = tag;
    list.append(opt);
  }
}

// parseTagsInput splits the editor's comma-separated field. It only splits and
// lowercases - the server normalises and rejects anything malformed (see
// meads.Tags.Normalize), and duplicating that ruleset here would just be a
// second copy to drift.
function parseTagsInput(raw) {
  return String(raw || "")
    .split(",")
    .map((s) => s.trim().toLowerCase())
    .filter(Boolean);
}

function openEditor(task) {
  state.editing = task;
  const dlg = document.getElementById("editor");
  const form = document.getElementById("editor-form");
  form.reset();
  document.getElementById("editor-title").textContent = task ? `Edit task #${task.id}` : "New task";
  if (task) {
    form.type.value = task.type || "task";
    form.priority.value = task.priority || "P2";
    form.status.value = task.status || "open";
    form.status_reason.value = task.status_reason || "";
    form.tags.value = taskTags(task).join(", ");
  }
  refreshTagSuggestions();
  form.description.value = composeBody(task);
  if (!task) loadDraft(); // restore an in-progress New task draft
  updateReasonVisibility();
  setEditingDeps(task ? (task.depends_on || []) : []);
  document.getElementById("dep-search").value = "";
  hideDepResults();
  renderPreview();
  dlg.showModal();
  form.description.focus();
}

// --- Dependency picker --------------------------------------------------

function setEditingDeps(ids) {
  const chips = document.getElementById("dep-chips");
  chips.innerHTML = "";
  for (const id of ids) {
    const t = state.tasks.find((x) => x.id === id);
    const label = t ? `#${id} ${t.title}` : `#${id}`;
    const chip = el("span");
    chip.className = "dep-chip";
    chip.dataset.depId = id;
    const text = el("span", label);
    const rm = el("button", "×");
    rm.type = "button";
    rm.className = "dep-chip-remove";
    rm.title = "Remove";
    rm.addEventListener("click", () => {
      chip.remove();
      syncHiddenDeps();
    });
    chip.append(text, rm);
    chips.append(chip);
  }
  syncHiddenDeps();
}

function syncHiddenDeps() {
  const ids = Array.from(document.querySelectorAll("#dep-chips .dep-chip"))
    .map((c) => parseInt(c.dataset.depId, 10));
  document.getElementById("depends_on").value = ids.join(",");
}

function currentDepIds() {
  return Array.from(document.querySelectorAll("#dep-chips .dep-chip"))
    .map((c) => parseInt(c.dataset.depId, 10));
}

function hideDepResults() {
  const r = document.getElementById("dep-results");
  r.hidden = true;
  r.innerHTML = "";
}

function renderDepResults(query) {
  const r = document.getElementById("dep-results");
  const q = query.trim().toLowerCase();
  const selfId = state.editing ? state.editing.id : 0;
  const taken = new Set(currentDepIds());
  const matches = state.tasks
    .filter((t) => t.id !== selfId && !taken.has(t.id))
    .filter((t) => {
      if (!q) return true;
      return String(t.id) === q
        || (t.title || "").toLowerCase().includes(q);
    })
    .slice(0, 8);
  if (matches.length === 0) { hideDepResults(); return; }
  r.innerHTML = "";
  for (const t of matches) {
    const row = el("button");
    row.type = "button";
    row.className = "dep-result";
    row.dataset.depId = t.id;
    row.append(
      Object.assign(el("span", "#" + t.id), { className: "dep-result-id" }),
      Object.assign(el("span", t.title || "(no title)"), { className: "dep-result-title" }),
    );
    r.append(row);
  }
  r.hidden = false;
}

function addEditingDep(id) {
  const ids = currentDepIds();
  if (ids.includes(id)) return;
  setEditingDeps([...ids, id]);
}

// --- Markdown editor toolbar -------------------------------------------

function descriptionInput() {
  return document.getElementById("editor-description");
}

function renderPreview() {
  const ta = descriptionInput();
  const preview = document.querySelector(".md-preview");
  if (!ta || !preview) return;
  preview.innerHTML = renderMarkdown(ta.value);
  preview.classList.toggle("empty", ta.value.trim() === "");
  const dt = document.getElementById("derived-title");
  if (dt) {
    const { title } = deriveTitleDesc(ta.value);
    dt.textContent = ta.value.trim() ? `Title: ${title || "Untitled"}` : "";
  }
}

function wrapSelection(ta, before, after, placeholder) {
  after = after ?? before;
  placeholder = placeholder ?? "";
  const start = ta.selectionStart;
  const end = ta.selectionEnd;
  const val = ta.value;
  const selected = val.slice(start, end);
  const inner = selected || placeholder;
  ta.value = val.slice(0, start) + before + inner + after + val.slice(end);
  const innerStart = start + before.length;
  ta.setSelectionRange(innerStart, innerStart + inner.length);
  ta.focus();
  ta.dispatchEvent(new Event("input", { bubbles: true }));
}

function prefixLines(ta, prefix) {
  const start = ta.selectionStart;
  const end = ta.selectionEnd;
  const val = ta.value;
  const blockStart = val.lastIndexOf("\n", start - 1) + 1;
  const blockEnd = val.indexOf("\n", end);
  const stop = blockEnd === -1 ? val.length : blockEnd;
  const block = val.slice(blockStart, stop);
  let i = 0;
  const transformed = block.split("\n").map((line) => {
    i += 1;
    return prefix.replace("{n}", String(i)) + line;
  }).join("\n");
  ta.value = val.slice(0, blockStart) + transformed + val.slice(stop);
  ta.setSelectionRange(blockStart, blockStart + transformed.length);
  ta.focus();
  ta.dispatchEvent(new Event("input", { bubbles: true }));
}

// Cycle the heading level on each selected line: none → # → ## → ### → none.
// Single-line selections (the common case) just transform the cursor's line.
function cycleHeading(ta) {
  const start = ta.selectionStart;
  const end = ta.selectionEnd;
  const val = ta.value;
  const blockStart = val.lastIndexOf("\n", start - 1) + 1;
  const blockEnd = val.indexOf("\n", end);
  const stop = blockEnd === -1 ? val.length : blockEnd;
  const transformed = val.slice(blockStart, stop).split("\n").map((line) => {
    const m = /^(#{1,3}) /.exec(line);
    if (!m) return "# " + line;
    if (m[1] === "#") return "## " + line.slice(2);
    if (m[1] === "##") return "### " + line.slice(3);
    return line.slice(4);
  }).join("\n");
  ta.value = val.slice(0, blockStart) + transformed + val.slice(stop);
  ta.setSelectionRange(blockStart, blockStart + transformed.length);
  ta.focus();
  ta.dispatchEvent(new Event("input", { bubbles: true }));
}

function applyMarkdownAction(action) {
  const ta = descriptionInput();
  if (!ta) return;
  switch (action) {
    case "bold": return wrapSelection(ta, "**", "**", "bold text");
    case "italic": return wrapSelection(ta, "*", "*", "italic text");
    case "code": return wrapSelection(ta, "`", "`", "code");
    case "link": {
      promptDialog({ title: "Insert link", label: "Link URL", value: "https://", rows: 1 })
        .then((url) => {
          if (url == null) return;
          wrapSelection(ta, "[", `](${url.trim()})`, "link text");
        });
      return;
    }
    case "ul": return prefixLines(ta, "- ");
    case "ol": return prefixLines(ta, "{n}. ");
    case "heading": return cycleHeading(ta);
  }
}

async function submitEditor(ev) {
  ev.preventDefault();
  const form = ev.target;
  const fd = new FormData(form);
  const depsRaw = String(fd.get("depends_on") || "").trim();
  const { title, description } = deriveTitleDesc(String(fd.get("description") || ""));
  const data = {
    title: title || "Untitled",
    type: String(fd.get("type") || ""),
    priority: String(fd.get("priority") || ""),
    status: String(fd.get("status") || ""),
    description,
  };
  // "tags" is sent only when it actually changed, which still covers clearing:
  // an empty field differs from a non-empty task, so [] is sent and the API
  // reads a present-but-empty "tags" as "clear them" (handlers.go).
  //
  // Sending it unconditionally looked simpler and was wrong. Tags are
  // validated at the input boundary, not on read (pkg/meads/tags.go), so a
  // value that predates the rule or was hand-edited into TASKS.md still
  // loads - but re-submitting it makes the server reject it. Editing only the
  // description of such a task would fail with "invalid tag", and the user
  // would have to repair a tag they never touched before their edit could
  // land. Omitting an unchanged key leaves it alone, which is what PATCH is
  // for.
  const tags = parseTagsInput(fd.get("tags"));
  if (!state.editing || tags.join(",") !== taskTags(state.editing).join(",")) {
    data.tags = tags;
  }
  // status_reason applies only to blocked/closed; the server keeps the prior
  // value when this is empty (the API cannot clear it).
  if (data.status === "blocked" || data.status === "closed") {
    const reason = String(fd.get("status_reason") || "").trim();
    if (reason) data.status_reason = reason;
  }
  try {
    let msg;
    if (state.editing) {
      await updateTask(state.editing.id, { id: state.editing.id, ...data });
      await syncDeps(state.editing, depsRaw);
      msg = `Task #${state.editing.id} saved`;
    } else {
      const { id } = await addTask(data);
      clearDraft();
      await syncDeps({ id, depends_on: [] }, depsRaw);
      msg = `Task #${id} added`;
    }
    document.getElementById("editor").close();
    await reload();
    toast(msg);
  } catch (err) {
    toast(err.message, "err");
  }
}

async function syncDeps(task, raw) {
  const wanted = raw
    .split(",")
    .map((s) => parseInt(s.trim(), 10))
    .filter((n) => Number.isInteger(n) && n > 0 && n !== task.id);
  const current = Array.isArray(task.depends_on) ? task.depends_on : [];
  const toAdd = wanted.filter((n) => !current.includes(n));
  const toRemove = current.filter((n) => !wanted.includes(n));
  for (const parent of toAdd) {
    try { await addDep(task.id, parent); } catch (err) { toast(err.message, "err"); }
  }
  for (const parent of toRemove) {
    try { await removeDep(task.id, parent); } catch (err) { toast(err.message, "err"); }
  }
}

// promptDialog shows the styled in-app prompt and resolves with the entered
// text (OK / Enter) or null (Cancel / Esc), replacing window.prompt.
function promptDialog({ title, label, value = "", rows = 3 }) {
  return new Promise((resolve) => {
    const dlg = document.getElementById("prompt");
    const form = document.getElementById("prompt-form");
    const field = document.getElementById("prompt-field");
    dlg.querySelector(".prompt-title").textContent = title;
    dlg.querySelector(".prompt-label").textContent = label;
    field.value = value;
    field.rows = rows;
    const cancelBtn = dlg.querySelector('[data-action="prompt-cancel"]');
    let settled = false;
    const finish = (val) => {
      if (settled) return;
      settled = true;
      form.removeEventListener("submit", onSubmit);
      dlg.removeEventListener("cancel", onCancel);
      cancelBtn.removeEventListener("click", onCancel);
      field.removeEventListener("keydown", onKey);
      dlg.close();
      resolve(val);
    };
    const onSubmit = (e) => { e.preventDefault(); finish(field.value); };
    const onCancel = (e) => { if (e) e.preventDefault(); finish(null); };
    // Enter submits single-line prompts; Ctrl/Cmd+Enter submits multiline ones.
    const onKey = (e) => {
      if (e.key === "Enter" && (rows === 1 || e.ctrlKey || e.metaKey)) { e.preventDefault(); finish(field.value); }
    };
    form.addEventListener("submit", onSubmit);
    dlg.addEventListener("cancel", onCancel);
    cancelBtn.addEventListener("click", onCancel);
    field.addEventListener("keydown", onKey);
    dlg.showModal();
    field.focus();
    field.select();
  });
}

function toast(msg, kind, action) {
  const t = el("div", msg);
  t.className = "toast" + (kind === "err" ? " err" : " ok");
  if (action) {
    const b = el("button", action.label);
    b.className = "toast-action";
    b.addEventListener("click", () => { t.remove(); action.fn(); });
    t.append(" ", b);
  }
  document.body.append(t);
  setTimeout(() => t.remove(), action ? 6000 : kind === "err" ? 4000 : 2500);
}

// deleteWithUndo hides the task immediately and shows an Undo toast; the real
// DELETE only fires once the undo window lapses, so undo needs no server call
// and the task keeps its id (and inbound dependencies).
function deleteWithUndo(task) {
  if (pendingDeletes.has(task.id)) return;
  const commit = setTimeout(async () => {
    pendingDeletes.delete(task.id);
    try { await deleteTask(task.id); await reload(); }
    catch (err) { toast(err.message, "err"); renderList(); }
  }, 6000);
  pendingDeletes.set(task.id, commit);
  renderList();
  toast(`Task #${task.id} deleted`, "ok", {
    label: "Undo",
    fn: () => { clearTimeout(commit); pendingDeletes.delete(task.id); renderList(); },
  });
}

// --- Chip quick-edit ---------------------------------------------------
const PRIORITIES = ["P0", "P1", "P2", "P3", "P4"];
const TYPES = ["task", "bug", "feature", "idea"];
let openMenu = null;

function closeChipMenu() {
  if (!openMenu) return;
  openMenu.el.remove();
  window.removeEventListener("scroll", openMenu.onScroll, true);
  document.removeEventListener("keydown", openMenu.onKey, true);
  openMenu = null;
}

// openChipMenu floats a small option list under a chip; picking one applies it.
function openChipMenu(anchor, task, facet, options) {
  closeChipMenu();
  const menu = el("div");
  menu.className = "chip-menu";
  for (const opt of options) {
    const b = el("button", opt);
    b.type = "button";
    b.className = "chip-menu-item" + (opt === (task[facet] || "") ? " current" : "");
    b.addEventListener("click", (e) => { e.stopPropagation(); closeChipMenu(); quickEdit(task, facet, opt); });
    menu.append(b);
  }
  document.body.append(menu);
  const r = anchor.getBoundingClientRect();
  menu.style.left = `${Math.round(r.left)}px`;
  menu.style.top = `${Math.round(r.bottom + 4)}px`;
  const onScroll = () => closeChipMenu();
  const onKey = (e) => { if (e.key === "Escape") { e.stopPropagation(); closeChipMenu(); } };
  window.addEventListener("scroll", onScroll, true);
  document.addEventListener("keydown", onKey, true);
  openMenu = { el: menu, onScroll, onKey };
}

// quickEdit applies a chip-chosen value. Status reuses setStatus (reason prompt
// for blocked/closed); priority and type PATCH directly.
async function quickEdit(task, facet, value) {
  if (facet === "status") return setStatus(task, value);
  if (value === (task[facet] || "")) return;
  try {
    await updateTask(task.id, { id: task.id, [facet]: value });
    await reload();
    toast(`Task #${task.id} ${facet} → ${value}`);
  } catch (err) { toast(err.message, "err"); }
}

// --- Dependency graph --------------------------------------------------
const SVGNS = "http://www.w3.org/2000/svg";
function svgEl(tag, attrs) {
  const e = document.createElementNS(SVGNS, tag);
  for (const k in attrs) e.setAttribute(k, attrs[k]);
  return e;
}

// nodeState classifies a task for graph node colouring.
function nodeState(t) {
  const s = t.status || "open";
  if (s === "closed") return "closed";
  if (isDepBlocked(t)) return "blocked";
  if (s === "inprogress") return "inprogress";
  if (s === "open") return "ready";
  return "other";
}

// openGraph renders the dependency DAG: every task that has, or is, a dependency,
// laid out in longest-path layers (prerequisites left, dependents right).
function openGraph() {
  const dlg = document.getElementById("graph");
  const body = dlg.querySelector(".graph-body");
  body.innerHTML = "";
  const byId = state.tasksById;
  const depended = new Set();
  for (const t of state.tasks) for (const p of (t.depends_on || [])) depended.add(p);
  const nodes = state.tasks.filter((t) => (t.depends_on && t.depends_on.length) || depended.has(t.id));
  if (nodes.length === 0) {
    body.append(Object.assign(el("div", "No dependencies yet."), { className: "empty" }));
    dlg.showModal();
    return;
  }
  // Longest-path level: 0 for tasks with no present deps, else 1 + max(parent).
  const level = new Map();
  const lvl = (t, seen) => {
    if (level.has(t.id)) return level.get(t.id);
    if (seen.has(t.id)) return 0; // cycle guard
    seen.add(t.id);
    const parents = (t.depends_on || []).map((id) => byId.get(id)).filter(Boolean);
    const v = parents.length ? 1 + Math.max(...parents.map((p) => lvl(p, seen))) : 0;
    seen.delete(t.id);
    level.set(t.id, v);
    return v;
  };
  for (const t of nodes) lvl(t, new Set());
  const cols = [];
  for (const t of nodes) (cols[level.get(t.id)] ||= []).push(t);
  const COLW = 175, ROWH = 50, NODEW = 145, NODEH = 32, PAD = 16;
  const pos = new Map();
  let maxRows = 0;
  cols.forEach((col, L) => {
    maxRows = Math.max(maxRows, col.length);
    col.forEach((t, i) => pos.set(t.id, { x: L * COLW + PAD, y: i * ROWH + PAD }));
  });
  const w = (cols.length - 1) * COLW + NODEW + 2 * PAD;
  const h = maxRows * ROWH + PAD;
  const svg = svgEl("svg", { width: w, height: h, viewBox: `0 0 ${w} ${h}`, class: "dep-graph" });
  for (const t of nodes) {
    const c = pos.get(t.id);
    for (const pid of (t.depends_on || [])) {
      const p = pos.get(pid);
      if (!p) continue;
      const parent = byId.get(pid);
      const unmet = parent && (parent.status || "open") !== "closed";
      svg.append(svgEl("line", {
        x1: p.x + NODEW, y1: p.y + NODEH / 2, x2: c.x, y2: c.y + NODEH / 2,
        class: "dep-edge" + (unmet ? " unmet" : ""),
      }));
    }
  }
  for (const t of nodes) {
    const c = pos.get(t.id);
    const g = svgEl("g", { class: "dep-node " + nodeState(t), "data-id": t.id, tabindex: "0" });
    g.append(svgEl("rect", { x: c.x, y: c.y, width: NODEW, height: NODEH, rx: 6 }));
    const text = svgEl("text", { x: c.x + 8, y: c.y + NODEH / 2 + 4 });
    const label = `#${t.id} ${t.title || ""}`;
    text.textContent = label.length > 20 ? label.slice(0, 19) + "…" : label;
    g.append(text);
    const tip = svgEl("title");
    tip.textContent = `#${t.id} ${t.title || ""} — ${t.status || "open"}`;
    g.append(tip);
    svg.append(g);
  }
  body.append(svg);
  dlg.showModal();
}

// --- Event delegation --------------------------------------------------

// Open/close the narrow-viewport kebab dropdown. Outside-click closes via the
// listener below; the toggle button itself flips the data attribute.
function setOverflowOpen(open) {
  const actions = document.querySelector(".bar .actions");
  const toggle = document.getElementById("overflow-toggle");
  if (!actions || !toggle) return;
  if (open) actions.setAttribute("data-overflow-open", "");
  else actions.removeAttribute("data-overflow-open");
  toggle.setAttribute("aria-expanded", open ? "true" : "false");
}

document.addEventListener("click", async (e) => {
  // Outside-click closes the overflow menu (before the early-return below).
  const inActions = e.target.closest(".bar .actions");
  if (!inActions) setOverflowOpen(false);

  // Chip quick-edit popover: open on an editable chip, close on any click that
  // is neither an editable chip nor inside the open menu.
  const editChip = e.target.closest(".chip.editable");
  if (!editChip && !e.target.closest(".chip-menu")) closeChipMenu();

  // Tag chip: narrow the filter to that tag. Deliberately AFTER the close
  // above, not before it: the menu is position:fixed and a tag click
  // re-renders the list under it, so returning early would leave the menu
  // hovering over whatever card now occupies those coordinates - still bound
  // to the task it was opened on, which the filter may well have hidden. The
  // next click would then edit an invisible task.
  const tagChip = e.target.closest(".chip.tag[data-tag]");
  if (tagChip) {
    toggleTagFilter(tagChip.dataset.tag);
    return;
  }

  if (editChip) {
    const card = editChip.closest("[data-id]");
    const task = card && state.tasks.find((t) => t.id === parseInt(card.dataset.id, 10));
    if (task) {
      const facet = editChip.dataset.facet;
      const opts = facet === "status" ? STATUS_ALL : facet === "priority" ? PRIORITIES : TYPES;
      openChipMenu(editChip, task, facet, opts);
    }
    return;
  }

  // Graph node click → jump to the task's card.
  const gnode = e.target.closest(".dep-node");
  if (gnode) {
    document.getElementById("graph").close();
    const card = document.getElementById("task-" + gnode.dataset.id);
    if (card) { card.scrollIntoView({ block: "center" }); if (card.focus) card.focus(); }
    return;
  }

  const btn = e.target.closest("button");
  if (!btn) return;
  const card = e.target.closest(".card");
  const id = card ? parseInt(card.dataset.id, 10) : 0;
  const action = btn.dataset.action;

  if (btn.id === "overflow-toggle") {
    const actions = document.querySelector(".bar .actions");
    setOverflowOpen(!actions.hasAttribute("data-overflow-open"));
    return;
  }
  if (btn.id === "new-task") {
    setOverflowOpen(false);
    if (!state.canWrite) return openConnection("Connect a write token to create tasks.");
    return openEditor(null);
  }
  if (btn.id === "help-toggle") { setOverflowOpen(false); return toggleHelp(); }
  if (btn.id === "connect-github") { setOverflowOpen(false); return openConnection(); }
  if (btn.id === "refresh") {
    setOverflowOpen(false);
    return reload().catch((err) => showLoadError(err));
  }
  if (btn.id === "graph-toggle") { setOverflowOpen(false); return openGraph(); }
  if (action === "close-help") {
    document.getElementById("help").close();
    return;
  }
  if (action === "close-graph") {
    document.getElementById("graph").close();
    return;
  }
  if (action === "cancel") {
    if (!state.editing) clearDraft(); // explicit discard of the New task draft
    document.getElementById("editor").close();
    return;
  }
  if (action === "remove-dep") {
    const parent = parseInt(btn.dataset.dep, 10);
    if (!id || !parent) return;
    try { await removeDep(id, parent); await reload(); }
    catch (err) { toast(err.message, "err"); }
    return;
  }
  if (btn.dataset.dep) {
    const dep = btn.dataset.dep;
    const target = document.getElementById("task-" + dep);
    if (target) target.scrollIntoView({ behavior: "smooth", block: "center" });
    return;
  }
  if (!id || !action) return;
  const task = state.tasks.find((t) => t.id === id);
  if (!task) return;
  try {
    if (action === "edit") openEditor(task);
    if (action === "status-next") await advanceStatus(task);
    if (action === "delete") deleteWithUndo(task);
  } catch (err) {
    toast(err.message, "err");
  }
});

document.getElementById("editor-form").addEventListener("submit", submitEditor);
document.getElementById("editor-form").addEventListener("input", saveDraft);
document.getElementById("editor-form").addEventListener("change", saveDraft);
document.querySelector('#editor-form [name="status"]')?.addEventListener("change", updateReasonVisibility);
document.getElementById("filter").addEventListener("input", (e) => {
  state.filter = e.target.value;
  renderList();
});

// toggleTagFilter adds or removes one `tag:<name>` token in the filter box,
// leaving every other token alone - so clicking a tag narrows an existing
// filter rather than replacing it, and clicking the same tag again undoes it.
function toggleTagFilter(tag) {
  const input = document.getElementById("filter");
  const token = "tag:" + tag;
  const tokens = state.filter.trim().split(/\s+/).filter(Boolean);
  // A tag can also be sitting inside a comma list from an earlier click, e.g.
  // "tag:api,web-ui"; splitting those out keeps the toggle exact.
  const flat = tokens.flatMap((tok) => {
    const m = /^tags?:(.+)$/i.exec(tok);
    if (!m) return [tok];
    return m[1].split(",").map((s) => s.trim()).filter(Boolean).map((s) => "tag:" + s.toLowerCase());
  });
  const next = flat.includes(token) ? flat.filter((tok) => tok !== token) : [...flat, token];
  state.filter = next.join(" ");
  input.value = state.filter;
  renderList();
}

// Sort + group controls — initialise from state, persist on change.
const sortSelect = document.getElementById("sort-by");
const groupCheck = document.getElementById("group-by-status");
if (sortSelect) {
  sortSelect.value = state.sortBy;
  sortSelect.addEventListener("change", (e) => {
    state.sortBy = e.target.value;
    localStorage.setItem("meads.sortBy", state.sortBy);
    renderList();
  });
}
if (groupCheck) {
  groupCheck.checked = state.groupByStatus;
  groupCheck.addEventListener("change", (e) => {
    state.groupByStatus = e.target.checked;
    localStorage.setItem("meads.groupByStatus", state.groupByStatus ? "1" : "0");
    renderList();
  });
}
const showClosed = document.getElementById("show-closed");
if (showClosed) {
  showClosed.checked = state.showClosed;
  showClosed.addEventListener("change", (e) => {
    state.showClosed = e.target.checked;
    localStorage.setItem("meads.showClosed", state.showClosed ? "1" : "0");
    renderList();
  });
}
const compactToggle = document.getElementById("compact");
if (compactToggle) {
  compactToggle.checked = state.compact;
  compactToggle.addEventListener("change", (e) => {
    state.compact = e.target.checked;
    localStorage.setItem("meads.compact", state.compact ? "1" : "0");
    renderList();
  });
}

// --- Theme -------------------------------------------------------------
// "auto" follows the OS; "light"/"dark" force a palette. Only affects a plain
// browser — inside VS Code the --vscode-* vars win regardless (see app.css).
let themePref = localStorage.getItem("meads.theme") || "auto";
const sysLight = window.matchMedia("(prefers-color-scheme: light)");
function applyTheme() {
  const resolved = themePref === "auto" ? (sysLight.matches ? "light" : "dark") : themePref;
  document.documentElement.dataset.theme = resolved;
  const btn = document.getElementById("theme-toggle");
  if (btn) btn.textContent = themePref;
}
sysLight.addEventListener("change", () => { if (themePref === "auto") applyTheme(); });
document.getElementById("theme-toggle")?.addEventListener("click", () => {
  themePref = themePref === "auto" ? "light" : themePref === "light" ? "dark" : "auto";
  localStorage.setItem("meads.theme", themePref);
  applyTheme();
});
applyTheme();

// Dep picker: typeahead + click-to-pick.
document.getElementById("dep-search")?.addEventListener("input", (e) => {
  renderDepResults(e.target.value);
});
document.getElementById("dep-search")?.addEventListener("focus", (e) => {
  renderDepResults(e.target.value);
});
document.getElementById("dep-results")?.addEventListener("click", (e) => {
  const row = e.target.closest(".dep-result");
  if (!row) return;
  const id = parseInt(row.dataset.depId, 10);
  if (!id) return;
  addEditingDep(id);
  const search = document.getElementById("dep-search");
  search.value = "";
  search.focus();
  hideDepResults();
});
// Clicking outside the picker (inside the editor dialog) hides the dropdown.
document.getElementById("editor")?.addEventListener("click", (e) => {
  if (!e.target.closest(".dep-picker")) hideDepResults();
});

// --- Keyboard shortcuts ------------------------------------------------

function visibleCardIds() {
  // Matches both cards and compact rows (each carries data-id); group/table
  // headers have none and are skipped.
  return Array.from(document.querySelectorAll("#list [data-id]"))
    .map((c) => parseInt(c.dataset.id, 10));
}

function updateFocusVisual() {
  document.querySelectorAll('#list [data-focused]').forEach((c) => {
    if (parseInt(c.dataset.id, 10) !== state.focusedId) c.removeAttribute("data-focused");
  });
  if (state.focusedId != null) {
    const node = document.getElementById("task-" + state.focusedId);
    if (node) {
      node.setAttribute("data-focused", "true");
      node.scrollIntoView({ block: "nearest" });
    }
  }
}

// Keep keyboard-focus state in sync with real DOM focus: tabbing or clicking
// into a card/row (or one of its controls) highlights that item.
document.getElementById("list").addEventListener("focusin", (e) => {
  const item = e.target.closest("[data-id]");
  if (!item) return;
  const id = parseInt(item.dataset.id, 10);
  if (id === state.focusedId) return;
  state.focusedId = id;
  document.querySelectorAll('#list [data-focused]').forEach((c) => c.removeAttribute("data-focused"));
  item.setAttribute("data-focused", "true");
});

function moveFocus(delta) {
  const ids = visibleCardIds();
  if (ids.length === 0) return;
  let i = ids.indexOf(state.focusedId);
  if (i === -1) i = delta > 0 ? -1 : ids.length;
  i = Math.max(0, Math.min(ids.length - 1, i + delta));
  // Move real DOM focus so screen readers announce the card; the focusin
  // handler above syncs state.focusedId and repaints the visual ring.
  const node = document.getElementById("task-" + ids[i]);
  if (node) node.focus();
}

function focusedTask() {
  return state.tasks.find((t) => t.id === state.focusedId);
}

function isTyping() {
  const a = document.activeElement;
  if (!a) return false;
  if (a.isContentEditable) return true;
  const tag = a.tagName;
  return tag === "INPUT" || tag === "TEXTAREA" || tag === "SELECT";
}

function toggleHelp() {
  const dlg = document.getElementById("help");
  if (!dlg) return;
  if (dlg.open) dlg.close();
  else dlg.showModal();
}

// copyShareUrl puts the current URL (including ?token=) on the clipboard.
// navigator.clipboard requires https or localhost; the webui binds to
// loopback by default, so this path is available.
async function copyShareUrl() {
  const url = location.href;
  try {
    if (navigator.clipboard && navigator.clipboard.writeText) {
      await navigator.clipboard.writeText(url);
    } else {
      // Fallback: textarea-based copy for older or restricted contexts.
      const ta = document.createElement("textarea");
      ta.value = url;
      ta.style.position = "fixed";
      ta.style.opacity = "0";
      document.body.append(ta);
      ta.select();
      document.execCommand("copy");
      ta.remove();
    }
    toast("URL copied to clipboard");
  } catch (err) {
    toast("Copy failed: " + err.message, "err");
  }
}

document.addEventListener("keydown", (e) => {
  // The editor dialog is modal and handles its own keys (Esc closes natively).
  if (document.getElementById("editor").open) return;
  const helpOpen = document.getElementById("help")?.open;

  if (e.key === "Escape") {
    if (helpOpen) return; // dialog closes natively
    const filter = document.getElementById("filter");
    if (filter.value || document.activeElement === filter) {
      filter.value = "";
      state.filter = "";
      renderList();
      filter.blur();
      e.preventDefault();
    }
    return;
  }

  if (e.key === "?" && !e.ctrlKey && !e.metaKey && !e.altKey) {
    if (!isTyping()) { toggleHelp(); e.preventDefault(); }
    return;
  }

  if (e.key === "/" && !isTyping()) {
    document.getElementById("filter").focus();
    e.preventDefault();
    return;
  }

  if (isTyping() || helpOpen) return;
  if (e.ctrlKey || e.metaKey || e.altKey) return;

  switch (e.key) {
    case "j":
      moveFocus(1); e.preventDefault(); break;
    case "k":
      moveFocus(-1); e.preventDefault(); break;
    case "n":
      if (state.canWrite) openEditor(null);
      else openConnection("Connect a write token to create tasks.");
      e.preventDefault(); break;
    case "e": {
      const t = focusedTask();
      if (t && state.canWrite) openEditor(t);
      else if (t) openConnection("Connect a write token to edit tasks.");
      if (t) e.preventDefault();
      break;
    }
    case "d": {
      const t = focusedTask();
      if (!t) break;
      e.preventDefault();
      if (state.canWrite) deleteWithUndo(t);
      else openConnection("Connect a write token to delete tasks.");
      break;
    }
    case "Enter": {
      const t = focusedTask();
      if (!t) break;
      e.preventDefault();
      if (state.canWrite) advanceStatus(t).catch((err) => toast(err.message, "err"));
      else openConnection("Connect a write token to edit tasks.");
      break;
    }
  }
});

// Toolbar buttons inside the editor dialog and live preview updates.
document.querySelector(".md-toolbar")?.addEventListener("click", (e) => {
  const btn = e.target.closest("button[data-md]");
  if (!btn) return;
  e.preventDefault();
  applyMarkdownAction(btn.dataset.md);
});
descriptionInput()?.addEventListener("input", renderPreview);

// --- GitHub connection + loading --------------------------------------

async function initialiseCore() {
  const badge = document.getElementById("core-status");
  try {
    await meadsCore.ready();
    badge.textContent = "Go WASM";
    badge.className = "core-status";
    badge.title = "Meads Go core is running locally in WebAssembly";
  } catch (error) {
    badge.textContent = "WASM error";
    badge.className = "core-status error";
    badge.title = error.message;
  }
}

function updateConnectionUI() {
  const button = document.getElementById("connect-github");
  if (!button) return;
  button.textContent = state.canWrite ? "connected" : "read only";
  button.classList.toggle("connected", state.canWrite);
  button.title = state.canWrite
    ? `Connected to ${github.slug} with write access`
    : `Viewing ${github.slug}; connect a token to edit`;
  document.body.dataset.readonly = state.canWrite ? "false" : "true";
}

function openConnection(message = "") {
  const dialog = document.getElementById("connect");
  const form = document.getElementById("connect-form");
  form.owner.value = github.owner;
  form.repo.value = github.repo;
  form.token.value = github.token;
  form.keep.checked = Boolean(sessionStorage.getItem(tokenKey(github.owner, github.repo)) || github.token);
  const error = document.getElementById("connect-error");
  error.textContent = message;
  error.hidden = !message;
  dialog.showModal();
  (github.token ? form.repo : form.token).focus();
}

function showLoadError(error) {
  state.loading = false;
  const list = document.getElementById("list");
  list.removeAttribute("aria-busy");
  document.getElementById("refresh").disabled = false;
  if (state.cached && state.tasks.length) {
    toast(`Showing cached tasks; sync failed: ${error.message}`, "err");
    updateConnectionUI();
    return;
  }
  list.innerHTML = "";
  const box = el("div");
  box.className = "empty load-error";
  box.append(`Could not load ${github.slug}: ${error.message} `);
  const retry = el("button", "Retry");
  retry.addEventListener("click", () => reload().catch(showLoadError));
  const connect = el("button", "Connection settings");
  connect.addEventListener("click", () => openConnection());
  box.append(retry, connect);
  list.append(box);
  updateConnectionUI();
}

function applySnapshot(snapshot, { cached = false } = {}) {
  state.file = snapshot.file;
  state.tasks = snapshot.tasks;
  state.tasksById = new Map(state.tasks.map((t) => [t.id, t]));
  // A persisted public snapshot never grants authority. Write controls are
  // enabled only after GitHub has verified the current token this session.
  state.canWrite = cached ? false : snapshot.canWrite;
  state.loading = false;
  state.cached = cached;
  document.body.dataset.cache = cached ? "stale" : "fresh";
  updateConnectionUI();
  renderList();
}

async function reload() {
  const list = document.getElementById("list");
  state.loading = true;
  list.setAttribute("aria-busy", "true");
  document.getElementById("refresh").disabled = true;
  const snapshot = await github.load();
  applySnapshot(snapshot);
  list.removeAttribute("aria-busy");
  document.getElementById("refresh").disabled = false;
}

document.getElementById("connect-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const form = event.currentTarget;
  const submit = form.querySelector('[type="submit"]');
  const error = document.getElementById("connect-error");
  error.hidden = true;
  submit.disabled = true;
  submit.textContent = "Connecting…";
  const previousKey = tokenKey(github.owner, github.repo);
  try {
    const owner = form.owner.value.trim();
    const repo = form.repo.value.trim();
    const token = form.token.value.trim();
    github.setTarget({ owner, repo, token });
    await github.connect();
    localStorage.setItem("meads.github.repo", github.slug);
    sessionStorage.removeItem(previousKey);
    if (token && form.keep.checked) sessionStorage.setItem(tokenKey(owner, repo), token);
    else sessionStorage.removeItem(tokenKey(owner, repo));
    const url = new URL(location.href);
    if (github.slug === "jpillora/meads") url.searchParams.delete("repo");
    else url.searchParams.set("repo", github.slug);
    url.searchParams.delete("token");
    history.replaceState(null, "", url);
    await reload();
    document.getElementById("connect").close();
  } catch (err) {
    error.textContent = err.message;
    error.hidden = false;
  } finally {
    submit.disabled = false;
    submit.textContent = "Connect";
  }
});

document.querySelector('[data-action="cancel-connect"]')?.addEventListener("click", () => {
  document.getElementById("connect").close();
});

document.querySelector('[data-action="disconnect"]')?.addEventListener("click", async () => {
  const form = document.getElementById("connect-form");
  sessionStorage.removeItem(tokenKey(github.owner, github.repo));
  github.setTarget({ owner: form.owner.value.trim(), repo: form.repo.value.trim(), token: "" });
  form.token.value = "";
  document.getElementById("connect").close();
  await reload().catch(showLoadError);
});

// GitHub asks API clients not to poll this endpoint more often than every five
// minutes. Manual refresh remains available in the header.
setInterval(() => {
  if (document.visibilityState === "visible" && !state.loading) {
    reload().catch((err) => toast(err.message, "err"));
  }
}, 300_000);

(async function init() {
  const cached = github.cachedSnapshot();
  if (cached) applySnapshot(cached, { cached: true });
  void initialiseCore();
  updateConnectionUI();
  try {
    await reload();
  } catch (err) {
    showLoadError(err);
  }
})();
