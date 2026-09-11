import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { timingSafeEqual, randomUUID, createHash } from "node:crypto";
import { Host } from "./src/index";

let host: Host, sent: any[], ws: any, storage: any, hashes: Set<string>;
let timingChecks: ReturnType<typeof vi.fn>;
const drain = () => vi.advanceTimersByTimeAsync(0);
beforeEach(() => {
  vi.useFakeTimers();
  timingChecks = vi.fn(timingSafeEqual);
  vi.stubGlobal("crypto", {
    subtle: {
      digest: async (_algorithm: string, value: ArrayBufferView) => {
        const bytes = new Uint8Array(
          value.buffer,
          value.byteOffset,
          value.byteLength,
        );
        return Uint8Array.from(createHash("sha256").update(bytes).digest())
          .buffer;
      },
      timingSafeEqual: timingChecks,
    },
    randomUUID,
  });
  vi.stubGlobal("WebSocketRequestResponsePair", class {});
  sent = [];
  let attachment: any = { host: "fixture" };
  ws = { deserializeAttachment: () => attachment, serializeAttachment(value: any) { attachment = value; }, send: (s: string) => sent.push(JSON.parse(s)), close: vi.fn() };
  const values = new Map();
  hashes = new Set([createHash("sha256").update("friend").digest("hex")]);
  storage = {
    __values: values,
    sql: {
      exec: (query: string, ...args: string[]) => {
        if (query.startsWith("DELETE")) hashes.clear();
        if (query.startsWith("INSERT")) hashes.add(args[0]);
        return query.startsWith("SELECT")
          ? [...hashes].map((hash) => ({ hash }))
          : [];
      },
    },
    transactionSync: (f: () => unknown) => f(),
    transaction: async (fn: any) =>
      fn({
        get: async (key: string) => values.get(key),
        put: async (key: string, value: unknown) => values.set(key, value),
      }),
  };
  host = new Host(
    {
      getWebSockets: () => [ws],
      setWebSocketAutoResponse() {},
      storage,
    } as any,
    { REGISTRY: { get: async () => null } } as any,
  );
});
afterEach(() => {
  (host as any).disconnect();
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
const req = (body: any = "{}", signal?: AbortSignal) =>
  new Request("https://fixture/v1/chat/completions", {
    method: "POST",
    headers: { "x-bridge-host": "fixture", authorization: "Bearer friend" },
    body,
    duplex: "half",
    signal,
  } as RequestInit);
const frame = (f: object) => host.webSocketMessage(ws, JSON.stringify(f));
const requestFrames = () => sent.filter((f) => f.type === "request");
const keyHash = (secret: string) =>
  createHash("sha256").update(secret).digest("hex");
async function beginResponse(id: string, pending: Promise<Response>) {
  await frame({
    type: "response",
    id,
    status: 200,
    headers: { "content-type": "text/event-stream" },
  });
  return pending;
}
async function finishNext(pending: Promise<Response>) {
  const id = requestFrames().at(-1).id;
  const response = await beginResponse(id, pending),
    text = response.text();
  await frame({ type: "data", id, data: btoa("next job") });
  await frame({ type: "end", id });
  expect(await text).toBe("next job");
  expect(ws.close).not.toHaveBeenCalled();
}
it("client disconnect mid-stream cancels just its job and serves the queued job", async () => {
  const first = host.fetch(req());
  await drain();
  const id = requestFrames()[0].id;
  const next = host.fetch(req());
  await drain();
  const response = await beginResponse(id, first),
    reader = response.body!.getReader();
  const write = frame({ type: "data", id, data: btoa("first chunk") });
  expect((await reader.read()).done).toBe(false);
  await write;
  const pendingWrite = frame({
    type: "data",
    id,
    data: btoa("cancelled chunk"),
  });
  await reader.cancel();
  await pendingWrite;
  await drain();
  expect(sent).toContainEqual({ type: "cancel", id });
  expect(requestFrames()).toHaveLength(1);
  expect(ws.close).not.toHaveBeenCalled();
  await frame({ type: "ready", id });
  expect(requestFrames()).toHaveLength(2);
  await finishNext(next);
});
it("a stalled reader's host ack-timeout cancels only that response", async () => {
  const first = host.fetch(req());
  await drain();
  const id = requestFrames()[0].id;
  const next = host.fetch(req());
  await drain();
  await beginResponse(id, first);
  // A real TransformStream with no consumer leaves this write pending.
  let completed = false;
  const pendingWrite = frame({
    type: "data",
    id,
    data: btoa("blocked chunk"),
  }).then(() => {
    completed = true;
  });
  await drain();
  expect(completed).toBe(false);
  // The Go fixture separately verifies that missing ack cancels its request context.
  await frame({ type: "cancel", id });
  expect(sent).toContainEqual({ type: "cancel", id });
  await frame({ type: "ready", id });
  await pendingWrite;
  expect(requestFrames()).toHaveLength(2);
  await finishNext(next);
});
it("a queued client abort neither cancels the active job nor closes its socket", async () => {
  const first = host.fetch(req());
  await drain();
  const controller = new AbortController(),
    queued = host.fetch(req("{}", controller.signal));
  await drain();
  controller.abort();
  await drain();
  expect((await queued).status).toBe(499);
  expect(sent.some((f) => f.type === "cancel")).toBe(false);
  await finishNext(first);
});
it("an incomplete upload holds no host slot and times out directly with 408", async () => {
  const body = new ReadableStream({
    start(controller) {
      controller.enqueue(new TextEncoder().encode("{"));
    },
  });
  const uploading = host.fetch(req(body));
  await drain();
  expect(requestFrames()).toHaveLength(0);
  const fast = host.fetch(req());
  await drain();
  expect(requestFrames()).toHaveLength(1);
  await finishNext(fast);
  await vi.advanceTimersByTimeAsync(30000);
  expect((await uploading).status).toBe(408);
  expect(sent.some((f) => f.type === "cancel")).toBe(false);
  expect(ws.close).not.toHaveBeenCalled();
});
it("an upload enters the queue only once its complete body is available", async () => {
  let upload!: ReadableStreamDefaultController;
  const body = new ReadableStream({
    start(controller) {
      upload = controller;
      controller.enqueue(new TextEncoder().encode("{"));
    },
  });
  const uploading = host.fetch(req(body));
  await drain();
  expect(requestFrames()).toHaveLength(0);
  const fast = host.fetch(req());
  await drain();
  await finishNext(fast);
  upload.enqueue(new TextEncoder().encode("}"));
  upload.close();
  await drain();
  expect(requestFrames()).toHaveLength(2);
  await finishNext(uploading);
});

it("reserves four readers/16 MiB, refuses the next upload immediately, and releases every reservation", async () => {
  const uploads: ReadableStreamDefaultController[] = [];
  const pending = Array.from({ length: 4 }, () =>
    host.fetch(
      req(
        new ReadableStream({
          start(controller) {
            uploads.push(controller);
            controller.enqueue(new TextEncoder().encode("{"));
          },
        }),
      ),
    ),
  );
  await drain();
  expect((host as any).reading).toBe(4);
  expect(requestFrames()).toHaveLength(0);
  const excess = await host.fetch(req());
  expect(excess.status).toBe(429);
  expect((await excess.json()).error.code).toBe("host_busy");
  // No body means no upload buffer: a models request can still use the host slot.
  const models = host.fetch(
    new Request("https://fixture/v1/models", {
      headers: { "x-bridge-host": "fixture", authorization: "Bearer friend" },
    }),
  );
  await drain();
  await finishNext(models);
  uploads[0].error(new Error("fixture upload stopped"));
  expect((await pending[0]).status).toBe(408);
  expect((host as any).reading).toBe(3);
  const replacement = host.fetch(req());
  await drain();
  await finishNext(replacement);
  expect((host as any).reading).toBe(3);
  await vi.advanceTimersByTimeAsync(30000);
  for (const result of await Promise.all(pending.slice(1)))
    expect(result.status).toBe(408);
  expect((host as any).reading).toBe(0);
  expect(ws.close).not.toHaveBeenCalled();
});

it("releases the full reservation after oversize refusal and after a successful read", async () => {
  const oversized = await host.fetch(req(new Uint8Array(4 * 1024 * 1024 + 1)));
  expect(oversized.status).toBe(413);
  expect((host as any).reading).toBe(0);
  const next = host.fetch(req());
  await drain();
  expect((host as any).reading).toBe(0);
  await finishNext(next);
});

it("refuses missing and unknown keys before rate, body, or queue admission", async () => {
  let cancelled = false;
  const noKey = new Request("https://fixture/v1/chat/completions", {
    method: "POST",
    headers: { "x-bridge-host": "fixture" },
    body: new ReadableStream({
      cancel() {
        cancelled = true;
      },
    }),
    duplex: "half",
  } as RequestInit);
  const wrong = req("{}");
  wrong.headers.set("authorization", "Bearer wrong");
  for (const request of [noKey, wrong]) {
    const response = await host.fetch(request);
    expect(response.status).toBe(401);
    expect((await response.json()).error.code).toBe("invalid_key");
  }
  expect(requestFrames()).toHaveLength(0);
  expect((host as any).reading).toBe(0);
  expect((host as any).queue).toHaveLength(0);
  expect((storage as any).__values.get("rate")).toBeUndefined();
  expect(cancelled).toBe(true);
});

it("replaces the complete key set and compares every stored hash", async () => {
  hashes.add(keyHash("second"));
  host = new Host(
    {
      getWebSockets: () => [ws],
      setWebSocketAutoResponse() {},
      storage,
    } as any,
    { REGISTRY: { get: async () => null } } as any,
  );
  const admitted = host.fetch(req());
  await drain();
  expect(timingChecks).toHaveBeenCalledTimes(2);
  await finishNext(admitted);

  await frame({ type: "keys", hashes: [keyHash("replacement")] });
  expect(sent).toContainEqual({ type: "keys_ready" });
  expect(hashes).toEqual(new Set([keyHash("replacement")]));
  expect((await host.fetch(req())).status).toBe(401);
  const replacement = req();
  replacement.headers.set("authorization", "Bearer replacement");
  const next = host.fetch(replacement);
  await drain();
  await finishNext(next);
});

it("loads the persisted key set when the object wakes without a new socket", async () => {
  await frame({ type: "keys", hashes: [keyHash("persisted")] });
  host = new Host(
    {
      getWebSockets: () => [ws],
      setWebSocketAutoResponse() {},
      storage,
    } as any,
    { REGISTRY: { get: async () => null } } as any,
  );
  const request = req();
  request.headers.set("authorization", "Bearer persisted");
  const pending = host.fetch(request);
  await drain();
  await finishNext(pending);
});

it.each([[undefined, 1], [0, 1], [-2, 1], [4, 4], [48, 48], [100, 64]])(
  "negotiates %s slots as %s and restores them on hibernation",
  async (advertised, accepted) => {
    await frame({ type: "keys", hashes: [keyHash("friend")], slots: advertised });
    expect(ws.deserializeAttachment()).toEqual({ host: "fixture", slots: accepted });
    expect(sent.at(-1)).toEqual(advertised === undefined
      ? { type: "keys_ready" } : { type: "keys_ready", slots: accepted });
    host = new Host({ getWebSockets: () => [ws], storage, setWebSocketAutoResponse() {} } as any,
      { REGISTRY: { get: async () => null } } as any);
    expect((host as any).slots).toBe(accepted);
    host.webSocketClose(ws);
    expect((host as any).slots).toBeUndefined();
  },
);
it.each([1.5, "4", null, 1e100])("refuses invalid slot announcement %s", async (slots) => {
  await frame({ type: "keys", slots });
  expect(ws.close).toHaveBeenCalledOnce();
});
it("keeps negotiated capacity unchanged by key reloads", async () => {
  await frame({ type: "keys", slots: 3, hashes: [keyHash("friend")] });
  await frame({ type: "keys", slots: 3, hashes: [keyHash("friend")] });
  expect(ws.close).not.toHaveBeenCalled();
  await frame({ type: "keys", slots: 4 });
  expect(ws.close).toHaveBeenCalledOnce();
});
it("admits N plus four waiting per host and settles interleaved responses by ID", async () => {
  await frame({ type: "keys", slots: 3, hashes: [keyHash("friend")] });
  const pending: Promise<Response>[] = [];
  for (let i = 0; i < 7; i++) {
    pending.push(host.fetch(req()));
    await drain();
  }
  expect(requestFrames()).toHaveLength(3);
  expect((host as any).queue).toHaveLength(4);
  expect((await host.fetch(req())).status).toBe(429);
  const ids = requestFrames().map((f) => f.id);
  const reads = await Promise.all(ids.map(async (id, i) => {
    const response = await beginResponse(id, pending[i]);
    return { text: response.text() };
  }));
  for (const i of [2, 0, 1]) await frame({ type: "data", id: ids[i], data: btoa(`job-${i}`) });
  for (const i of [1, 2, 0]) await frame({ type: "end", id: ids[i] });
  expect(await Promise.all(reads.map((r) => r.text))).toEqual(["job-0", "job-1", "job-2"]);
  expect(requestFrames()).toHaveLength(6);
  expect((host as any).active.size).toBe(3);
  expect((host as any).queue).toHaveLength(1);
  expect(ws.close).not.toHaveBeenCalled();
});
it("one stalled response and its cancellation drain do not hold other slots", async () => {
  await frame({ type: "keys", slots: 2, hashes: [keyHash("friend")] });
  const first = host.fetch(req());
  await drain();
  const id = requestFrames()[0].id;
  const response = await beginResponse(id, first);
  const blocked = frame({ type: "data", id, data: btoa("unread") });
  const second = host.fetch(req());
  await drain();
  const third = host.fetch(req());
  await drain();
  expect(requestFrames()).toHaveLength(2);
  await response.body!.cancel();
  await blocked;
  expect(sent).toContainEqual({ type: "cancel", id });
  await finishNext(second);
  expect(requestFrames()).toHaveLength(3);
  await finishNext(third);
  expect((host as any).active.size).toBe(1);
  await frame({ type: "ready", id });
  expect((host as any).active.size).toBe(0);
  expect(ws.close).not.toHaveBeenCalled();
});

it("uses all 48 workstation slots with only four additional waiting jobs", async () => {
  await frame({ type: "keys", slots: 48, hashes: [keyHash("friend")] });
  for (let i = 0; i < 52; i++) {
    void host.fetch(req(null));
    await drain();
  }
  expect(requestFrames()).toHaveLength(48);
  expect((host as any).queue).toHaveLength(4);
  expect((await host.fetch(req(null))).status).toBe(429);
});
