const API_ROOT = "https://api.github.com";
const RAW_ROOT = "https://raw.githubusercontent.com";
const API_VERSION = "2026-03-10";
const TASK_PREFIX = "refs/meads/tasks/";
const CONFIG_REF = "refs/meads/config";
const PROTOCOL_VERSION = 1;
const MAX_RETRIES = 5;

export class GitHubAPIError extends Error {
  constructor(message, status, body = null) {
    super(message);
    this.name = "GitHubAPIError";
    this.status = status;
    this.body = body;
  }
}

export class RefConflictError extends Error {
  constructor(ref) {
    super(`${ref} changed while it was being edited`);
    this.name = "RefConflictError";
    this.ref = ref;
  }
}

function clone(value) {
  return globalThis.structuredClone
    ? globalThis.structuredClone(value)
    : JSON.parse(JSON.stringify(value));
}

function storage() {
  try { return globalThis.localStorage || null; } catch { return null; }
}

function validateRepoPart(value, label) {
  const part = String(value || "").trim();
  if (!/^[A-Za-z0-9_.-]+$/.test(part)) throw new Error(`Invalid GitHub ${label}`);
  return part;
}

function taskID(ref) {
  const match = /^refs\/meads\/tasks\/([1-9][0-9]*)$/.exec(ref);
  return match ? Number(match[1]) : null;
}

function normaliseTask(task, expectedID) {
  if (!task || typeof task !== "object" || Array.isArray(task)) {
    throw new Error(`Task #${expectedID} does not contain a JSON object`);
  }
  if (task.id !== expectedID) {
    throw new Error(`${TASK_PREFIX}${expectedID} contains task id ${JSON.stringify(task.id)}`);
  }
  return task;
}

async function mapLimit(items, limit, fn) {
  const results = new Array(items.length);
  let cursor = 0;
  async function worker() {
    while (cursor < items.length) {
      const index = cursor++;
      results[index] = await fn(items[index], index);
    }
  }
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
  return results;
}

export class GitHubMeads {
  constructor({ owner = "jpillora", repo = "meads", token = "", fetchImpl = fetch } = {}) {
    // Native window.fetch requires Window as its receiver in some browsers;
    // wrapping it avoids an "Illegal invocation" when called as a store method.
    this.fetch = (...args) => fetchImpl(...args);
    this.memory = new Map();
    this.last = null;
    this.setTarget({ owner, repo, token });
  }

  setTarget({ owner, repo, token = "" }) {
    this.owner = validateRepoPart(owner, "owner");
    this.repo = validateRepoPart(repo, "repository");
    this.token = String(token || "").trim();
    this.repository = null;
    this.config = null;
    this.last = null;
    this.memory.clear();
  }

  get slug() { return `${this.owner}/${this.repo}`; }
  get canWrite() {
    if (!this.token || !this.repository) return false;
    return Boolean(this.repository.permissions?.push || this.repository.permissions?.admin);
  }

  async request(path, {
    method = "GET",
    body,
    accept = "application/vnd.github+json",
    allow404 = false,
    raw = false,
  } = {}) {
    const headers = {
      Accept: accept,
      "X-GitHub-Api-Version": API_VERSION,
    };
    if (this.token) headers.Authorization = `Bearer ${this.token}`;
    if (body !== undefined) headers["Content-Type"] = "application/json";
    const response = await this.fetch(`${API_ROOT}/repos/${this.owner}/${this.repo}${path}`, {
      method,
      headers,
      body: body === undefined ? undefined : JSON.stringify(body),
    });
    if (allow404 && response.status === 404) return null;
    if (!response.ok) {
      const text = await response.text();
      let payload = null;
      try { payload = JSON.parse(text); } catch { payload = text; }
      const detail = payload?.message || text || response.statusText;
      throw new GitHubAPIError(`${method} ${path} → ${response.status}: ${detail}`, response.status, payload);
    }
    if (response.status === 204) return null;
    return raw ? response.text() : response.json();
  }

  async connect() {
    this.repository = await this.request("");
    return {
      slug: this.slug,
      private: Boolean(this.repository.private),
      canWrite: this.canWrite,
      htmlURL: this.repository.html_url,
    };
  }

  async getRef(relative, allow404 = false) {
    return this.request(`/git/ref/${relative}`, { allow404 });
  }

  objectCacheKey(sha, filename) {
    return `meads:github-object:${this.slug}:${sha}:${filename}`;
  }

  cachedObject(sha, filename) {
    const key = this.objectCacheKey(sha, filename);
    if (this.memory.has(key)) return this.memory.get(key);
    if (this.repository?.private) return null;
    const store = storage();
    if (!store) return null;
    try {
      const value = store.getItem(key);
      if (value !== null) {
        this.memory.set(key, value);
        return value;
      }
    } catch { /* storage is an optional optimisation */ }
    return null;
  }

  cacheObject(sha, filename, value) {
    const key = this.objectCacheKey(sha, filename);
    this.memory.set(key, value);
    if (this.repository?.private || value.length > 100_000) return;
    try { storage()?.setItem(key, value); } catch { /* quota/privacy mode */ }
  }

  async readFileAtCommit(filename, sha) {
    const cached = this.cachedObject(sha, filename);
    if (cached !== null) return JSON.parse(cached);

    let text = "";
    if (!this.repository?.private) {
      const rawURL = `${RAW_ROOT}/${this.owner}/${this.repo}/${sha}/${filename}`;
      const response = await this.fetch(rawURL, { headers: { Accept: "application/json" } });
      if (response.ok) text = await response.text();
    }
    if (!text) {
      text = await this.request(`/contents/${filename}?ref=${encodeURIComponent(sha)}`, {
        accept: "application/vnd.github.raw+json",
        raw: true,
      });
    }
    JSON.parse(text); // validate before caching an immutable object
    this.cacheObject(sha, filename, text);
    return JSON.parse(text);
  }

  validateConfig(config) {
    const version = config.git_ref_protocol_version ?? 1;
    if (!Number.isInteger(version) || version < 1) {
      throw new Error(`Invalid git_ref_protocol_version ${JSON.stringify(version)}`);
    }
    if (version > PROTOCOL_VERSION) {
      throw new Error(`This repository uses Meads ref protocol v${version}; this frontend supports v${PROTOCOL_VERSION}`);
    }
    return config;
  }

  async load() {
    if (!this.repository) await this.connect();
    const refs = await this.request("/git/matching-refs/meads");
    const configRef = refs.find((entry) => entry.ref === CONFIG_REF);
    const config = configRef
      ? this.validateConfig(await this.readFileAtCommit("config.json", configRef.object.sha))
      : {};

    const taskRefs = refs
      .map((entry) => ({ id: taskID(entry.ref), ref: entry.ref, sha: entry.object?.sha }))
      .filter((entry) => entry.id !== null && /^[0-9a-f]{40}$/.test(entry.sha || ""))
      .sort((a, b) => a.id - b.id);
    const allTasks = await mapLimit(taskRefs, 12, async (entry) => {
      const task = await this.readFileAtCommit("task.json", entry.sha);
      return normaliseTask(task, entry.id);
    });
    const tasks = allTasks.filter((task) => !task.deleted);
    const updated = tasks
      .map((task) => task.meta?.updated || task.meta?.created || "")
      .sort()
      .at(-1) || "";

    this.config = config;
    this.last = {
      file: {
        path: `${this.slug} · ${TASK_PREFIX}*`,
        format: "git",
        task_count: tasks.length,
        updated_at: updated,
      },
      tasks,
      allTasks,
      refs: taskRefs,
      config,
      canWrite: this.canWrite,
    };
    return this.last;
  }

  async createCommit(filename, value, parent, message) {
    const tree = await this.request("/git/trees", {
      method: "POST",
      body: {
        tree: [{
          path: filename,
          mode: "100644",
          type: "blob",
          content: JSON.stringify(value),
        }],
      },
    });
    const identity = {
      name: "meads",
      email: "meads@localhost",
      date: new Date().toISOString(),
    };
    const commit = await this.request("/git/commits", {
      method: "POST",
      body: {
        message,
        tree: tree.sha,
        parents: parent ? [parent] : [],
        author: identity,
        committer: identity,
      },
    });
    return commit.sha;
  }

  async updateRef(relative, next, previous) {
    try {
      return await this.request(`/git/refs/${relative}`, {
        method: "PATCH",
        body: { sha: next, force: false },
      });
    } catch (error) {
      if (!(error instanceof GitHubAPIError) || ![409, 422].includes(error.status)) throw error;
      const current = await this.getRef(relative, true);
      if (!current || current.object?.sha !== previous) throw new RefConflictError(`refs/${relative}`);
      throw error;
    }
  }

  async configSnapshot() {
    const ref = await this.getRef("meads/config", true);
    if (!ref) return { ref: null, config: {} };
    const config = this.validateConfig(await this.readFileAtCommit("config.json", ref.object.sha));
    return { ref, config };
  }

  async ensureWritable() {
    if (!this.token) throw new Error("Connect a GitHub token with Contents: write permission first");
    if (!this.repository) await this.connect();
    if (!this.canWrite) throw new Error(`The connected GitHub token cannot write ${this.slug}`);

    for (let attempt = 0; attempt < MAX_RETRIES; attempt++) {
      const { ref, config } = await this.configSnapshot();
      if (config.remote_locking) {
        throw new Error("This repository requires remote locking, which GitHub's browser REST API cannot safely implement");
      }
      if (config.git_ref_protocol_version === PROTOCOL_VERSION) {
        this.config = config;
        return;
      }
      const next = { ...config, git_ref_protocol_version: PROTOCOL_VERSION };
      const sha = await this.createCommit("config.json", next, ref?.object.sha || null, "record git ref protocol version");
      try {
        if (ref) {
          await this.updateRef("meads/config", sha, ref.object.sha);
        } else {
          await this.request("/git/refs", {
            method: "POST",
            body: { ref: CONFIG_REF, sha },
          });
        }
        this.config = next;
        return;
      } catch (error) {
        if (error instanceof RefConflictError || (error instanceof GitHubAPIError && error.status === 422)) continue;
        throw error;
      }
    }
    throw new Error("The Meads config changed too many times; try again");
  }

  validateTask(task) {
    if (!String(task.title || "").trim()) throw new Error("Task title is required");
    if (task.status && !["draft", "open", "inprogress", "blocked", "closed"].includes(task.status)) {
      throw new Error(`Invalid task status ${JSON.stringify(task.status)}`);
    }
    if (task.priority && !/^P[0-9]$/.test(task.priority)) throw new Error(`Invalid priority ${JSON.stringify(task.priority)}`);
    const tags = Array.isArray(task.tags) ? task.tags : [];
    if (tags.some((tag) => !/^[a-z0-9-]+$/.test(tag))) throw new Error("Tags may contain lowercase letters, numbers, and dashes only");
    const deps = Array.isArray(task.depends_on) ? task.depends_on : [];
    if (deps.some((id) => !Number.isInteger(id) || id <= 0 || id === task.id)) throw new Error("Invalid task dependency");
  }

  async mutateTask(id, action, decide) {
    await this.ensureWritable();
    const relative = `meads/tasks/${id}`;
    for (let attempt = 0; attempt < MAX_RETRIES; attempt++) {
      const ref = await this.getRef(relative, true);
      if (!ref) throw new Error(`Task #${id} not found`);
      const current = normaliseTask(await this.readFileAtCommit("task.json", ref.object.sha), id);
      if (current.deleted && action !== "restore") throw new Error(`Task #${id} not found`);
      const next = decide(clone(current));
      if (!next) return current;
      next.id = id;
      next.meta = { ...(next.meta || {}), updated: new Date().toISOString() };
      this.validateTask(next);
      const sha = await this.createCommit("task.json", next, ref.object.sha, `${action} task ${id}`);
      try {
        await this.updateRef(relative, sha, ref.object.sha);
        this.last = null;
        return next;
      } catch (error) {
        if (error instanceof RefConflictError) continue;
        throw error;
      }
    }
    throw new Error(`Task #${id} changed too many times; try again`);
  }

  async addTask(input) {
    await this.ensureWritable();
    for (let attempt = 0; attempt < MAX_RETRIES; attempt++) {
      const refs = await this.request("/git/matching-refs/meads/tasks");
      const highest = refs.reduce((max, entry) => Math.max(max, taskID(entry.ref) || 0), 0);
      const id = highest + 1;
      const now = new Date().toISOString();
      const task = {
        ...clone(input),
        id,
        status: input.status || "open",
        priority: input.priority || "P2",
        type: input.type || "task",
        meta: { ...(input.meta || {}), created: now },
      };
      delete task.meta.updated;
      this.validateTask(task);
      const sha = await this.createCommit("task.json", task, null, `create task ${id}`);
      try {
        await this.request("/git/refs", {
          method: "POST",
          body: { ref: `${TASK_PREFIX}${id}`, sha },
        });
        this.last = null;
        return { id };
      } catch (error) {
        if (error instanceof GitHubAPIError && error.status === 422) continue;
        throw error;
      }
    }
    throw new Error("Task IDs were contended too many times; try again");
  }

  async updateTask(id, patch) {
    return this.mutateTask(id, "update", (task) => {
      for (const key of ["title", "description", "status", "status_reason", "priority", "type", "tags", "depends_on", "agent_id", "files_in_scope"]) {
        if (Object.prototype.hasOwnProperty.call(patch, key)) task[key] = clone(patch[key]);
      }
      if (Array.isArray(task.tags)) task.tags = [...new Set(task.tags.map((tag) => String(tag).trim().toLowerCase()).filter(Boolean))];
      if (Array.isArray(task.depends_on)) task.depends_on = [...new Set(task.depends_on)].sort((a, b) => a - b);
      return task;
    });
  }

  async addDependency(id, parent) {
    const snapshot = await this.load();
    const parentRef = await this.getRef(`meads/tasks/${parent}`, true);
    if (!parentRef) throw new Error(`Task #${parent} not found`);
    const parentTask = await this.readFileAtCommit("task.json", parentRef.object.sha);
    if (parentTask.deleted) throw new Error(`Task #${parent} not found`);
    const byID = new Map(snapshot.tasks.map((task) => [task.id, task]));
    byID.set(parent, parentTask);
    const reaches = (from, wanted, seen = new Set()) => {
      if (from === wanted) return true;
      if (seen.has(from)) return false;
      seen.add(from);
      return (byID.get(from)?.depends_on || []).some((next) => reaches(next, wanted, seen));
    };
    if (reaches(parent, id)) throw new Error(`Dependency #${id} → #${parent} would create a cycle`);
    return this.mutateTask(id, "update", (task) => {
      task.depends_on = [...new Set([...(task.depends_on || []), parent])].sort((a, b) => a - b);
      return task;
    });
  }

  async removeDependency(id, parent) {
    return this.mutateTask(id, "update", (task) => {
      task.depends_on = (task.depends_on || []).filter((value) => value !== parent);
      return task;
    });
  }

  async deleteTask(id) {
    // GitHub REST has no atomic multi-ref transaction. A delete is compatible
    // only when no other live task needs its dependency edge cleaned up.
    const snapshot = await this.load();
    const dependents = snapshot.tasks.filter((task) => (task.depends_on || []).includes(id));
    if (dependents.length) {
      throw new Error(`Cannot safely delete #${id} in the browser: ${dependents.map((task) => `#${task.id}`).join(", ")} depend on it`);
    }
    return this.mutateTask(id, "soft delete", (task) => {
      task.deleted = true;
      return task;
    });
  }
}
