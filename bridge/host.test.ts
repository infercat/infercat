import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { Host } from "./src/index";

let host: Host, sent: any[], ws: any;
const drain = () => vi.advanceTimersByTimeAsync(0);
beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("WebSocketRequestResponsePair", class {});
  sent = [];
  ws = { send: (s: string) => sent.push(JSON.parse(s)), close: vi.fn() };
  const values = new Map();
  const storage = {
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
    headers: { "x-bridge-host": "fixture" },
    body,
    duplex: "half",
    signal,
  } as RequestInit);
const frame = (f: object) => host.webSocketMessage(ws, JSON.stringify(f));
const requestFrames = () => sent.filter((f) => f.type === "request");
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
  expect((host as any).readBytes).toBe(16 * 1024 * 1024);
  expect(requestFrames()).toHaveLength(0);
  const excess = await host.fetch(req());
  expect(excess.status).toBe(429);
  expect((await excess.json()).error.code).toBe("host_busy");
  expect((host as any).readBytes).toBe(16 * 1024 * 1024);
  // No body means no upload buffer: a models request can still use the host slot.
  const models = host.fetch(
    new Request("https://fixture/v1/models", {
      headers: { "x-bridge-host": "fixture" },
    }),
  );
  await drain();
  await finishNext(models);
  uploads[0].error(new Error("fixture upload stopped"));
  expect((await pending[0]).status).toBe(408);
  expect((host as any).reading).toBe(3);
  expect((host as any).readBytes).toBe(12 * 1024 * 1024);
  const replacement = host.fetch(req());
  await drain();
  await finishNext(replacement);
  expect((host as any).reading).toBe(3);
  await vi.advanceTimersByTimeAsync(30000);
  for (const result of await Promise.all(pending.slice(1)))
    expect(result.status).toBe(408);
  expect((host as any).reading).toBe(0);
  expect((host as any).readBytes).toBe(0);
  expect(ws.close).not.toHaveBeenCalled();
});

it("releases the full reservation after oversize refusal and after a successful read", async () => {
  const oversized = await host.fetch(req(new Uint8Array(4 * 1024 * 1024 + 1)));
  expect(oversized.status).toBe(413);
  expect((host as any).reading).toBe(0);
  expect((host as any).readBytes).toBe(0);
  const next = host.fetch(req());
  await drain();
  expect((host as any).reading).toBe(0);
  expect((host as any).readBytes).toBe(0);
  await finishNext(next);
});
