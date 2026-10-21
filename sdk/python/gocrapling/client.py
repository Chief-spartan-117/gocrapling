"""Standard-library client for the gocrapling REST API (no dependencies).

Start a server with ``gocrapling mcp -http 127.0.0.1:8080 -token $TOKEN`` (or
``docker compose up``), then::

    from gocrapling import Client

    c = Client("http://127.0.0.1:8080", token="...")
    page = c.fetch("https://example.com", css="h1::text")
    print(page.results)
"""

import base64
import json
import os
import urllib.error
import urllib.request
from dataclasses import dataclass, field
from typing import Any, Dict, List, Optional


class GocraplingError(Exception):
    """The server refused or failed a request.

    ``status`` is the HTTP status (0 for network errors), ``code`` the API
    error code such as ``invalid_url``, ``target_not_allowed`` or
    ``fetch_failed``.
    """

    def __init__(self, message: str, status: int = 0, code: str = ""):
        super().__init__(message)
        self.status = status
        self.code = code


@dataclass
class FetchResult:
    status: int
    url: str
    content: str
    blocked: bool = False
    truncated: bool = False
    warning: str = ""
    results: List[str] = field(default_factory=list)


@dataclass
class Captured:
    url: str
    method: str
    status: int
    mime_type: str
    body: str


@dataclass
class CrawlResult:
    stats: str
    items: List[Dict[str, Any]]
    error: str = ""


class Client:
    """Synchronous client. Thread-safe: it holds no per-request state."""

    def __init__(
        self,
        base_url: str = "http://127.0.0.1:8080",
        token: Optional[str] = None,
        timeout: float = 330.0,
    ):
        """``token`` defaults to $GOCRAPLING_MCP_TOKEN. ``timeout`` is in
        seconds and should exceed the slowest call (crawls can take minutes)."""
        self.base_url = base_url.rstrip("/")
        self.token = token if token is not None else os.environ.get("GOCRAPLING_MCP_TOKEN", "")
        self.timeout = timeout

    # -- endpoints -------------------------------------------------------

    def fetch(
        self,
        url: str,
        *,
        mode: str = "http",
        css: str = "",
        xpath: str = "",
        main_only: bool = False,
        raw_html: bool = False,
        max_chars: int = 0,
        ai_targeted: bool = False,
        wait_selector: str = "",
        network_idle: bool = False,
        actions: Optional[List[Dict[str, Any]]] = None,
        state_file: str = "",
    ) -> FetchResult:
        """Fetch a page. ``mode="browser"`` runs JavaScript in stealth Chrome.

        Returns Markdown in ``content``; with ``css`` or ``xpath`` the matches
        are in ``results``. ``ai_targeted`` strips hidden text so it cannot
        inject instructions into a language model."""
        body = _drop_empty(
            url=url, mode=mode if mode != "http" else "", css=css, xpath=xpath,
            main_only=main_only, raw_html=raw_html, max_chars=max_chars,
            ai_targeted=ai_targeted, wait_selector=wait_selector,
            network_idle=network_idle, actions=actions, state_file=state_file,
        )
        r = self._post("/v1/fetch", body)
        return FetchResult(
            status=r["status"], url=r["url"], content=r.get("content", ""),
            blocked=r.get("blocked", False), truncated=r.get("truncated", False),
            warning=r.get("warning", ""), results=r.get("results") or [],
        )

    def screenshot(self, url: str) -> bytes:
        """Full-page PNG bytes rendered in stealth Chrome."""
        r = self._post("/v1/screenshot", {"url": url})
        return base64.b64decode(r["data"])

    def capture(self, url: str, url_regex: str = "", max_chars: int = 0) -> List[Captured]:
        """Background XHR/fetch responses the page makes (often its JSON API)."""
        r = self._post("/v1/capture", _drop_empty(url=url, url_regex=url_regex, max_chars=max_chars))
        return [Captured(**c) for c in r.get("captured") or []]

    def crawl(
        self,
        start_url: str,
        fields: Dict[str, str],
        *,
        follow_css: str = "",
        allow: str = "",
        max_pages: int = 0,
        depth: int = 0,
    ) -> CrawlResult:
        """Polite same-host crawl (robots.txt obeyed). ``fields`` maps a name to
        a CSS selector such as ``"h1::text"`` or ``"a::attr(href)"``."""
        r = self._post(
            "/v1/crawl",
            _drop_empty(start_url=start_url, fields=fields, follow_css=follow_css,
                        allow=allow, max_pages=max_pages, depth=depth),
        )
        return CrawlResult(stats=r.get("stats", ""), items=r.get("items") or [], error=r.get("error", ""))

    def openapi(self) -> Dict[str, Any]:
        """The server's OpenAPI document."""
        return self._request("GET", "/v1/openapi.json", None)

    # -- plumbing --------------------------------------------------------

    def _post(self, path: str, body: Dict[str, Any]) -> Dict[str, Any]:
        return self._request("POST", path, body)

    def _request(self, method: str, path: str, body: Optional[Dict[str, Any]]) -> Dict[str, Any]:
        data = json.dumps(body).encode() if body is not None else None
        req = urllib.request.Request(self.base_url + path, data=data, method=method)
        req.add_header("Content-Type", "application/json")
        req.add_header("Accept", "application/json")
        if self.token:
            req.add_header("Authorization", "Bearer " + self.token)
        try:
            with urllib.request.urlopen(req, timeout=self.timeout) as resp:
                return json.loads(resp.read().decode())
        except urllib.error.HTTPError as e:
            raw = e.read().decode(errors="replace")
            code, msg = "", raw.strip() or e.reason
            try:
                err = json.loads(raw)["error"]
                code, msg = err.get("code", ""), err.get("message", msg)
            except (ValueError, KeyError, TypeError):
                pass
            raise GocraplingError(msg, status=e.code, code=code) from None
        except urllib.error.URLError as e:
            raise GocraplingError(f"cannot reach {self.base_url}: {e.reason}") from None


def _drop_empty(**kw: Any) -> Dict[str, Any]:
    """Omit unset options so the server applies its defaults."""
    return {k: v for k, v in kw.items() if v not in ("", 0, None, False)}
