# gocrapling (Python)

Zero-dependency client for the [gocrapling](https://github.com/Chief-spartan-117/gocrapling) REST API. Python 3.8+.

```sh
pip install ./clients/python        # from a checkout of the repository
```

Start a server first:

```sh
export GOCRAPLING_MCP_TOKEN=$(openssl rand -hex 32)
gocrapling mcp -http 127.0.0.1:8080          # or: docker compose up -d --build
```

```python
from gocrapling import Client, GocraplingError

c = Client("http://127.0.0.1:8080")           # token from $GOCRAPLING_MCP_TOKEN

page = c.fetch("https://example.com", css="h1::text")
print(page.results)                            # ['Example Domain']

md = c.fetch("https://example.com", mode="browser", main_only=True, ai_targeted=True)
print(md.content)

for item in c.crawl("https://example.com", {"title": "h1::text"}, max_pages=5).items:
    print(item)

open("shot.png", "wb").write(c.screenshot("https://example.com"))

try:
    c.fetch("http://169.254.169.254/")
except GocraplingError as e:
    print(e.status, e.code)                    # 403 target_not_allowed
```

The server refuses private and metadata addresses unless started with `-allow-private`.
