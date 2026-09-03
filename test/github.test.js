import assert from "node:assert/strict";
import test from "node:test";

import { GitHubMeads } from "../github.js";

const CONFIG_SHA = "a".repeat(40);
const TASK_SHA = "b".repeat(40);
const TREE_SHA = "c".repeat(40);
const NEXT_SHA = "d".repeat(40);

function json(value, status = 200, headers = {}) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json", ...headers },
  });
}

class MemoryStorage {
  constructor() { this.values = new Map(); }
  getItem(key) { return this.values.has(key) ? this.values.get(key) : null; }
  setItem(key, value) { this.values.set(key, String(value)); }
  removeItem(key) { this.values.delete(key); }
}

function fixtureFetch(log = []) {
  return async (url, options = {}) => {
    const method = options.method || "GET";
    const body = options.body ? JSON.parse(options.body) : undefined;
    log.push({ url, method, body });

    if (url === "https://api.github.com/repos/acme/demo") {
      return json({ private: false, html_url: "https://github.com/acme/demo", permissions: { push: true } });
    }
    if (url.endsWith("/git/matching-refs/meads") || url.endsWith("/git/matching-refs/meads/tasks")) {
      return json([
        { ref: "refs/meads/config", object: { sha: CONFIG_SHA, type: "commit" } },
        { ref: "refs/meads/tasks/7", object: { sha: TASK_SHA, type: "commit" } },
      ], 200, { etag: '"refs-v1"' });
    }
    if (url === `https://raw.githubusercontent.com/acme/demo/${CONFIG_SHA}/config.json`) {
      return json({ git_ref_protocol_version: 1 });
    }
    if (url === `https://raw.githubusercontent.com/acme/demo/${TASK_SHA}/task.json`) {
      return json({ id: 7, title: "Ship it", status: "open", priority: "P2", type: "task", meta: { created: "2026-09-04T00:00:00Z" } });
    }
    if (url.endsWith("/git/ref/meads/config")) {
      return json({ ref: "refs/meads/config", object: { sha: CONFIG_SHA, type: "commit" } });
    }
    if (url.endsWith("/git/ref/meads/tasks/7")) {
      return json({ ref: "refs/meads/tasks/7", object: { sha: TASK_SHA, type: "commit" } });
    }
    if (url.endsWith("/git/trees") && method === "POST") return json({ sha: TREE_SHA }, 201);
    if (url.endsWith("/git/commits") && method === "POST") return json({ sha: NEXT_SHA }, 201);
    if (url.endsWith("/git/refs/meads/tasks/7") && method === "PATCH") {
      return json({ ref: "refs/meads/tasks/7", object: { sha: NEXT_SHA, type: "commit" } });
    }
    return json({ message: `Unhandled test request: ${method} ${url}` }, 500);
  };
}

test("loads task JSON through custom refs", async () => {
  const log = [];
  const store = new GitHubMeads({ owner: "acme", repo: "demo", fetchImpl: fixtureFetch(log) });
  const snapshot = await store.load();

  assert.equal(snapshot.file.path, "acme/demo · refs/meads/tasks/*");
  assert.equal(snapshot.tasks.length, 1);
  assert.equal(snapshot.tasks[0].title, "Ship it");
  assert.equal(snapshot.config.git_ref_protocol_version, 1);
  assert(log.some((call) => call.url.endsWith("/git/matching-refs/meads")));
});

test("hydrates a persisted snapshot and delta-syncs refs with ETag", async () => {
  const browserStorage = new MemoryStorage();
  const first = new GitHubMeads({
    owner: "acme",
    repo: "demo",
    fetchImpl: fixtureFetch(),
    storageImpl: browserStorage,
  });
  await first.load();

  const cached = first.cachedSnapshot();
  assert.equal(cached.cached, true);
  assert.equal(cached.canWrite, false);
  assert.equal(cached.tasks[0].title, "Ship it");

  const log = [];
  const fallback = fixtureFetch(log);
  const second = new GitHubMeads({
    owner: "acme",
    repo: "demo",
    storageImpl: browserStorage,
    fetchImpl: async (url, options = {}) => {
      if (url.endsWith("/git/matching-refs/meads")) {
        log.push({ url, method: options.method || "GET", headers: options.headers });
        assert.equal(options.headers["If-None-Match"], '"refs-v1"');
        return new Response(null, { status: 304, headers: { etag: '"refs-v1"' } });
      }
      return fallback(url, options);
    },
  });
  const synced = await second.load();

  assert.equal(synced.tasks[0].title, "Ship it");
  assert.equal(log.filter((call) => call.url.includes("raw.githubusercontent.com")).length, 0);
});

test("updates a task with a parented commit and a non-force ref move", async () => {
  const log = [];
  const store = new GitHubMeads({ owner: "acme", repo: "demo", token: "test-token", fetchImpl: fixtureFetch(log) });
  await store.load();
  await store.updateTask(7, { status: "inprogress" });

  const commit = log.find((call) => call.url.endsWith("/git/commits") && call.method === "POST");
  assert.deepEqual(commit.body.parents, [TASK_SHA]);
  assert.equal(commit.body.message, "update task 7");

  const patch = log.find((call) => call.url.endsWith("/git/refs/meads/tasks/7") && call.method === "PATCH");
  assert.deepEqual(patch.body, { sha: NEXT_SHA, force: false });
  const tree = log.find((call) => call.url.endsWith("/git/trees") && call.method === "POST");
  assert.equal(JSON.parse(tree.body.tree[0].content).status, "inprogress");
});

test("routes write decisions through the Meads core when supplied", async () => {
  const log = [];
  const decisions = [];
  const core = {
    async apply(request) {
      decisions.push(request);
      const current = request.tasks.find((task) => task.id === request.id);
      return {
        ...current,
        ...request.input,
        meta: { ...current.meta, updated: "2026-09-04T01:02:03Z" },
      };
    },
  };
  const store = new GitHubMeads({
    owner: "acme",
    repo: "demo",
    token: "test-token",
    fetchImpl: fixtureFetch(log),
    core,
  });
  await store.load();
  await store.updateTask(7, { priority: "P1" });

  assert.equal(decisions.length, 1);
  assert.equal(decisions[0].operation, "update");
  assert.equal(decisions[0].tasks[0].title, "Ship it");
  const tree = log.find((call) => call.url.endsWith("/git/trees") && call.method === "POST");
  const written = JSON.parse(tree.body.tree[0].content);
  assert.equal(written.priority, "P1");
  assert.equal(written.meta.updated, "2026-09-04T01:02:03Z");
});

test("refuses a non-atomic delete when another task depends on it", async () => {
  const store = new GitHubMeads({ owner: "acme", repo: "demo", token: "test-token", fetchImpl: fixtureFetch() });
  store.load = async () => ({ tasks: [{ id: 8, title: "Dependent", depends_on: [7] }] });
  await assert.rejects(() => store.deleteTask(7), /Cannot safely delete #7/);
});
