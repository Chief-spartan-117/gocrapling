# gocrapling (JavaScript / TypeScript)

Zero-dependency client for the [gocrapling](https://github.com/Chief-spartan-117/gocrapling) REST API. ESM, Node 18+ (also Deno, Bun), with TypeScript types.

```sh
npm install ./clients/js            # from a checkout of the repository
```

Start a server first:

```sh
export GOCRAPLING_MCP_TOKEN=$(openssl rand -hex 32)
gocrapling mcp -http 127.0.0.1:8080          # or: docker compose up -d --build
```

```js
import { Client, GocraplingError } from "gocrapling";

const c = new Client({ baseUrl: "http://127.0.0.1:8080" }); // token from $GOCRAPLING_MCP_TOKEN

const page = await c.fetch("https://example.com", { css: "h1::text" });
console.log(page.results);                                   // ['Example Domain']

const md = await c.fetch("https://example.com", { mode: "browser", main_only: true, ai_targeted: true });
const { items } = await c.crawl("https://example.com", { title: "h1::text" }, { max_pages: 5 });
const png = await c.screenshot("https://example.com");       // Uint8Array

try {
  await c.fetch("http://169.254.169.254/");
} catch (e) {
  if (e instanceof GocraplingError) console.log(e.status, e.code); // 403 target_not_allowed
}
```

Keep the token on a backend; do not ship it to a browser. The server refuses private and metadata addresses unless started with `-allow-private`.
