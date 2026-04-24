// Minimal vanilla UI for meads. No framework, no build step.

const qs = new URLSearchParams(location.search);
const TOKEN = qs.get("token") || "";
const AUTH = TOKEN ? { Authorization: "Bearer " + TOKEN } : {};

let state = {
  file: null,
  tasks: [],
  filter: "",
  editing: null, // task being edited, or null for new task
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

function taskCard(t) {
  const tpl = document.getElementById("task-card");
  const node = tpl.content.firstElementChild.cloneNode(true);
  node.dataset.id = t.id;
  node.dataset.status = t.status || "open";
  node.dataset.priority = t.priority || "P2";
  node.id = "task-" + t.id;

  node.querySelector(".id").textContent = "#" + t.id;
  node.querySelector(".title").textContent = t.title || "(no title)";

  const chips = node.querySelector(".chips");
  chips.append(chip(t.type || "task", "type-" + (t.type || "task")));
  chips.append(chip(t.priority || "P2", (t.priority || "P2").toLowerCase()));
  chips.append(chip(t.status || "open", "status-" + (t.status || "open")));

  const deps = node.querySelector(".deps");
  if (Array.isArray(t.depends_on)) {
    for (const dep of t.depends_on) {
      const b = el("button", "↳ " + dep);
      b.className = "dep-link";
      b.dataset.dep = dep;
      deps.append(b);
    }
  }

  node.querySelector(".description").textContent = t.description || "";
  return node;
}

function renderList() {
  const list = document.getElementById("list");
  list.innerHTML = "";
  const filter = state.filter.trim().toLowerCase();
  const visible = state.tasks.filter((t) => {
    if (!filter) return true;
    return (t.title || "").toLowerCase().includes(filter)
      || (t.description || "").toLowerCase().includes(filter)
      || String(t.id) === filter
      || (t.type || "").toLowerCase() === filter
      || (t.status || "").toLowerCase() === filter
      || (t.priority || "").toLowerCase() === filter;
  });
  if (visible.length === 0) {
    const empty = el("div", state.tasks.length === 0 ? "No tasks yet. Add one with the button above." : "No matches.");
    empty.className = "empty";
    list.append(empty);
    return;
  }
  for (const t of visible) list.append(taskCard(t));
}

function renderMeta() {
  document.getElementById("file-path").textContent = state.file ? state.file.path : "TASKS.md";
  const meta = document.getElementById("file-meta");
  if (!state.file) { meta.textContent = ""; return; }
  const count = state.file.task_count;
  const u = state.file.updated_at ? new Date(state.file.updated_at).toLocaleString() : "";
  meta.textContent = `· ${count} task${count === 1 ? "" : "s"}${u ? " · updated " + u : ""}`;
}

// --- Actions -----------------------------------------------------------

const STATUS_ORDER = ["draft", "open", "inprogress", "closed"];
function nextStatus(cur) {
  const i = STATUS_ORDER.indexOf(cur);
  if (i < 0) return "open";
  return STATUS_ORDER[(i + 1) % STATUS_ORDER.length];
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
    form.depends_on.value = (task.depends_on || []).join(", ");
  }
  dlg.showModal();
  form.title.focus();
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
  try {
    if (state.editing) {
      await updateTask(state.editing.id, { id: state.editing.id, ...data });
      // Adjust deps: this v1 only supports adding, but we can diff & patch.
      await syncDeps(state.editing, depsRaw);
    } else {
      const { id } = await addTask(data);
      await syncDeps({ id, depends_on: [] }, depsRaw);
    }
    document.getElementById("editor").close();
    await reload();
  } catch (err) {
    toast(err.message, true);
  }
}

async function syncDeps(task, raw) {
  const wanted = raw
    .split(",")
    .map((s) => parseInt(s.trim(), 10))
    .filter((n) => Number.isInteger(n) && n > 0 && n !== task.id);
  const current = Array.isArray(task.depends_on) ? task.depends_on : [];
  const toAdd = wanted.filter((n) => !current.includes(n));
  for (const parent of toAdd) {
    try { await addDep(task.id, parent); } catch (err) { toast(err.message, true); }
  }
  // (Removal requires a dedicated endpoint; out of v1 scope.)
}

function toast(msg, isErr) {
  const t = el("div", msg);
  t.className = "toast" + (isErr ? " err" : "");
  document.body.append(t);
  setTimeout(() => t.remove(), 4000);
}

// --- Event delegation --------------------------------------------------

document.addEventListener("click", async (e) => {
  const btn = e.target.closest("button");
  if (!btn) return;
  const card = e.target.closest(".card");
  const id = card ? parseInt(card.dataset.id, 10) : 0;
  const action = btn.dataset.action;

  if (btn.id === "new-task") return openEditor(null);
  if (action === "cancel") {
    document.getElementById("editor").close();
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
    if (action === "status-next") {
      await updateTask(id, { id, status: nextStatus(task.status || "open") });
      await reload();
    }
    if (action === "delete") {
      if (!confirm(`Delete task #${id} "${task.title}"?`)) return;
      await deleteTask(id);
      await reload();
    }
  } catch (err) {
    toast(err.message, true);
  }
});

document.getElementById("editor-form").addEventListener("submit", submitEditor);
document.getElementById("filter").addEventListener("input", (e) => {
  state.filter = e.target.value;
  renderList();
});

// --- Load + SSE --------------------------------------------------------

async function reload() {
  const [file, tasks] = await Promise.all([getFile(), getTasks()]);
  state.file = file;
  state.tasks = Array.isArray(tasks) ? tasks : [];
  renderMeta();
  renderList();
}

function subscribe() {
  const url = "/api/events" + (TOKEN ? "?token=" + encodeURIComponent(TOKEN) : "");
  const src = new EventSource(url);
  const onEvent = () => { reload().catch((err) => toast(err.message, true)); };
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
