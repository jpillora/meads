// Minimal vanilla UI for meads. No framework, no build step.
// Third-party deps are vendored ESM resolved via the import map in index.html.
import { marked } from "marked";
import DOMPurify from "dompurify";

const qs = new URLSearchParams(location.search);
const TOKEN = qs.get("token") || "";
const AUTH = TOKEN ? { Authorization: "Bearer " + TOKEN } : {};

let state = {
  file: null,
  tasks: [],
  tasksById: new Map(),
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

// --- API ---------------------------------------------------------------

async function api(method, path, body) {
  const opts = { method, headers: { ...AUTH } };
  if (body !== undefined) {
    opts.headers["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const r = await fetch(path, opts);
  if (!r.ok) {
    const txt = await r.text();
    let msg = txt;
    try { msg = JSON.parse(txt).error || msg; } catch {}
    throw new Error(`${method} ${path} → ${r.status}: ${msg}`);
  }
  if (r.status === 204) return null;
  return r.json();
}

const getFile = () => api("GET", "/api/file");
const getTasks = () => api("GET", "/api/tasks");
const addTask = (t) => api("POST", "/api/tasks", t);
const updateTask = (id, t) => api("PATCH", `/api/tasks/${id}`, t);
const deleteTask = (id) => api("DELETE", `/api/tasks/${id}`);
const addDep = (id, parent) => api("POST", `/api/tasks/${id}/deps`, { parent_id: parent });
const removeDep = (id, parent) => api("DELETE", `/api/tasks/${id}/deps/${parent}`);

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
// are vendored ESM (see assets/vendor/).
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

  node.querySelector(".id").textContent = "#" + t.id;
  node.querySelector(".title").textContent = t.title || "(no title)";

  // Dependency-blocked: unmet (non-closed) parents hold this task back even
  // though its own status is open/inprogress. Distinct from a manual "blocked".
  const blocking = isDepBlocked(t) ? unmetDeps(t) : [];
  if (blocking.length) node.dataset.depBlocked = "true";

  // Status leads (most important state), then priority, then type.
  const chips = node.querySelector(".chips");
  const statusChip = chip(status, "status-" + status);
  if (t.status_reason && (status === "blocked" || status === "closed")) statusChip.title = t.status_reason;
  chips.append(statusChip);
  if (blocking.length) {
    const c = chip("blocked by deps", "dep-blocked");
    c.title = "Not ready — waiting on " + blocking.map((p) => "#" + p.id).join(", ");
    chips.append(c);
  }
  chips.append(chip(priority, priority.toLowerCase()));
  chips.append(chip(type, "type-" + type));

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
  return node;
}

// colShow toggles the optional compact columns; recomputed each render.
let colShow = { priority: true, dep: true };

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
  if (colShow.dep) {
    const deps = Array.isArray(t.depends_on) ? t.depends_on : [];
    const cell = tcell("tdep" + (blocking.length ? " unmet" : ""), deps.length ? deps.join(", ") : "");
    if (deps.length) cell.title = "depends on " + deps.map((d) => "#" + d).join(", ");
    row.append(cell);
  }

  row.addEventListener("click", () => openEditor(t));
  return row;
}

// compactHeader is the sticky column-label row for the compact table.
function compactHeader() {
  const h = el("div");
  h.className = "trow thead";
  h.append(tcell("tid", "ID"), tcell("ttitle", "Title"), tcell("ttype", ""), tcell("tstatus", ""));
  if (colShow.priority) h.append(tcell("tpri", "Pri"));
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

function renderList() {
  const list = document.getElementById("list");
  list.innerHTML = "";
  const filter = state.filter.trim().toLowerCase();
  const visible = state.tasks.filter((t) => {
    if (!state.showClosed && (t.status || "open") === "closed") return false;
    if (!filter) return true;
    return (t.title || "").toLowerCase().includes(filter)
      || (t.description || "").toLowerCase().includes(filter)
      || String(t.id) === filter
      || (t.type || "").toLowerCase() === filter
      || (t.status || "").toLowerCase() === filter
      || (t.priority || "").toLowerCase() === filter;
  });
  renderMeta(visible.length);
  if (visible.length === 0) {
    const empty = el("div", state.tasks.length === 0 ? "No tasks yet. Add one with the button above." : "No matches.");
    empty.className = "empty";
    list.append(empty);
    state.focusedId = null;
    return;
  }
  const sorted = sortTasks(visible);
  // Compact mode: switch the row renderer and recompute the optional columns
  // (hide priority when every visible task shares one; hide deps when none).
  list.classList.toggle("compact", state.compact);
  if (state.compact) {
    const firstPri = visible[0].priority || "P2";
    colShow.priority = visible.some((t) => (t.priority || "P2") !== firstPri);
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
  meta.textContent = `· ${count}${rel ? " · updated " + rel : ""}`;
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
    const reason = window.prompt(
      `Reason for marking #${task.id} ${status}? (optional, leave blank for none)`,
      prior,
    );
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

function openEditor(task) {
  state.editing = task;
  const dlg = document.getElementById("editor");
  const form = document.getElementById("editor-form");
  form.reset();
  document.getElementById("editor-title").textContent = task ? `Edit task #${task.id}` : "New task";
  if (task) {
    form.title.value = task.title || "";
    form.type.value = task.type || "task";
    form.priority.value = task.priority || "P2";
    form.status.value = task.status || "open";
    form.description.value = task.description || "";
    form.status_reason.value = task.status_reason || "";
  }
  updateReasonVisibility();
  setEditingDeps(task ? (task.depends_on || []) : []);
  document.getElementById("dep-search").value = "";
  hideDepResults();
  renderPreview();
  dlg.showModal();
  form.title.focus();
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
      const url = window.prompt("Link URL", "https://");
      if (url == null) return;
      return wrapSelection(ta, "[", `](${url})`, "link text");
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
  const data = {
    title: String(fd.get("title") || "").trim(),
    type: String(fd.get("type") || ""),
    priority: String(fd.get("priority") || ""),
    status: String(fd.get("status") || ""),
    description: String(fd.get("description") || ""),
  };
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

function toast(msg, kind) {
  const t = el("div", msg);
  t.className = "toast" + (kind === "err" ? " err" : " ok");
  document.body.append(t);
  setTimeout(() => t.remove(), kind === "err" ? 4000 : 2500);
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
  if (btn.id === "new-task") { setOverflowOpen(false); return openEditor(null); }
  if (btn.id === "help-toggle") { setOverflowOpen(false); return toggleHelp(); }
  if (btn.id === "copy-url") { setOverflowOpen(false); return copyShareUrl(); }
  if (action === "close-help") {
    document.getElementById("help").close();
    return;
  }
  if (action === "cancel") {
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
    if (action === "delete") {
      if (!confirm(`Delete task #${id} "${task.title}"?`)) return;
      await deleteTask(id);
      await reload();
      toast(`Task #${id} deleted`);
    }
  } catch (err) {
    toast(err.message, "err");
  }
});

document.getElementById("editor-form").addEventListener("submit", submitEditor);
document.querySelector('#editor-form [name="status"]')?.addEventListener("change", updateReasonVisibility);
document.getElementById("filter").addEventListener("input", (e) => {
  state.filter = e.target.value;
  renderList();
});

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

function moveFocus(delta) {
  const ids = visibleCardIds();
  if (ids.length === 0) return;
  let i = ids.indexOf(state.focusedId);
  if (i === -1) i = delta > 0 ? -1 : ids.length;
  i = Math.max(0, Math.min(ids.length - 1, i + delta));
  state.focusedId = ids[i];
  updateFocusVisual();
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
      openEditor(null); e.preventDefault(); break;
    case "e": {
      const t = focusedTask();
      if (t) { openEditor(t); e.preventDefault(); }
      break;
    }
    case "d": {
      const t = focusedTask();
      if (!t) break;
      e.preventDefault();
      if (!confirm(`Delete task #${t.id} "${t.title}"?`)) break;
      deleteTask(t.id)
        .then(reload)
        .then(() => toast(`Task #${t.id} deleted`))
        .catch((err) => toast(err.message, "err"));
      break;
    }
    case "Enter": {
      const t = focusedTask();
      if (!t) break;
      e.preventDefault();
      advanceStatus(t).catch((err) => toast(err.message, "err"));
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

// --- Load + SSE --------------------------------------------------------

async function reload() {
  const [file, tasks] = await Promise.all([getFile(), getTasks()]);
  state.file = file;
  state.tasks = Array.isArray(tasks) ? tasks : [];
  state.tasksById = new Map(state.tasks.map((t) => [t.id, t]));
  renderList();
}

function subscribe() {
  const url = "/api/events" + (TOKEN ? "?token=" + encodeURIComponent(TOKEN) : "");
  const src = new EventSource(url);
  const onEvent = () => { reload().catch((err) => toast(err.message, "err")); };
  for (const kind of ["task_added", "task_updated", "task_deleted", "file_changed"]) {
    src.addEventListener(kind, onEvent);
  }
  src.onerror = () => {
    // Browser auto-reconnects; swallow.
  };
  return src;
}

(async function init() {
  try {
    await reload();
  } catch (err) {
    document.getElementById("list").innerHTML = "";
    document.getElementById("list").append(Object.assign(
      document.createElement("div"),
      { className: "empty", textContent: "Error: " + err.message },
    ));
  }
  subscribe();
})();
