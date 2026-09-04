import { PUBLIC_CONFIG } from "./config.js";

const encoder = new TextEncoder();
const decoder = new TextDecoder();
const PENDING_SECONDS = 10 * 60;
const REFRESH_WINDOW_MS = 5 * 60 * 1000;
// GitHub currently reports its six-month refresh lifetime as 15,811,200
// seconds (183 days). Keep a tight defensive cap with enough calendar slack.
const MAX_SESSION_SECONDS = 200 * 24 * 60 * 60;
const COOKIE_WIRE_LIMIT = 7000;
const GITHUB_API_VERSION = "2026-03-10";

const SECURITY_HEADERS = Object.freeze({
  "Content-Security-Policy": "default-src 'self'; connect-src 'self' https://api.github.com https://raw.githubusercontent.com; img-src 'self' data: https://avatars.githubusercontent.com; style-src 'self'; script-src 'self' 'wasm-unsafe-eval'; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'; worker-src 'self'",
  "Referrer-Policy": "no-referrer",
  "X-Content-Type-Options": "nosniff",
  "X-Frame-Options": "DENY",
  "Permissions-Policy": "camera=(), microphone=(), geolocation=(), payment=()",
});

const AUTH_HEADERS = Object.freeze({
  "Cache-Control": "no-store",
  Vary: "Cookie",
});

function applyHeaders(response, extra = {}) {
  const headers = new Headers(response.headers);
  for (const [name, value] of Object.entries(SECURITY_HEADERS)) headers.set(name, value);
  for (const [name, value] of Object.entries(extra)) headers.set(name, value);
  return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
}

function json(value, status = 200, headers = {}) {
  return applyHeaders(new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json; charset=utf-8", ...AUTH_HEADERS, ...headers },
  }));
}

function redirect(location, status = 302, headers = {}) {
  return applyHeaders(new Response(null, {
    status,
    headers: { Location: location, ...AUTH_HEADERS, ...headers },
  }));
}

function methodNotAllowed(allow) {
  return json({ error: "method_not_allowed" }, 405, { Allow: allow });
}

function bytesToBase64URL(bytes) {
  let binary = "";
  for (let i = 0; i < bytes.length; i += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  }
  return btoa(binary).replaceAll("+", "-").replaceAll("/", "_").replace(/=+$/, "");
}

function base64URLToBytes(value) {
  if (!/^[A-Za-z0-9_-]+$/.test(value) || value.length > COOKIE_WIRE_LIMIT) throw new Error("invalid encoding");
  const padded = value.replaceAll("-", "+").replaceAll("_", "/") + "===".slice((value.length + 3) % 4);
  const binary = atob(padded);
  return Uint8Array.from(binary, (char) => char.charCodeAt(0));
}

function randomBase64URL(size, cryptoImpl) {
  return bytesToBase64URL(cryptoImpl.getRandomValues(new Uint8Array(size)));
}

async function sha256Base64URL(value, cryptoImpl) {
  return bytesToBase64URL(new Uint8Array(await cryptoImpl.subtle.digest("SHA-256", encoder.encode(value))));
}

async function cookieKey(secret, purpose, config, cryptoImpl) {
  const material = await cryptoImpl.subtle.importKey("raw", encoder.encode(secret), "HKDF", false, ["deriveKey"]);
  return cryptoImpl.subtle.deriveKey({
    name: "HKDF",
    hash: "SHA-256",
    salt: encoder.encode(`${config.publicOrigin}|meads-github-cookie-v1`),
    info: encoder.encode(`meads.github.${purpose}-cookie.v1`),
  }, material, { name: "AES-GCM", length: 256 }, false, ["encrypt", "decrypt"]);
}

function cookieAAD(purpose, config) {
  const name = purpose === "pending" ? config.pendingCookie : config.sessionCookie;
  return encoder.encode(`${config.cookieVersion}|${purpose}|${name}|${config.publicOrigin}`);
}

async function sealCookie(payload, secret, purpose, config, cryptoImpl) {
  const nonce = cryptoImpl.getRandomValues(new Uint8Array(12));
  const key = await cookieKey(secret, purpose, config, cryptoImpl);
  const plaintext = encoder.encode(JSON.stringify(payload));
  const ciphertext = new Uint8Array(await cryptoImpl.subtle.encrypt({
    name: "AES-GCM",
    iv: nonce,
    additionalData: cookieAAD(purpose, config),
    tagLength: 128,
  }, key, plaintext));
  const wire = new Uint8Array(nonce.length + ciphertext.length);
  wire.set(nonce);
  wire.set(ciphertext, nonce.length);
  const value = `${config.cookieVersion}.${bytesToBase64URL(wire)}`;
  if (value.length > COOKIE_WIRE_LIMIT) throw new Error("cookie too large");
  return value;
}

async function openCookie(wire, secret, purpose, config, cryptoImpl) {
  if (typeof wire !== "string" || wire.length > COOKIE_WIRE_LIMIT) throw new Error("invalid cookie");
  const [version, encoded, extra] = wire.split(".");
  if (version !== config.cookieVersion || !encoded || extra !== undefined) throw new Error("invalid cookie");
  const sealed = base64URLToBytes(encoded);
  if (sealed.length < 29) throw new Error("invalid cookie");
  const key = await cookieKey(secret, purpose, config, cryptoImpl);
  const plaintext = await cryptoImpl.subtle.decrypt({
    name: "AES-GCM",
    iv: sealed.subarray(0, 12),
    additionalData: cookieAAD(purpose, config),
    tagLength: 128,
  }, key, sealed.subarray(12));
  const payload = JSON.parse(decoder.decode(plaintext));
  if (!payload || typeof payload !== "object" || Array.isArray(payload)) throw new Error("invalid cookie");
  return payload;
}

function parseCookies(request) {
  const cookies = new Map();
  const header = request.headers.get("Cookie") || "";
  if (header.length > 16000) return cookies;
  for (const part of header.split(";")) {
    const equals = part.indexOf("=");
    if (equals <= 0) continue;
    cookies.set(part.slice(0, equals).trim(), part.slice(equals + 1).trim());
  }
  return cookies;
}

function cookieHeader(name, value, maxAge) {
  return `${name}=${value}; Max-Age=${Math.max(0, Math.floor(maxAge))}; Path=/; HttpOnly; Secure; SameSite=Lax`;
}

function clearCookie(name) {
  return cookieHeader(name, "", 0);
}

function withCookies(response, values) {
  const headers = new Headers(response.headers);
  for (const value of values) headers.append("Set-Cookie", value);
  return new Response(response.body, { status: response.status, statusText: response.statusText, headers });
}

export function validateReturnTo(value, config = PUBLIC_CONFIG) {
  if (value === null || value === undefined || value === "") return "/";
  if (typeof value !== "string" || value.length > 2048 || !value.startsWith("/") || value.startsWith("//")) return "/";
  if (/[\\\u0000-\u001f\u007f]/.test(value)) return "/";
  let decoded;
  try { decoded = decodeURIComponent(value); } catch { return "/"; }
  if (!decoded.startsWith("/") || decoded.startsWith("//") || /[\\\u0000-\u001f\u007f]/.test(decoded)) return "/";
  try {
    const target = new URL(value, config.publicOrigin);
    if (target.origin !== config.publicOrigin || target.username || target.password) return "/";
    return `${target.pathname}${target.search}${target.hash}`;
  } catch {
    return "/";
  }
}

function safeErrorLocation(returnTo, code, config) {
  const target = new URL(validateReturnTo(returnTo, config), config.publicOrigin);
  target.searchParams.set("github_auth_error", code);
  return `${target.pathname}${target.search}${target.hash}`;
}

function constantTimeEqual(left, right) {
  const a = encoder.encode(typeof left === "string" ? left : "");
  const b = encoder.encode(typeof right === "string" ? right : "");
  let mismatch = a.length ^ b.length;
  const length = Math.max(a.length, b.length);
  for (let index = 0; index < length; index++) mismatch |= (a[index] || 0) ^ (b[index] || 0);
  return mismatch === 0;
}

function validString(value, min, max, pattern) {
  return typeof value === "string" && value.length >= min && value.length <= max && (!pattern || pattern.test(value));
}

function validatePending(payload, now, config) {
  const returnTo = validateReturnTo(payload.return_to, config);
  if (payload.v !== 1 || returnTo !== payload.return_to) throw new Error("invalid pending");
  if (!validString(payload.state, 22, 200, /^[A-Za-z0-9_-]+$/)) throw new Error("invalid pending");
  if (!validString(payload.verifier, 43, 128, /^[A-Za-z0-9._~-]+$/)) throw new Error("invalid pending");
  if (!Number.isSafeInteger(payload.issued_at) || !Number.isSafeInteger(payload.expires_at)) throw new Error("invalid pending");
  if (payload.issued_at > now + 60_000 || payload.expires_at > payload.issued_at + PENDING_SECONDS * 1000) throw new Error("invalid pending");
  if (payload.expires_at <= now || payload.issued_at < now - PENDING_SECONDS * 1000) {
    const error = new Error("expired pending");
    error.expired = true;
    error.returnTo = returnTo;
    throw error;
  }
  return { ...payload, return_to: returnTo };
}

function validateUser(user) {
  if (!user || typeof user !== "object" || !Number.isSafeInteger(user.id) || user.id <= 0) throw new Error("invalid user");
  if (!validString(user.login, 1, 100, /^[A-Za-z0-9-]+$/)) throw new Error("invalid user");
  if (!validString(user.avatar_url, 1, 500)) throw new Error("invalid user");
  const avatar = new URL(user.avatar_url);
  if (avatar.protocol !== "https:") throw new Error("invalid user");
  return { id: user.id, login: user.login, avatar_url: avatar.toString() };
}

function tokenPayload(result, previous, now) {
  if (!result || typeof result !== "object" || !validString(result.access_token, 1, 2048)) throw new Error("invalid token");
  if (String(result.token_type || previous?.token_type || "").toLowerCase() !== "bearer") throw new Error("invalid token");
  const expiresIn = Number(result.expires_in);
  if (!Number.isSafeInteger(expiresIn) || expiresIn <= 0 || expiresIn > MAX_SESSION_SECONDS) throw new Error("invalid token");
  const refreshToken = result.refresh_token === undefined ? previous?.refresh_token : result.refresh_token;
  const refreshExpiresIn = result.refresh_token_expires_in === undefined ? null : Number(result.refresh_token_expires_in);
  const refreshExpiresAt = refreshExpiresIn === null
    ? previous?.refresh_expires_at || null
    : now + refreshExpiresIn * 1000;
  if (refreshToken !== undefined && refreshToken !== null && !validString(refreshToken, 1, 2048)) throw new Error("invalid token");
  if (refreshExpiresAt !== null && (!Number.isSafeInteger(refreshExpiresAt) || refreshExpiresAt <= now)) throw new Error("invalid token");
  return {
    access_token: result.access_token,
    token_type: "bearer",
    access_expires_at: now + expiresIn * 1000,
    refresh_token: refreshToken || null,
    refresh_expires_at: refreshExpiresAt,
  };
}

function validateSession(payload, now) {
  if (payload.v !== 1 || !Number.isSafeInteger(payload.issued_at) || payload.issued_at > now + 60_000) throw new Error("invalid session");
  if (!validString(payload.access_token, 1, 2048) || payload.token_type !== "bearer") throw new Error("invalid session");
  if (!Number.isSafeInteger(payload.access_expires_at) || payload.access_expires_at <= payload.issued_at) throw new Error("invalid session");
  if (payload.refresh_token !== null && !validString(payload.refresh_token, 1, 2048)) throw new Error("invalid session");
  if (payload.refresh_expires_at !== null && !Number.isSafeInteger(payload.refresh_expires_at)) throw new Error("invalid session");
  payload.user = validateUser(payload.user);
  const absoluteExpiry = payload.refresh_token ? payload.refresh_expires_at : payload.access_expires_at;
  if (!Number.isSafeInteger(absoluteExpiry) || absoluteExpiry <= now || absoluteExpiry > payload.issued_at + MAX_SESSION_SECONDS * 1000 + 60_000) {
    throw new Error("expired session");
  }
  return payload;
}

async function githubTokenRequest(fields, secret, config, fetchImpl) {
  const body = new URLSearchParams(fields);
  body.set("client_id", config.githubClientID);
  body.set("client_secret", secret);
  const response = await fetchImpl(config.githubTokenURL, {
    method: "POST",
    headers: { Accept: "application/json", "Content-Type": "application/x-www-form-urlencoded" },
    body: body.toString(),
    redirect: "manual",
  });
  if (!response.ok) throw new Error("token request failed");
  const value = await response.json();
  if (value?.error) throw new Error("token request failed");
  return value;
}

async function githubUser(token, config, fetchImpl) {
  const response = await fetchImpl(`${config.githubAPIOrigin}/user`, {
    headers: {
      Accept: "application/vnd.github+json",
      Authorization: `Bearer ${token}`,
      "X-GitHub-Api-Version": GITHUB_API_VERSION,
      "User-Agent": "meads.jpillora.com",
    },
    redirect: "manual",
  });
  if (!response.ok) throw new Error("identity request failed");
  return validateUser(await response.json());
}

function configured(secret, config) {
  return validString(secret, 1, 1024) && validString(config.githubClientID, 5, 200) && validString(config.githubAppSlug, 1, 100, /^[a-z0-9-]+$/);
}

async function handleLogin(request, env, deps, config) {
  const url = new URL(request.url);
  const returnTo = validateReturnTo(url.searchParams.get("return_to"), config);
  if (!configured(env.GITHUB_CLIENT_SECRET, config)) return redirect(safeErrorLocation(returnTo, "configuration", config));
  const now = deps.now();
  const state = randomBase64URL(32, deps.crypto);
  const verifier = randomBase64URL(32, deps.crypto);
  const pending = await sealCookie({
    v: 1,
    state,
    verifier,
    issued_at: now,
    expires_at: now + PENDING_SECONDS * 1000,
    return_to: returnTo,
  }, env.GITHUB_CLIENT_SECRET, "pending", config, deps.crypto);
  const target = new URL(config.githubAuthorizeURL);
  target.searchParams.set("client_id", config.githubClientID);
  target.searchParams.set("redirect_uri", config.githubCallbackURL);
  target.searchParams.set("state", state);
  target.searchParams.set("code_challenge", await sha256Base64URL(verifier, deps.crypto));
  target.searchParams.set("code_challenge_method", "S256");
  return withCookies(redirect(target.toString()), [cookieHeader(config.pendingCookie, pending, PENDING_SECONDS)]);
}

async function handleCallback(request, env, deps, config) {
  const url = new URL(request.url);
  const pendingWire = parseCookies(request).get(config.pendingCookie);
  const cleared = clearCookie(config.pendingCookie);
  if (!configured(env.GITHUB_CLIENT_SECRET, config)) {
    return withCookies(redirect(safeErrorLocation("/", "configuration", config)), [cleared]);
  }

  let pending;
  try {
    pending = validatePending(await openCookie(pendingWire, env.GITHUB_CLIENT_SECRET, "pending", config, deps.crypto), deps.now(), config);
  } catch (error) {
    const code = error?.expired ? "expired" : "invalid_state";
    return withCookies(redirect(safeErrorLocation(error?.returnTo || "/", code, config)), [cleared]);
  }

  const returnTo = pending.return_to;
  const state = url.searchParams.get("state") || "";
  if (!validString(state, 22, 200, /^[A-Za-z0-9_-]+$/) || !constantTimeEqual(state, pending.state)) {
    return withCookies(redirect(safeErrorLocation(returnTo, "invalid_state", config)), [cleared]);
  }
  if (url.searchParams.get("error")) {
    return withCookies(redirect(safeErrorLocation(returnTo, "denied", config)), [cleared]);
  }
  const code = url.searchParams.get("code") || "";
  if (!validString(code, 1, 1024)) {
    return withCookies(redirect(safeErrorLocation(returnTo, "invalid_state", config)), [cleared]);
  }

  let token;
  try {
    token = tokenPayload(await githubTokenRequest({
      code,
      redirect_uri: config.githubCallbackURL,
      code_verifier: pending.verifier,
    }, env.GITHUB_CLIENT_SECRET, config, deps.fetch), null, deps.now());
  } catch {
    return withCookies(redirect(safeErrorLocation(returnTo, "exchange_failed", config)), [cleared]);
  }

  let user;
  try {
    user = await githubUser(token.access_token, config, deps.fetch);
  } catch {
    return withCookies(redirect(safeErrorLocation(returnTo, "identity_failed", config)), [cleared]);
  }

  const now = deps.now();
  const session = {
    v: 1,
    issued_at: now,
    ...token,
    user,
  };
  try {
    const wire = await sealCookie(session, env.GITHUB_CLIENT_SECRET, "session", config, deps.crypto);
    const expiresAt = session.refresh_token ? session.refresh_expires_at : session.access_expires_at;
    const maxAge = Math.min(MAX_SESSION_SECONDS, Math.max(0, Math.floor((expiresAt - now) / 1000)));
    return withCookies(redirect(returnTo), [cleared, cookieHeader(config.sessionCookie, wire, maxAge)]);
  } catch {
    return withCookies(redirect(safeErrorLocation(returnTo, "configuration", config)), [cleared]);
  }
}

function loggedOut(config, clear = false) {
  const response = json({ authenticated: false });
  return clear ? withCookies(response, [clearCookie(config.sessionCookie)]) : response;
}

async function readSession(request, env, deps, config) {
  if (!configured(env.GITHUB_CLIENT_SECRET, config)) return loggedOut(config, Boolean(parseCookies(request).get(config.sessionCookie)));
  let session;
  try {
    session = validateSession(await openCookie(parseCookies(request).get(config.sessionCookie), env.GITHUB_CLIENT_SECRET, "session", config, deps.crypto), deps.now());
  } catch {
    return loggedOut(config, Boolean(parseCookies(request).get(config.sessionCookie)));
  }

  let rotated = false;
  const now = deps.now();
  if (session.access_expires_at <= now + REFRESH_WINDOW_MS) {
    if (!session.refresh_token || !session.refresh_expires_at || session.refresh_expires_at <= now) return loggedOut(config, true);
    try {
      const refreshed = tokenPayload(await githubTokenRequest({
        grant_type: "refresh_token",
        refresh_token: session.refresh_token,
      }, env.GITHUB_CLIENT_SECRET, config, deps.fetch), session, now);
      session = { ...session, ...refreshed, issued_at: now };
      validateSession(session, now);
      rotated = true;
    } catch {
      return loggedOut(config, true);
    }
  }

  let response = json({
    authenticated: true,
    access_token: session.access_token,
    token_type: "bearer",
    expires_at: new Date(session.access_expires_at).toISOString(),
    user: session.user,
  });
  if (rotated) {
    const wire = await sealCookie(session, env.GITHUB_CLIENT_SECRET, "session", config, deps.crypto);
    const expiresAt = session.refresh_token ? session.refresh_expires_at : session.access_expires_at;
    response = withCookies(response, [cookieHeader(config.sessionCookie, wire, Math.min(MAX_SESSION_SECONDS, (expiresAt - now) / 1000))]);
  }
  return response;
}

async function deleteSession(request, env, deps, config) {
  const origin = request.headers.get("Origin");
  if (origin && origin !== config.publicOrigin) return json({ error: "invalid_origin" }, 403);
  let accessToken = "";
  if (configured(env.GITHUB_CLIENT_SECRET, config)) {
    try {
      const session = validateSession(await openCookie(parseCookies(request).get(config.sessionCookie), env.GITHUB_CLIENT_SECRET, "session", config, deps.crypto), deps.now());
      accessToken = session.access_token;
    } catch { /* an invalid session is already logged out */ }
  }
  if (accessToken) {
    try {
      await deps.fetch(`${config.githubAPIOrigin}/applications/${encodeURIComponent(config.githubClientID)}/grant`, {
        method: "DELETE",
        headers: {
          Accept: "application/vnd.github+json",
          Authorization: `Basic ${btoa(`${config.githubClientID}:${env.GITHUB_CLIENT_SECRET}`)}`,
          "Content-Type": "application/json",
          "X-GitHub-Api-Version": GITHUB_API_VERSION,
          "User-Agent": "meads.jpillora.com",
        },
        body: JSON.stringify({ access_token: accessToken }),
        redirect: "manual",
      });
    } catch { /* revocation is deliberately best effort */ }
  }
  return withCookies(applyHeaders(new Response(null, { status: 204, headers: AUTH_HEADERS })), [
    clearCookie(config.pendingCookie),
    clearCookie(config.sessionCookie),
  ]);
}

async function handleAuth(request, env, deps, config) {
  const path = new URL(request.url).pathname;
  if (path === "/_/github/config") {
    if (request.method !== "GET") return methodNotAllowed("GET");
    return json({
      app_slug: config.githubAppSlug,
      login_url: "/_/github/login",
      session_url: "/_/github/session",
      logout_url: "/_/github/session",
      install_url: "/_/github/install",
    });
  }
  if (path === "/_/github/login") {
    if (request.method !== "GET") return methodNotAllowed("GET");
    return handleLogin(request, env, deps, config);
  }
  if (path === "/_/github/callback") {
    if (request.method !== "GET") return methodNotAllowed("GET");
    return handleCallback(request, env, deps, config);
  }
  if (path === "/_/github/session") {
    if (request.method === "GET") return readSession(request, env, deps, config);
    if (request.method === "DELETE") return deleteSession(request, env, deps, config);
    return methodNotAllowed("GET, DELETE");
  }
  if (path === "/_/github/install") {
    if (request.method !== "GET") return methodNotAllowed("GET");
    return redirect(`https://github.com/apps/${config.githubAppSlug}/installations/new`);
  }
  return json({ error: "not_found" }, 404);
}

function upstreamURL(url, config) {
  const prefix = config.upstreamAssetPrefix.endsWith("/") ? config.upstreamAssetPrefix.slice(0, -1) : config.upstreamAssetPrefix;
  return `${config.upstreamAssetOrigin}${prefix}${url.pathname}${url.search}`;
}

function rewrittenLocation(value, target, config) {
  let upstream;
  try { upstream = new URL(value, target); } catch { return null; }
  const allowedHosts = new Set([new URL(config.upstreamAssetOrigin).host, "jpillora.com"]);
  if (!allowedHosts.has(upstream.host)) return null;
  const prefix = config.upstreamAssetPrefix.endsWith("/") ? config.upstreamAssetPrefix.slice(0, -1) : config.upstreamAssetPrefix;
  if (upstream.pathname !== prefix && !upstream.pathname.startsWith(`${prefix}/`)) return null;
  const path = upstream.pathname.slice(prefix.length) || "/";
  return `${config.publicOrigin}${path.startsWith("/") ? path : `/${path}`}${upstream.search}${upstream.hash}`;
}

async function assetFetch(request, deps, config) {
  if (request.method !== "GET" && request.method !== "HEAD") {
    return applyHeaders(new Response("Method Not Allowed", { status: 405, headers: { Allow: "GET, HEAD" } }));
  }
  const incoming = new URL(request.url);
  const target = upstreamURL(incoming, config);
  const headers = new Headers();
  for (const name of ["Accept", "If-None-Match", "If-Modified-Since", "Range"]) {
    const value = request.headers.get(name);
    if (value) headers.set(name, value);
  }
  let response;
  try {
    response = await deps.fetch(target, { method: request.method, headers, redirect: "manual" });
  } catch {
    return applyHeaders(new Response("Bad Gateway", { status: 502 }));
  }

  if (response.status >= 300 && response.status < 400) {
    const location = response.headers.get("Location");
    const publicLocation = location && rewrittenLocation(location, target, config);
    if (!publicLocation) return applyHeaders(new Response("Bad Gateway", { status: 502 }));
    // GitHub Pages redirects project sites to the account's custom domain. A
    // redirect back to this exact canonical path would loop in the browser, so
    // follow that one hop server-side and return the asset instead.
    if (publicLocation === `${config.publicOrigin}${incoming.pathname}${incoming.search}`) {
      try {
        response = await deps.fetch(new URL(location, target).toString().replace(/^http:/, "https:"), {
          method: request.method,
          headers,
          redirect: "follow",
        });
      } catch {
        return applyHeaders(new Response("Bad Gateway", { status: 502 }));
      }
    } else {
      return applyHeaders(new Response(null, { status: response.status, headers: { Location: publicLocation } }));
    }
  }

  const outputHeaders = new Headers();
  for (const name of ["Content-Type", "Cache-Control", "ETag", "Last-Modified", "Expires", "Accept-Ranges", "Content-Range", "Vary"]) {
    const value = response.headers.get(name);
    if (value) outputHeaders.set(name, value);
  }
  if (incoming.pathname.toLowerCase().endsWith(".wasm")) outputHeaders.set("Content-Type", "application/wasm");
  if (!outputHeaders.has("Cache-Control") && response.ok) outputHeaders.set("Cache-Control", "public, max-age=3600");
  return applyHeaders(new Response(request.method === "HEAD" ? null : response.body, {
    status: response.status,
    statusText: response.statusText,
    headers: outputHeaders,
  }));
}

export function createWorker({
  config = PUBLIC_CONFIG,
  fetchImpl = globalThis.fetch,
  cryptoImpl = globalThis.crypto,
  now = () => Date.now(),
} = {}) {
  const deps = { fetch: (...args) => fetchImpl(...args), crypto: cryptoImpl, now };
  return {
    async fetch(request, env = {}) {
      const url = new URL(request.url);
      const loopback = url.hostname === "localhost" || url.hostname === "127.0.0.1" || url.hostname === "[::1]";
      if (url.protocol === "http:" && !loopback) {
        url.protocol = "https:";
        const authPath = url.pathname === "/_/github" || url.pathname.startsWith("/_/github/");
        return applyHeaders(Response.redirect(url.toString(), 308), authPath ? AUTH_HEADERS : {});
      }
      if (url.pathname === "/_/github" || url.pathname.startsWith("/_/github/")) {
        return handleAuth(request, env, deps, config);
      }
      return assetFetch(request, deps, config);
    },
  };
}

export default createWorker();
