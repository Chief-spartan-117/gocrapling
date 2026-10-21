# Using gocrapling from n8n

n8n needs no custom node. gocrapling serves a JSON API (`/v1/fetch`, `/v1/crawl`, ...), and n8n's built-in **HTTP Request** node calls it. Both example workflows below were imported into a real n8n container and executed successfully.

## 1. Start both services

```sh
cd examples/n8n
export GOCRAPLING_MCP_TOKEN=$(openssl rand -hex 32)
docker compose up -d --build
```

This starts gocrapling (reachable by n8n only, port 8080 is not published to your host) and n8n at <http://localhost:5678>.

Already running n8n elsewhere? Run gocrapling anywhere n8n can reach and keep the token secret:
`gocrapling mcp -http 0.0.0.0:8080` with `GOCRAPLING_MCP_TOKEN` set (see the root `docker-compose.yml`). Put TLS in front of it if it crosses a network you do not control.

## 2. Create the credential

In n8n: **Credentials, Create, Header Auth**.

| Field | Value |
| --- | --- |
| Name | `gocrapling` |
| Header name | `Authorization` |
| Header value | `Bearer <your token>` |

## 3. Import a workflow

**Workflows, Import from file**, then pick one from [`examples/n8n`](../examples/n8n):

| File | What it does |
| --- | --- |
| `fetch-one-page.json` | Fetches one URL and extracts text with a CSS selector (`title::text`). Shows `results`, `status`, `blocked`. |
| `scrape-list-of-pages.json` | Takes a list of URLs, loads each in the stealth browser one at a time with a 20 s pause, returns Markdown with hidden text stripped (`ai_targeted`). |

Open the HTTP Request node and select the `gocrapling` credential.

## 4. Build your own call

An **HTTP Request** node with:

- Method `POST`, URL `http://gocrapling:8080/v1/fetch` (use the host name that n8n can reach)
- Authentication: Generic credential type, Header Auth, your `gocrapling` credential
- Body: JSON, for example `={{ JSON.stringify({ url: $json.url, css: "h1::text" }) }}`
- Options, Timeout: `300000` (browser fetches and crawls can take minutes)

Response fields you can use in later nodes: `status`, `url`, `blocked`, `content` (Markdown), `results` (matches when you pass `css` or `xpath`), `truncated`, `warning`.

Other endpoints: `/v1/crawl` (several pages of one site), `/v1/capture` (the JSON APIs a page calls behind the scenes), `/v1/screenshot` (base64 PNG). The full contract is [`api/openapi.json`](../api/openapi.json), also served at `GET /v1/openapi.json`.

## Good practice

- **Pace requests.** Use the HTTP Request node's **Batching** option (batch size 1, interval 15–30 s) when looping over many URLs, as the list workflow does.
- **Check `blocked` and `status`.** An IF node after the call can send blocked pages to a retry path or an alert instead of storing junk.
- **Use `ai_targeted: true`** when the output goes into an AI node, so hidden text on a page cannot inject instructions.
- **Private addresses are refused** by default (`target_not_allowed`, HTTP 403). Start the server with `-allow-private` only when you need to scrape your own internal sites.
- **Respect each site's terms and robots.txt.** The crawl endpoint obeys robots.txt; single fetches do not check it.

## Why not a community node?

A packaged `n8n-nodes-gocrapling` node would add a nicer form UI, but it is a second thing to maintain and publish, and the HTTP Request node already covers every endpoint. If you want one, the OpenAPI spec is the source for its fields.
