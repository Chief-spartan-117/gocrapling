export interface Step {
  type: "click" | "fill" | "press" | "wait" | "wait_for" | "scroll" | "evaluate";
  selector?: string;
  text?: string;
  key?: string;
  script?: string;
  ms?: number;
  pixels?: number;
  to?: string;
}

export interface FetchOptions {
  /** "http" (default, fast) or "browser" (runs JavaScript). */
  mode?: "http" | "browser";
  /** CSS selector; supports ::text and ::attr(name). */
  css?: string;
  xpath?: string;
  /** Markdown of <main>/<article> only. */
  main_only?: boolean;
  raw_html?: boolean;
  max_chars?: number;
  /** Strip hidden elements and invisible characters (prompt-injection defence). */
  ai_targeted?: boolean;
  /** Browser: wait until this selector exists. */
  wait_selector?: string;
  network_idle?: boolean;
  actions?: Step[];
  /** Browser: plain name of a saved login state. */
  state_file?: string;
}

export interface FetchResult {
  status: number;
  url: string;
  blocked: boolean;
  warning?: string;
  /** Markdown, raw HTML, or the matches joined by newlines. */
  content: string;
  truncated: boolean;
  /** Each match when css or xpath was given. */
  results?: string[];
}

export interface CaptureOptions {
  url_regex?: string;
  max_chars?: number;
}

export interface Captured {
  url: string;
  method: string;
  status: number;
  mime_type: string;
  body: string;
}

export interface CrawlOptions {
  follow_css?: string;
  /** Regexp links must match. */
  allow?: string;
  /** Default 20, max 200. */
  max_pages?: number;
  /** Default 2. */
  depth?: number;
}

export interface CrawlResult {
  stats: string;
  items: Record<string, unknown>[];
  /** Set when the crawl ended early; items still holds what was scraped. */
  error: string;
}

export interface ClientOptions {
  baseUrl?: string;
  /** Defaults to process.env.GOCRAPLING_MCP_TOKEN. */
  token?: string;
  timeoutMs?: number;
}

export class GocraplingError extends Error {
  status: number;
  code: string;
  constructor(message: string, status?: number, code?: string);
}

export class Client {
  constructor(opts?: ClientOptions);
  fetch(url: string, options?: FetchOptions): Promise<FetchResult>;
  screenshot(url: string): Promise<Uint8Array>;
  capture(url: string, options?: CaptureOptions): Promise<Captured[]>;
  crawl(startUrl: string, fields: Record<string, string>, options?: CrawlOptions): Promise<CrawlResult>;
  openapi(): Promise<Record<string, unknown>>;
}
