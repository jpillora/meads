const CONFIG_URL = "/_/github/config";

const CONFIG_PATHS = {
  login_url: "/_/github/login",
  session_url: "/_/github/session",
  logout_url: "/_/github/session",
  install_url: "/_/github/install",
};

function genericError(message = "GitHub sign-in is temporarily unavailable") {
  const error = new Error(message);
  error.name = "GitHubBrokerError";
  return error;
}

function safePath(value, origin) {
  if (
    typeof value !== "string" || !value.startsWith("/") || value.startsWith("//") ||
    value.includes("\\") || /[\u0000-\u001f\u007f]/.test(value)
  ) {
    throw genericError();
  }
  const url = new URL(value, origin);
  if (url.origin !== origin || url.username || url.password) throw genericError();
  return url.pathname + url.search + url.hash;
}

async function jsonResponse(response) {
  const type = response.headers.get("content-type") || "";
  if (!type.toLowerCase().includes("application/json")) throw genericError();
  try {
    return await response.json();
  } catch {
    throw genericError();
  }
}

function validateConfig(value, origin) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw genericError();
  if (typeof value.app_slug !== "string" || !/^[a-z0-9][a-z0-9-]*$/i.test(value.app_slug)) {
    throw genericError();
  }
  const config = { app_slug: value.app_slug };
  for (const [field, expected] of Object.entries(CONFIG_PATHS)) {
    config[field] = safePath(value[field], origin);
    if (config[field] !== expected) throw genericError();
  }
  return config;
}

function validateSession(value) {
  if (!value || typeof value !== "object" || Array.isArray(value)) throw genericError();
  if (value.authenticated === false) return { authenticated: false };
  const user = value.user;
  if (
    value.authenticated !== true ||
    typeof value.access_token !== "string" || !value.access_token ||
    String(value.token_type || "").toLowerCase() !== "bearer" ||
    typeof value.expires_at !== "string" || Number.isNaN(Date.parse(value.expires_at)) ||
    !user || typeof user !== "object" || !Number.isSafeInteger(user.id) ||
    typeof user.login !== "string" || !user.login ||
    typeof user.avatar_url !== "string"
  ) {
    throw genericError();
  }
  return {
    authenticated: true,
    access_token: value.access_token,
    token_type: "bearer",
    expires_at: value.expires_at,
    user: { id: user.id, login: user.login, avatar_url: user.avatar_url },
  };
}

export class GitHubBroker {
  constructor({
    origin = globalThis.location?.origin,
    fetchImpl = globalThis.fetch,
  } = {}) {
    if (!origin) throw new Error("GitHubBroker requires an origin");
    this.origin = new URL(origin).origin;
    this.fetch = (...args) => Reflect.apply(fetchImpl, globalThis, args);
    this.available = false;
    this.config = null;
    this.session = { authenticated: false };
    this.problem = "";
  }

  get authenticated() { return this.session.authenticated === true; }
  get accessToken() { return this.authenticated ? this.session.access_token : ""; }
  get user() { return this.authenticated ? this.session.user : null; }

  clearSession() {
    this.session = { authenticated: false };
  }

  async request(path, options = {}) {
    const url = new URL(safePath(path, this.origin), this.origin);
    const headers = new Headers(options.headers || {});
    headers.set("Accept", "application/json");
    return this.fetch(url.href, {
      ...options,
      headers,
      credentials: "same-origin",
      cache: "no-store",
    });
  }

  async initialise() {
    this.available = false;
    this.config = null;
    this.problem = "";
    this.clearSession();

    let response;
    try {
      response = await this.request(CONFIG_URL);
      if (!response.ok) return this.snapshot();
      this.config = validateConfig(await jsonResponse(response), this.origin);
      this.available = true;
    } catch {
      return this.snapshot();
    }

    try {
      await this.loadSession();
    } catch (error) {
      this.problem = error.message;
      this.clearSession();
    }
    return this.snapshot();
  }

  async loadSession() {
    if (!this.available || !this.config) return this.snapshot();
    const response = await this.request(this.config.session_url);
    if (!response.ok) throw genericError();
    this.session = validateSession(await jsonResponse(response));
    this.problem = "";
    return this.snapshot();
  }

  loginURL(returnTo = "/") {
    if (!this.available || !this.config) throw genericError();
    const target = new URL(this.config.login_url, this.origin);
    target.searchParams.set("return_to", safePath(returnTo, this.origin));
    return target.href;
  }

  installURL() {
    if (!this.available || !this.config) throw genericError();
    return new URL(this.config.install_url, this.origin).href;
  }

  async logout() {
    if (!this.available || !this.config) {
      this.clearSession();
      return;
    }
    try {
      const response = await this.request(this.config.logout_url, { method: "DELETE" });
      if (response.status !== 204) throw genericError("GitHub sign-out could not be confirmed");
    } finally {
      this.clearSession();
    }
  }

  snapshot() {
    return {
      available: this.available,
      authenticated: this.authenticated,
      user: this.user ? { ...this.user } : null,
      expires_at: this.authenticated ? this.session.expires_at : "",
      problem: this.problem,
    };
  }
}

export function oauthErrorMessage(code) {
  switch (code) {
    case "denied": return "GitHub sign-in was cancelled.";
    case "invalid_state": return "GitHub sign-in could not be verified. Please try again.";
    case "expired": return "GitHub sign-in expired. Please try again.";
    case "exchange_failed": return "GitHub sign-in could not be completed. Please try again.";
    case "identity_failed": return "GitHub identity could not be loaded. Please try again.";
    case "configuration": return "GitHub sign-in is not configured yet.";
    default: return "";
  }
}
