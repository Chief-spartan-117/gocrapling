// Needs a live server: GOCRAPLING_URL, GOCRAPLING_TOKEN, GOCRAPLING_TARGET.
import { test } from "node:test";
import assert from "node:assert/strict";
import { Client, GocraplingError } from "../index.js";

const { GOCRAPLING_URL: url, GOCRAPLING_TOKEN: token, GOCRAPLING_TARGET: target } = process.env;
const live = { skip: !(url && target) && "needs a running server" };
const client = () => new Client({ baseUrl: url, token });

test("fetch css results", live, async () => {
  const r = await client().fetch(target, { css: "h1::text" });
  assert.deepEqual(r.results, ["Hello"]);
});

test("markdown strips hidden text with ai_targeted", live, async () => {
  const r = await client().fetch(target, { ai_targeted: true });
  assert.match(r.content, /# Hello/);
  assert.doesNotMatch(r.content, /IGNORE ME/);
});

test("crawl", live, async () => {
  const r = await client().crawl(target, { title: "h1::text" }, { max_pages: 3 });
  assert.ok(r.items.length > 0);
});

test("errors carry status and code", live, async () => {
  await assert.rejects(client().fetch("file:///etc/hosts"), (e) => {
    assert.ok(e instanceof GocraplingError);
    assert.equal(e.status, 400);
    assert.equal(e.code, "invalid_url");
    return true;
  });
  await assert.rejects(new Client({ baseUrl: url, token: "wrong" }).fetch(target), (e) => e.status === 401);
});

test("openapi", live, async () => {
  assert.ok((await client().openapi()).paths["/v1/fetch"]);
});

test("unreachable server", async () => {
  await assert.rejects(new Client({ baseUrl: "http://127.0.0.1:1", token: "x", timeoutMs: 2000 }).fetch("http://example.com"), GocraplingError);
});
