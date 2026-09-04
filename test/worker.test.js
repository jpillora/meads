import assert from "node:assert/strict";
import test from "node:test";

import { createWorker, validateReturnTo } from "../src/worker.js";

const config = Object.freeze({
  publicOrigin: "https://meads.jpillora.com",
  githubCallbackURL: "https://meads.jpillora.com/_/github/callback",
  upstreamAssetOrigin: "https://jpillora.github.io",
  upstreamAssetPrefix: "/meads/",
  githubClientID: "Iv1.test-client",
  githubAppSlug: "meads-test",
  pendingCookie: "__Host-meads_github_pending",
  sessionCookie: "__Host-meads_github_session",
  cookieVersion: "v1",
  githubAuthorizeURL: "https://github.com/login/oauth/authorize",
  githubTokenURL: "https://github.com/login/oauth/access_token",
  githubAPIOrigin: "https://api.github.com",
});

const env = { GITHUB_CLIENT_SECRET: "test-secret-never-real" };

function cookieValue(response, name) {
  const values = response.headers.getSetCookie?.() || [response.headers.get("set-cookie") || ""];
  const item = values.find((value) => value.startsWith(`${name}=`));
  return item ? item.slice(name.length + 1).split(";", 1)[0] : "";
}

function cookieRequest(url, name, value, init = {}) {
  return new Request(url, { ...init, headers: { Cookie: `${name}=${value}`, ...(init.headers || {}) } });
}

function assertAuthHeaders(response) {
  assert.equal(response.headers.get("cache-control"), "no-store");
  assert.equal(response.headers.get("vary"), "Cookie");
  assert.equal(response.headers.get("referrer-policy"), "no-referrer");
}

function githubFixture(log, options = {}) {
  return async (url, init = {}) => {
    log.push({ url: String(url), init });
    if (url === config.githubTokenURL) {
      const body = new URLSearchParams(init.body);
      if (body.get("grant_type") === "refresh_token") {
        if (options.refreshFails) return Response.json({ error: "bad_refresh" }, { status: 400 });
        return Response.json({ access_token: "refreshed-token", token_type: "bearer", expires_in: 28800, refresh_token: "rotated-refresh", refresh_token_expires_in: 15_811_200 });
      }
      if (options.exchangeFails) return Response.json({ error: "bad_verification_code" }, { status: 400 });
      return Response.json({ access_token: "access-token", token_type: "bearer", expires_in: options.expiresIn ?? 28800, refresh_token: "refresh-token", refresh_token_expires_in: 15_811_200 });
    }
    if (url === `${config.githubAPIOrigin}/user`) {
      if (options.identityFails) return Response.json({ message: "no" }, { status: 401 });
      return Response.json({ id: 123, login: "octocat", avatar_url: "https://avatars.githubusercontent.com/u/123?v=4" });
    }
    if (String(url).includes("/applications/") && init.method === "DELETE") return new Response(null, { status: 204 });
    return new Response("missing fixture", { status: 500 });
  };
}

async function begin(worker, returnTo = "/tasks?status=open") {
  const response = await worker.fetch(new Request(`${config.publicOrigin}/_/github/login?return_to=${encodeURIComponent(returnTo)}`), env);
  const location = new URL(response.headers.get("location"));
  return {
    response,
    state: location.searchParams.get("state"),
    verifierChallenge: location.searchParams.get("code_challenge"),
    cookie: cookieValue(response, config.pendingCookie),
  };
}

async function authenticate({ nowRef = { value: Date.parse("2026-09-04T08:00:00Z") }, options = {} } = {}) {
  const log = [];
  const worker = createWorker({ config, fetchImpl: githubFixture(log, options), now: () => nowRef.value });
  const login = await begin(worker);
  const callback = await worker.fetch(cookieRequest(`${config.publicOrigin}/_/github/callback?code=one-time-code&state=${login.state}`, config.pendingCookie, login.cookie), env);
  return { worker, log, login, callback, sessionCookie: cookieValue(callback, config.sessionCookie), nowRef };
}

test("config is exact and auth routing never falls through to assets", async () => {
  let assetCalls = 0;
  const worker = createWorker({ config, fetchImpl: async () => { assetCalls++; return new Response("asset"); } });
  const response = await worker.fetch(new Request(`${config.publicOrigin}/_/github/config`), env);
  assert.deepEqual(await response.json(), {
    app_slug: "meads-test", login_url: "/_/github/login", session_url: "/_/github/session", logout_url: "/_/github/session", install_url: "/_/github/install",
  });
  assertAuthHeaders(response);
  assert.equal(assetCalls, 0);

  const missing = await worker.fetch(new Request(`${config.publicOrigin}/_/github/nope`), env);
  assert.equal(missing.status, 404);
  assert.equal(assetCalls, 0);
  const wrongMethod = await worker.fetch(new Request(`${config.publicOrigin}/_/github/config`, { method: "POST" }), env);
  assert.equal(wrongMethod.status, 405);
  assert.equal(wrongMethod.headers.get("allow"), "GET");
  const sessionMethod = await worker.fetch(new Request(`${config.publicOrigin}/_/github/session`, { method: "POST" }), env);
  assert.equal(sessionMethod.status, 405);
  assert.equal(sessionMethod.headers.get("allow"), "GET, DELETE");
  const install = await worker.fetch(new Request(`${config.publicOrigin}/_/github/install`), env);
  assert.equal(install.headers.get("location"), "https://github.com/apps/meads-test/installations/new");
});

test("login creates strict PKCE redirect and secure encrypted pending cookie", async () => {
  const worker = createWorker({ config, fetchImpl: githubFixture([]) });
  const login = await begin(worker);
  const location = new URL(login.response.headers.get("location"));
  assert.equal(location.origin + location.pathname, config.githubAuthorizeURL);
  assert.equal(location.searchParams.get("client_id"), config.githubClientID);
  assert.equal(location.searchParams.get("redirect_uri"), config.githubCallbackURL);
  assert.equal(location.searchParams.get("code_challenge_method"), "S256");
  assert.match(login.state, /^[A-Za-z0-9_-]{43}$/);
  assert.match(login.verifierChallenge, /^[A-Za-z0-9_-]{43}$/);
  const setCookie = login.response.headers.get("set-cookie");
  assert.match(setCookie, /^__Host-meads_github_pending=v1\./);
  assert.match(setCookie, /Max-Age=600/);
  assert.match(setCookie, /Path=\/; HttpOnly; Secure; SameSite=Lax/);
  assert.doesNotMatch(setCookie, /Domain=/i);
  assertAuthHeaders(login.response);
});

test("return_to validation rejects off-origin and ambiguous inputs", () => {
  for (const value of ["https://evil.example/x", "//evil.example/x", "/\\evil.example", "/%5cevil", "/%2f%2fevil.example", "/ok%0d%0aX-Evil:y", "///evil"]) {
    assert.equal(validateReturnTo(value, config), "/", value);
  }
  assert.equal(validateReturnTo("/tasks/../ready?q=one", config), "/ready?q=one");
  assert.equal(validateReturnTo("/", config), "/");
});

test("callback rejects missing, tampered, expired, and mismatched state cookies", async () => {
  const nowRef = { value: Date.parse("2026-09-04T08:00:00Z") };
  const worker = createWorker({ config, fetchImpl: githubFixture([]), now: () => nowRef.value });
  const missing = await worker.fetch(new Request(`${config.publicOrigin}/_/github/callback?code=x&state=y`), env);
  assert.equal(new URL(missing.headers.get("location"), config.publicOrigin).searchParams.get("github_auth_error"), "invalid_state");
  assert.match(missing.headers.get("set-cookie"), /Max-Age=0/);

  const login = await begin(worker);
  const tampered = await worker.fetch(cookieRequest(`${config.publicOrigin}/_/github/callback?code=x&state=${login.state}`, config.pendingCookie, `${login.cookie}x`), env);
  assert.equal(new URL(tampered.headers.get("location"), config.publicOrigin).searchParams.get("github_auth_error"), "invalid_state");

  const mismatch = await worker.fetch(cookieRequest(`${config.publicOrigin}/_/github/callback?code=x&state=wrong`, config.pendingCookie, login.cookie), env);
  assert.equal(new URL(mismatch.headers.get("location"), config.publicOrigin).searchParams.get("github_auth_error"), "invalid_state");

  nowRef.value += 601_000;
  const expired = await worker.fetch(cookieRequest(`${config.publicOrigin}/_/github/callback?code=x&state=${login.state}`, config.pendingCookie, login.cookie), env);
  assert.equal(new URL(expired.headers.get("location"), config.publicOrigin).searchParams.get("github_auth_error"), "expired");
  assert.equal(new URL(expired.headers.get("location"), config.publicOrigin).pathname, "/tasks");
});

test("callback maps an authorization denial to its stable code", async () => {
  const worker = createWorker({ config, fetchImpl: githubFixture([]) });
  const login = await begin(worker, "/after");
  const denied = await worker.fetch(cookieRequest(`${config.publicOrigin}/_/github/callback?error=access_denied&state=${login.state}`, config.pendingCookie, login.cookie), env);
  assert.equal(denied.headers.get("location"), "/after?github_auth_error=denied");
  assert.match(denied.headers.get("set-cookie"), /Max-Age=0/);
  assertAuthHeaders(denied);

  const second = await begin(worker, "/after");
  const wrongState = await worker.fetch(cookieRequest(`${config.publicOrigin}/_/github/callback?error=access_denied&state=wrong`, config.pendingCookie, second.cookie), env);
  assert.equal(wrongState.headers.get("location"), "/after?github_auth_error=invalid_state");
});

test("successful callback exchanges code, establishes identity, and returns exact session shape", async () => {
  const result = await authenticate();
  assert.equal(result.callback.status, 302);
  assert.equal(result.callback.headers.get("location"), "/tasks?status=open");
  assert.ok(result.sessionCookie.startsWith("v1."));
  assert.doesNotMatch(result.callback.headers.get("location"), /one-time-code|access-token|octocat/);
  assert.match(result.callback.headers.get("set-cookie"), /__Host-meads_github_pending=; Max-Age=0/);

  const exchange = result.log.find((entry) => entry.url === config.githubTokenURL);
  const fields = new URLSearchParams(exchange.init.body);
  assert.equal(fields.get("code"), "one-time-code");
  assert.ok(fields.get("code_verifier"));
  assert.equal(fields.get("redirect_uri"), config.githubCallbackURL);
  const identity = result.log.find((entry) => entry.url.endsWith("/user"));
  assert.equal(identity.init.headers.Authorization, "Bearer access-token");

  const session = await result.worker.fetch(cookieRequest(`${config.publicOrigin}/_/github/session`, config.sessionCookie, result.sessionCookie), env);
  assert.deepEqual(await session.json(), {
    authenticated: true,
    access_token: "access-token",
    token_type: "bearer",
    expires_at: "2026-09-04T16:00:00.000Z",
    user: { id: 123, login: "octocat", avatar_url: "https://avatars.githubusercontent.com/u/123?v=4" },
  });
  assertAuthHeaders(session);
});

test("callback exposes only stable exchange and identity errors", async () => {
  for (const [options, expected] of [[{ exchangeFails: true }, "exchange_failed"], [{ identityFails: true }, "identity_failed"]]) {
    const log = [];
    const worker = createWorker({ config, fetchImpl: githubFixture(log, options) });
    const login = await begin(worker, "/after");
    const response = await worker.fetch(cookieRequest(`${config.publicOrigin}/_/github/callback?code=sensitive-code&state=${login.state}`, config.pendingCookie, login.cookie), env);
    const location = response.headers.get("location");
    assert.equal(new URL(location, config.publicOrigin).searchParams.get("github_auth_error"), expected);
    assert.doesNotMatch(location, /sensitive|bad_|access-token/);
  }
});

test("logged-out session is exact and near-expiry sessions refresh and rotate", async () => {
  const plain = createWorker({ config, fetchImpl: githubFixture([]) });
  const loggedOut = await plain.fetch(new Request(`${config.publicOrigin}/_/github/session`), env);
  assert.equal(await loggedOut.text(), '{"authenticated":false}');

  const nowRef = { value: Date.parse("2026-09-04T08:00:00Z") };
  const result = await authenticate({ nowRef, options: { expiresIn: 360 } });
  nowRef.value += 61_000;
  const response = await result.worker.fetch(cookieRequest(`${config.publicOrigin}/_/github/session`, config.sessionCookie, result.sessionCookie), env);
  const body = await response.json();
  assert.equal(body.authenticated, true);
  assert.equal(body.access_token, "refreshed-token");
  assert.equal(body.expires_at, "2026-09-04T16:01:01.000Z");
  assert.ok(cookieValue(response, config.sessionCookie).startsWith("v1."));
  assert(result.log.some((entry) => new URLSearchParams(entry.init.body || "").get("grant_type") === "refresh_token"));
});

test("refresh failure logs out and clears the session", async () => {
  const nowRef = { value: Date.parse("2026-09-04T08:00:00Z") };
  const result = await authenticate({ nowRef, options: { expiresIn: 360, refreshFails: true } });
  nowRef.value += 61_000;
  const response = await result.worker.fetch(cookieRequest(`${config.publicOrigin}/_/github/session`, config.sessionCookie, result.sessionCookie), env);
  assert.equal(await response.text(), '{"authenticated":false}');
  assert.match(response.headers.get("set-cookie"), /Max-Age=0/);
});

test("logout clears both cookies, validates Origin, and best-effort revokes", async () => {
  const result = await authenticate();
  const bad = await result.worker.fetch(cookieRequest(`${config.publicOrigin}/_/github/session`, config.sessionCookie, result.sessionCookie, { method: "DELETE", headers: { Origin: "https://evil.example" } }), env);
  assert.equal(bad.status, 403);

  const response = await result.worker.fetch(cookieRequest(`${config.publicOrigin}/_/github/session`, config.sessionCookie, result.sessionCookie, { method: "DELETE", headers: { Origin: config.publicOrigin } }), env);
  assert.equal(response.status, 204);
  const setCookies = response.headers.getSetCookie?.() || [response.headers.get("set-cookie")];
  assert(setCookies.some((value) => value.includes(config.pendingCookie) && value.includes("Max-Age=0")));
  assert(setCookies.some((value) => value.includes(config.sessionCookie) && value.includes("Max-Age=0")));
  assert(result.log.some((entry) => entry.url.includes("/applications/") && entry.init.method === "DELETE"));
});

test("assets map under /meads, preserve query and validators, rewrite redirects, and force WASM MIME", async () => {
  const log = [];
  const worker = createWorker({ config, fetchImpl: async (url, init) => {
    log.push({ url: String(url), init });
    if (String(url).endsWith("/meads/old")) return new Response(null, { status: 301, headers: { Location: "https://jpillora.github.io/meads/new?q=1" } });
    return new Response("asset", { headers: { "Content-Type": "application/octet-stream", ETag: '"one"', "Cache-Control": "public, max-age=600" } });
  } });
  const root = await worker.fetch(new Request(`${config.publicOrigin}/?v=1`, { headers: { "If-None-Match": '"old"', Cookie: "private=no" } }), env);
  assert.equal(log[0].url, "https://jpillora.github.io/meads/?v=1");
  assert.equal(log[0].init.headers.get("If-None-Match"), '"old"');
  assert.equal(log[0].init.headers.has("Cookie"), false);
  assert.equal(root.headers.get("etag"), '"one"');
  const wasm = await worker.fetch(new Request(`${config.publicOrigin}/meads.wasm`), env);
  assert.equal(wasm.headers.get("content-type"), "application/wasm");
  const moved = await worker.fetch(new Request(`${config.publicOrigin}/old`), env);
  assert.equal(moved.headers.get("location"), `${config.publicOrigin}/new?q=1`);
});

test("the GitHub Pages custom-domain redirect is followed without looping clients", async () => {
  const calls = [];
  const worker = createWorker({ config, fetchImpl: async (url, init) => {
    calls.push({ url: String(url), init });
    if (calls.length === 1) return new Response(null, { status: 301, headers: { Location: "http://jpillora.com/meads/app.js?v=1" } });
    return new Response("javascript", { headers: { "Content-Type": "text/javascript", ETag: '"entry"' } });
  } });
  const response = await worker.fetch(new Request(`${config.publicOrigin}/app.js?v=1`), env);
  assert.equal(response.status, 200);
  assert.equal(await response.text(), "javascript");
  assert.equal(calls[1].url, "https://jpillora.com/meads/app.js?v=1");
  assert.equal(calls[1].init.redirect, "follow");
});

test("external HTTP redirects before routing and mutating assets are never proxied", async () => {
  let calls = 0;
  const worker = createWorker({ config, fetchImpl: async () => { calls++; return new Response("asset"); } });
  const http = await worker.fetch(new Request("http://meads.jpillora.com/app.js"), env);
  assert.equal(http.status, 308);
  assert.equal(http.headers.get("location"), "https://meads.jpillora.com/app.js");
  const post = await worker.fetch(new Request(`${config.publicOrigin}/app.js`, { method: "POST" }), env);
  assert.equal(post.status, 405);
  assert.equal(post.headers.get("allow"), "GET, HEAD");
  assert.equal(calls, 0);
});

test("missing secret leaves assets and public discovery usable with safe auth failure", async () => {
  const worker = createWorker({ config, fetchImpl: async () => new Response("ui", { headers: { "Content-Type": "text/html" } }) });
  const configResponse = await worker.fetch(new Request(`${config.publicOrigin}/_/github/config`), {});
  assert.equal(configResponse.status, 200);
  const session = await worker.fetch(new Request(`${config.publicOrigin}/_/github/session`), {});
  assert.equal(await session.text(), '{"authenticated":false}');
  const login = await worker.fetch(new Request(`${config.publicOrigin}/_/github/login?return_to=%2Fready`), {});
  assert.equal(login.headers.get("location"), "/ready?github_auth_error=configuration");
  const asset = await worker.fetch(new Request(`${config.publicOrigin}/`), {});
  assert.equal(await asset.text(), "ui");
});
