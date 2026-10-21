/**
 * Zero-dependency client for the gocrapling REST API. Needs a global `fetch`
 * (Node 18+, Deno, Bun, browsers).
 *
 * Do not ship the bearer token to a browser: call the server from a backend.
 */

export class GocraplingError extends Error {
  /**
   * @param {string} message
   * @param {number} status HTTP status, 0 for network errors
   * @param {string} code API error code, e.g. "invalid_url", "target_not_allowed"
   */
  constructor(message, status = 0, code = "") {
    super(message);
    this.name = "GocraplingError";
    this.status = status;
    this.code = code;
  }
}

export class Client {
  /**
   * @param {{baseUrl?: string, token?: string, timeoutMs?: number}} [opts]
   *   token defaults to process.env.GOCRAPLING_MCP_TOKEN. timeoutMs should
   *   exceed the slowest call (crawls can take minutes).
   */
  constructor({ baseUrl = "http://127.0.0.1:8080", token, timeoutMs = 330_000 } = {}) {
    this.baseUrl = baseUrl.replace(/\/+$/, "");
    this.token = token ?? (typeof process !== "undefined" ? process.env?.GOCRAPLING_MCP_TOKEN : "") ?? "";
    this.timeoutMs = timeoutMs;
  }

  /** Fetch a page. `mode: "browser"` runs JavaScript in stealth Chrome. */
  async fetch(url, options = {}) {
    return this.#post("/v1/fetch", { url, ...options });
  }

  /** Full-page PNG as a Uint8Array. */
  async screenshot(url) {
    const r = await this.#post("/v1/screenshot", { url });
    return Uint8Array.from(atob(r.data), (c) => c.charCodeAt(0));
  }

  /** Background XHR/fetch responses the page makes. */
  async capture(url, options = {}) {
    const r = await this.#post("/v1/capture", { url, ...options });
    return r.captured ?? [];
  }

  /** Polite same-host crawl; `fields` maps names to CSS selectors. */
  async crawl(startUrl, fields, options = {}) {
    const r = await this.#post("/v1/crawl", { start_url: startUrl, fields, ...options });
    return { stats: r.stats ?? "", items: r.items ?? [], error: r.error ?? "" };
  }

  /** The server's OpenAPI document. */
  async openapi() {
    return this.#request("GET", "/v1/openapi.json");
  }

  #post(path, body) {
    return this.#request("POST", path, body);
  }

  async #request(method, path, body) {
    const headers = { Accept: "application/json" };
    if (body !== undefined) headers["Content-Type"] = "application/json";
    if (this.token) headers.Authorization = `Bearer ${this.token}`;
    let res;
    try {
      res = await fetch(this.baseUrl + path, {
        method,
        headers,
        body: body === undefined ? undefined : JSON.stringify(body),
        signal: AbortSignal.timeout(this.timeoutMs),
      });
    } catch (e) {
      throw new GocraplingError(`cannot reach ${this.baseUrl}: ${e?.cause?.code ?? e?.message ?? e}`);
    }
    const text = await res.text();
    if (!res.ok) {
      let code = "";
      let message = text.trim() || res.statusText;
      try {
        const err = JSON.parse(text).error;
        code = err.code ?? "";
        message = err.message ?? message;
      } catch {
        /* not an API error body (e.g. 401 from the auth layer) */
      }
      throw new GocraplingError(message, res.status, code);
    }
    return JSON.parse(text);
  }
}
