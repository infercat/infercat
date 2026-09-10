import { beforeAll, afterAll, describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { Miniflare, convertV4MiniflareOptions } from "miniflare";

const workerOptions = {
  modules: true,
  scriptPath: "dist/index.js",
  compatibilityDate: "2026-06-11",
  durableObjects: { HOSTS: { className: "Host", useSQLite: true } },
  kvNamespaces: ["REGISTRY"],
};
let mf, kv;
const sockets = [];
beforeAll(async () => {
  mf = new Miniflare(convertV4MiniflareOptions(workerOptions));
  kv = await mf.getKVNamespace("REGISTRY");
});
afterAll(async () => {
  for (const s of sockets)
    try {
      s.close();
    } catch {}
  await mf?.dispose();
});
let serial = 0;
async function register(host = `fixture-${++serial}`, runtime = mf) {
  const registry = runtime === mf ? kv : await runtime.getKVNamespace("REGISTRY");
  const code = `registration-code-${++serial}`;
  await registry.put("reg:" + code, host);
  const result = await runtime.dispatchFetch("https://test/register", {
    method: "POST",
    body: JSON.stringify({ code }),
  });
  expect(result.status).toBe(200);
  return { ...(await result.json()), code, runtime };
}
const digest = (key) => createHash("sha256").update(key).digest("hex");
async function socket(c, hashes = [digest("friend")], slots) {
  const result = await c.runtime.dispatchFetch(`https://test/h/${c.host}/socket`, {
    headers: { upgrade: "websocket", authorization: `Bearer ${c.token}` },
  });
  expect(result.status).toBe(101);
  const ws = result.webSocket;
  ws.accept();
  sockets.push(ws);
  const messages = [],
    waiters = [];
  ws.addEventListener("message", (event) => {
    const f = JSON.parse(event.data);
    const waiter = waiters.shift();
    if (waiter) waiter(f);
    else messages.push(f);
  });
  const peer = {
    ws,
    send: (f) => ws.send(JSON.stringify(f)),
    next: () =>
      messages.length
        ? Promise.resolve(messages.shift())
        : new Promise((resolve) => waiters.push(resolve)),
  };
  peer.send({ type: "keys", hashes, slots });
  expect(await peer.next()).toEqual(slots === undefined
    ? { type: "keys_ready" } : { type: "keys_ready", slots: Math.min(64, Math.max(1, slots)) });
  return peer;
}
const post = (c, body = "{}", headers = {}) =>
  c.runtime.dispatchFetch(`https://test/h/${c.host}/v1/chat/completions?x=1`, {
    method: "POST",
    body,
    headers: {
      authorization: "Bearer friend",
      "content-type": "application/json",
      ...headers,
    },
  });
async function incoming(s) {
  const start = await s.next();
  expect(start.type).toBe("request");
  const chunks = [];
  for (;;) {
    const f = await s.next();
    expect(f.id).toBe(start.id);
    if (f.type === "end") break;
    expect(f.type).toBe("body");
    expect(Buffer.byteLength(JSON.stringify(f))).toBeLessThanOrEqual(1048576);
    chunks.push(Buffer.from(f.data, "base64"));
  }
  return { ...start, body: Buffer.concat(chunks) };
}
async function respond(s, job, pending, body = "hello", status = 200) {
  s.send({
    type: "response",
    id: job.id,
    status,
    headers: {
      "content-type": "text/event-stream",
      "set-cookie": "not-forwarded",
    },
  });
  const response = await pending;
  const read = response.text();
  if (body) {
    s.send({
      type: "data",
      id: job.id,
      data: Buffer.from(body).toString("base64"),
    });
    expect(await s.next()).toEqual({ type: "ack", id: job.id });
  }
  s.send({ type: "end", id: job.id });
  expect(await s.next()).toEqual({ type: "ready", id: job.id });
  return { response, body: await read };
}
describe("Worker and hibernating Durable Object", () => {
  it("refuses invalid registration and routes", async () => {
    expect(
      (
        await mf.dispatchFetch("https://test/register", {
          method: "POST",
          body: "{}",
        })
      ).status,
    ).toBe(400);
    expect(
      (
        await mf.dispatchFetch("https://test/register", {
          method: "POST",
          body: JSON.stringify({ code: "unknown-code-long-enough" }),
        })
      ).status,
    ).toBe(403);
    expect((await mf.dispatchFetch("https://test/admin")).status).toBe(404);
  });
  it("consumes a registration code once even with concurrent requests and stale KV", async () => {
    const code = `concurrent-registration-${++serial}`;
    await kv.put("reg:" + code, `host-${++serial}`);
    const send = () =>
      mf.dispatchFetch("https://test/register", {
        method: "POST",
        body: JSON.stringify({ code }),
      });
    const results = await Promise.all([send(), send()]);
    expect(results.map((r) => r.status).sort()).toEqual([200, 409]);
    expect((await send()).status).toBe(409);
  });
  it("requires the bridge token for the host socket", async () => {
    const c = await register();
    const res = await mf.dispatchFetch(`https://test/h/${c.host}/socket`, {
      headers: {
        upgrade: "websocket",
        authorization: "Bearer " + "0".repeat(64),
      },
    });
    expect(res.status).toBe(401);
  });
  it("refuses missing and unknown friend keys before admission", async () => {
    const c = await register(),
      s = await socket(c, [digest("allowed")]);
    for (const authorization of [undefined, "Bearer wrong"]) {
      const headers = { "content-type": "application/json" };
      if (authorization) headers.authorization = authorization;
      const response = await mf.dispatchFetch(
        `https://test/h/${c.host}/v1/chat/completions`,
        { method: "POST", body: "{}", headers },
      );
      expect(response.status).toBe(401);
      const error = (await response.json()).error;
      expect(error.code).toBe("invalid_key");
      expect(error.type).toBe("authentication_error");
      expect(error.message).toBe(
        authorization
          ? "unknown key; check the invite"
          : "missing or malformed Authorization header; expected: Bearer <invite secret>",
      );
    }
    const allowed = mf.dispatchFetch(
      `https://test/h/${c.host}/v1/chat/completions`,
      {
        method: "POST",
        body: "{}",
        headers: { authorization: "Bearer allowed" },
      },
    );
    await respond(s, await incoming(s), allowed);
  });
  it("replaces the persisted key set across socket loss", async () => {
    const c = await register(),
      first = await socket(c, [digest("old")]);
    first.ws.close();
    await new Promise((resolve) => setTimeout(resolve, 30));
    // No live host socket: admission still reads the durable set, then reports host_offline.
    expect(
      (
        await mf.dispatchFetch(`https://test/h/${c.host}/v1/models`, {
          headers: { authorization: "Bearer old" },
        })
      ).status,
    ).toBe(503);
    expect(
      (
        await mf.dispatchFetch(`https://test/h/${c.host}/v1/models`, {
          headers: { authorization: "Bearer wrong" },
        })
      ).status,
    ).toBe(401);

    const second = await socket(c, [digest("new")]);
    expect(
      (
        await mf.dispatchFetch(`https://test/h/${c.host}/v1/models`, {
          headers: { authorization: "Bearer old" },
        })
      ).status,
    ).toBe(401);
    const admitted = mf.dispatchFetch(`https://test/h/${c.host}/v1/models`, {
      headers: { authorization: "Bearer new" },
    });
    await respond(second, await incoming(second), admitted);
  });
  it("frames requests at the cap, limits headers, and streams response bytes", async () => {
    const c = await register(),
      s = await socket(c);
    const body = "x".repeat(4 * 1024 * 1024);
    const pending = post(c, body, { "x-private-header": "never" }),
      job = await incoming(s);
    expect(job.body.toString()).toBe(body);
    expect(job.path).toBe("/v1/chat/completions?x=1");
    expect(job.headers).toEqual({
      authorization: "Bearer friend",
      "content-type": "application/json",
      accept: "*/*",
    });
    const got = await respond(
      s,
      job,
      pending,
      "data: first\n\ndata: [DONE]\n\n",
    );
    expect(got.body).toContain("data: first");
    expect(got.response.headers.get("set-cookie")).toBeNull();
    expect(got.response.headers.get("cache-control")).toBe("no-store");
    const again = post(c);
    expect((await respond(s, await incoming(s), again, "second")).body).toBe(
      "second",
    );
  });
  it("refuses an oversized body before forwarding and retains a usable socket", async () => {
    const c = await register(),
      s = await socket(c);
    expect((await post(c, "x".repeat(4 * 1024 * 1024 + 1))).status).toBe(413);
    const pending = post(c);
    expect((await respond(s, await incoming(s), pending)).body).toBe("hello");
  });
  it("serializes a bounded queue and refuses overflow with host_busy", async () => {
    const c = await register(),
      s = await socket(c),
      first = post(c);
    const job = await incoming(s);
    const waiting = Array.from({ length: 4 }, () => post(c));
    const overflow = await post(c);
    expect(overflow.status).toBe(429);
    expect((await overflow.json()).error.code).toBe("host_busy");
    await respond(s, job, first);
    for (const pending of waiting) await respond(s, await incoming(s), pending);
  });
  it("checks the disable list for requests and socket admission", async () => {
    const c = await register();
    await socket(c);
    await kv.put("disabled:" + c.host, "disabled");
    expect((await post(c)).status).toBe(403);
    expect(
      (
        await mf.dispatchFetch(`https://test/h/${c.host}/socket`, {
          headers: { upgrade: "websocket", authorization: `Bearer ${c.token}` },
        })
      ).status,
    ).toBe(403);
  });
  it("keeps the rate counter across reconnects", async () => {
    // A separate Worker clock keeps all 61 real storage transactions in one rate window.
    const runtime = new Miniflare(convertV4MiniflareOptions({
      ...workerOptions,
      scriptPath: undefined,
      script: `Date.now = () => 0;\n${readFileSync("dist/index.js", "utf8")}`,
    }));
    try {
      const c = await register(undefined, runtime), s = await socket(c);
      // The server's close reply follows disconnect(); elapsed time cannot prove that.
      const closed = new Promise((resolve) => s.ws.addEventListener("close", resolve, { once: true }));
      s.ws.close();
      await closed;
      for (let i = 0; i < 60; i++) expect((await post(c)).status).toBe(503);
      await socket(c);
      expect((await post(c)).status).toBe(429);
    } finally {
      await runtime.dispose();
    }
    // Includes isolated workerd startup, 61 persisted admissions and disposal on shared CI.
  }, 30_000);
  it("settles disconnected requests without replay on reconnect", async () => {
    const c = await register(),
      s = await socket(c),
      pending = post(c);
    await incoming(s);
    s.ws.close();
    expect((await pending).status).toBe(502);
    const next = await socket(c),
      fresh = post(c, "fresh");
    const job = await incoming(next);
    expect(job.body.toString()).toBe("fresh");
    await respond(next, job, fresh);
  });
  it("re-registration rotates the host credential and rejects the old token", async () => {
    const old = await register();
    await socket(old);
    const fresh = await register(old.host);
    expect(
      (
        await mf.dispatchFetch(`https://test/h/${old.host}/socket`, {
          headers: {
            upgrade: "websocket",
            authorization: `Bearer ${old.token}`,
          },
        })
      ).status,
    ).toBe(401);
    await socket(fresh);
  });
  it("auto-responds to keepalive using the hibernation API", async () => {
    const c = await register(),
      s = await socket(c);
    s.send({ type: "ping" });
    expect(await s.next()).toEqual({ type: "pong" });
  });
  it("host-requested cancellation preserves the real Worker socket and next job", async () => {
    const c = await register(),
      s = await socket(c),
      pending = post(c);
    const job = await incoming(s);
    s.send({
      type: "response",
      id: job.id,
      status: 200,
      headers: { "content-type": "text/event-stream" },
    });
    const response = await pending,
      reader = response.body.getReader();
    s.send({
      type: "data",
      id: job.id,
      data: Buffer.from("first chunk").toString("base64"),
    });
    expect((await reader.read()).done).toBe(false);
    expect(await s.next()).toEqual({ type: "ack", id: job.id });
    const next = post(c);
    void next.catch(() => {});
    // The HTTP boundary can surface a producer error as EOF or a rejected read.
    const closed = reader.read().then(
      (result) => result.done,
      () => true,
    );
    s.send({ type: "cancel", id: job.id });
    expect(await s.next()).toEqual({ type: "cancel", id: job.id });
    expect(await closed).toBe(true);
    s.send({ type: "ready", id: job.id });
    expect(
      (await respond(s, await incoming(s), next, "still connected")).body,
    ).toBe("still connected");
  });
});


it("streams three interleaved jobs over one real Worker socket", async () => {
  const c = await register(), s = await socket(c, [digest("friend")], 3);
  const pending = [], jobs = [];
  for (let i = 0; i < 3; i++) {
    pending.push(post(c, `body-${i}`));
    jobs.push(await incoming(s));
  }
  expect(jobs.map((j) => j.body.toString())).toEqual(["body-0", "body-1", "body-2"]);
  for (const job of jobs) s.send({ type: "response", id: job.id, status: 200 });
  const responses = await Promise.all(pending);
  const texts = responses.map((r) => r.text());
  for (const i of [2, 0, 1]) {
    s.send({ type: "data", id: jobs[i].id, data: Buffer.from(`result-${i}`).toString("base64") });
    expect(await s.next()).toEqual({ type: "ack", id: jobs[i].id });
  }
  for (const i of [1, 2, 0]) {
    s.send({ type: "end", id: jobs[i].id });
    expect(await s.next()).toEqual({ type: "ready", id: jobs[i].id });
  }
  expect(await Promise.all(texts)).toEqual(["result-0", "result-1", "result-2"]);
});
