import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { createHash, randomUUID, timingSafeEqual } from "node:crypto";
import worker, { Host } from "../src/index";

let host: Host, ws: any, sent: any[], values: Map<string, unknown>;
let disabled: boolean;
const drain = () => vi.advanceTimersByTimeAsync(0);
beforeEach(() => {
  vi.useFakeTimers();
  vi.stubGlobal("crypto", {
    subtle: {
      digest: async (_: string, value: Uint8Array) =>
        Uint8Array.from(createHash("sha256").update(value).digest()).buffer,
      timingSafeEqual,
    },
    randomUUID,
  });
  vi.stubGlobal("WebSocketRequestResponsePair", class {});
  values = new Map();
  disabled = false;
  sent = [];
  ws = { send: (s: string) => sent.push(JSON.parse(s)), close: vi.fn() };
  const storage = {
    sql: { exec: () => [{ hash: createHash("sha256").update("friend").digest("hex") }] },
    transaction: async (fn: any) => fn({
      get: async (key: string) => values.get(key),
      put: async (key: string, value: unknown) => values.set(key, value),
    }),
  };
  host = new Host({
    getWebSockets: () => [ws], storage, setWebSocketAutoResponse() {},
  } as any, { REGISTRY: { get: async () => disabled ? "yes" : null } } as any);
});
afterEach(() => {
  // Some refusal fixtures set only the queue's length.
  (host as any).queue = [];
  host.webSocketClose(ws);
  vi.clearAllTimers();
  vi.useRealTimers();
  vi.unstubAllGlobals();
});
function request(body: ReadableStream, auth = "Bearer friend") {
  return new Request("https://fixture/v1/chat/completions", {
    method: "POST", headers: { authorization: auth }, body, duplex: "half",
  } as RequestInit);
}
function upload() {
  let finish!: () => void;
  const cancelled = new Promise<void>((resolve) => { finish = resolve; });
  const cancel = vi.fn(() => cancelled);
  const pull = vi.fn();
  const body = new ReadableStream({ pull, cancel }, { highWaterMark: 0 });
  return { body, cancel, pull, finish };
}

it.each([
  ["invalid key", 401], ["rate window", 429], ["offline", 503],
  ["queue full", 429], ["read budget", 429], ["disabled", 403],
] as const)("settles unread input before the %s response", async (reason, status) => {
  if (reason === "rate window") values.set("rate", { minute: Math.floor(Date.now() / 60000), n: 60 });
  if (reason === "offline") host.webSocketClose(ws);
  if (reason === "queue full") (host as any).queue.length = 4;
  if (reason === "read budget") (host as any).reading = 4;
  if (reason === "disabled") disabled = true;
  const input = upload();
  let committed = false;
  const response = host.fetch(request(input.body, reason === "invalid key" ? "" : "Bearer friend"))
    .then((r) => { committed = true; return r; });
  await drain();
  expect(input.pull).not.toHaveBeenCalled();
  expect(input.cancel).toHaveBeenCalledTimes(1);
  expect(committed).toBe(false);
  input.finish();
  expect((await response).status).toBe(status);
  expect(input.body.locked).toBe(false);
});

it("joins the timeout's original cancellation before returning 408 and releasing admission", async () => {
  const input = upload();
  let committed = false;
  const response = host.fetch(request(input.body)).then((r) => { committed = true; return r; });
  await drain();
  expect(input.pull).toHaveBeenCalledTimes(1);
  await vi.advanceTimersByTimeAsync(30000);
  expect(input.cancel).toHaveBeenCalledTimes(1);
  expect(committed).toBe(false);
  expect((host as any).reading).toBe(1);
  input.finish();
  expect((await response).status).toBe(408);
  expect(input.body.locked).toBe(false);
  expect((host as any).reading).toBe(0);
  expect(sent).toHaveLength(0);
});

it("releases the reader on input failure before returning a response", async () => {
  let controller!: ReadableStreamDefaultController;
  const body = new ReadableStream({ start(c) { controller = c; } });
  const response = host.fetch(request(body));
  await drain();
  controller.error(new Error("client closed upload"));
  expect((await response).status).toBe(408);
  expect(body.locked).toBe(false);
  expect((host as any).reading).toBe(0);
  expect(sent).toHaveLength(0);
});

it("settles unread input on an unrouted edge request too", async () => {
  const input = upload();
  let committed = false;
  const req = new Request("https://fixture/not-a-route", {
    method: "POST", body: input.body, duplex: "half",
  } as RequestInit);
  const response = worker.fetch(req, {} as any).then((r) => { committed = true; return r; });
  await drain();
  expect(input.cancel).toHaveBeenCalledTimes(1);
  expect(committed).toBe(false);
  input.finish();
  expect((await response).status).toBe(404);
});

it("a failed cancellation still settles before the original refusal", async () => {
  const body = new ReadableStream({ cancel: async () => { throw new Error("input already gone"); } });
  const result = await host.fetch(request(body, ""));
  expect(result.status).toBe(401);
  expect(body.locked).toBe(false);
});

it("socket teardown after response headers never touches the admitted request body", async () => {
  const body = new ReadableStream({ start(c) { c.enqueue(new TextEncoder().encode("{}")); c.close(); } });
  const response = host.fetch(request(body));
  await drain();
  const id = sent.find((f) => f.type === "request").id;
  await host.webSocketMessage(ws, JSON.stringify({ type: "response", id, status: 200 }));
  const result = await response;
  const cancel = vi.spyOn(body, "cancel");
  const consume = result.text().catch(() => "disconnected");
  host.webSocketClose(ws);
  expect(await consume).toBe("disconnected");
  expect(cancel).not.toHaveBeenCalled();
  expect(body.locked).toBe(false);
});
