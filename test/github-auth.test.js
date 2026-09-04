import assert from "node:assert/strict";
import test from "node:test";

import { GitHubBroker, oauthErrorMessage } from "../github-auth.js";

const ORIGIN = "https://meads.jpillora.com";
const CONFIG = {
  app_slug: "meads",
  login_url: "/_/github/login",
  session_url: "/_/github/session",
  logout_url: "/_/github/session",
  install_url: "/_/github/install",
};

function json(value, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "content-type": "application/json; charset=utf-8" },
  });
}

test("discovers the same-origin broker and loads a token into memory", async () => {
  const calls = [];
  const broker = new GitHubBroker({
    origin: ORIGIN,
    fetchImpl: async (url, options) => {
      calls.push({ url, options });
      if (url.endsWith("/_/github/config")) return json(CONFIG);
      if (url.endsWith("/_/github/session")) {
        return json({
          authenticated: true,
          access_token: "memory-only-token",
          token_type: "bearer",
          expires_at: "2026-09-04T12:00:00Z",
          user: { id: 7, login: "jpillora", avatar_url: "https://avatars.githubusercontent.com/u/7" },
        });
      }
      throw new Error("unexpected URL");
    },
  });

  const state = await broker.initialise();

  assert.deepEqual(state, {
    available: true,
    authenticated: true,
    user: { id: 7, login: "jpillora", avatar_url: "https://avatars.githubusercontent.com/u/7" },
    expires_at: "2026-09-04T12:00:00Z",
    problem: "",
  });
  assert.equal(broker.accessToken, "memory-only-token");
  assert.equal(JSON.stringify(state).includes("memory-only-token"), false);
  assert.deepEqual(calls.map((call) => call.url), [
    `${ORIGIN}/_/github/config`,
    `${ORIGIN}/_/github/session`,
  ]);
  for (const call of calls) {
    assert.equal(call.options.credentials, "same-origin");
    assert.equal(call.options.cache, "no-store");
    assert.equal(call.options.headers.get("Accept"), "application/json");
  }
});

test("falls back cleanly when the static Pages origin has no broker", async () => {
  const broker = new GitHubBroker({
    origin: "https://jpillora.github.io",
    fetchImpl: async () => new Response("not found", { status: 404, headers: { "content-type": "text/html" } }),
  });

  assert.deepEqual(await broker.initialise(), {
    available: false,
    authenticated: false,
    user: null,
    expires_at: "",
    problem: "",
  });
  assert.equal(broker.accessToken, "");
});

test("rejects a config that redirects auth to another origin or route", async () => {
  for (const loginURL of ["https://evil.example/login", "/_/github/not-login", "//evil.example/login"]) {
    const broker = new GitHubBroker({
      origin: ORIGIN,
      fetchImpl: async () => json({ ...CONFIG, login_url: loginURL }),
    });
    const state = await broker.initialise();
    assert.equal(state.available, false);
    assert.equal(state.authenticated, false);
  }
});

test("builds a same-origin login URL with an encoded return path", async () => {
  const broker = new GitHubBroker({
    origin: ORIGIN,
    fetchImpl: async (url) => url.endsWith("/config") ? json(CONFIG) : json({ authenticated: false }),
  });
  await broker.initialise();

  const login = new URL(broker.loginURL("/?repo=acme%2Fdemo"));
  assert.equal(login.origin, ORIGIN);
  assert.equal(login.pathname, "/_/github/login");
  assert.equal(login.searchParams.get("return_to"), "/?repo=acme%2Fdemo");
  assert.throws(() => broker.loginURL("https://evil.example/"), /temporarily unavailable/);
});

test("logout uses DELETE and always clears the in-memory token", async () => {
  const calls = [];
  const broker = new GitHubBroker({
    origin: ORIGIN,
    fetchImpl: async (url, options) => {
      calls.push({ url, method: options.method || "GET" });
      if (url.endsWith("/config")) return json(CONFIG);
      if (options.method === "DELETE") return new Response(null, { status: 204 });
      return json({
        authenticated: true,
        access_token: "logout-token",
        token_type: "bearer",
        expires_at: "2026-09-04T12:00:00Z",
        user: { id: 7, login: "jpillora", avatar_url: "" },
      });
    },
  });
  await broker.initialise();
  assert.equal(broker.authenticated, true);

  await broker.logout();

  assert.equal(broker.authenticated, false);
  assert.equal(broker.accessToken, "");
  assert.deepEqual(calls.at(-1), { url: `${ORIGIN}/_/github/session`, method: "DELETE" });
});

test("session and callback errors expose only stable UI messages", async () => {
  const broker = new GitHubBroker({
    origin: ORIGIN,
    fetchImpl: async (url) => url.endsWith("/config")
      ? json(CONFIG)
      : json({ authenticated: true, access_token: "sensitive-but-invalid" }),
  });

  const state = await broker.initialise();
  assert.equal(state.available, true);
  assert.equal(state.authenticated, false);
  assert.equal(state.problem, "GitHub sign-in is temporarily unavailable");
  assert.equal(state.problem.includes("sensitive"), false);
  assert.equal(oauthErrorMessage("invalid_state"), "GitHub sign-in could not be verified. Please try again.");
  assert.equal(oauthErrorMessage("anything-else"), "");
});
