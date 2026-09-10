import { expect, it, vi } from "vitest";
import worker from "../src/index";

function request(body: ReadableStream) {
  return new Request("https://fixture/h/test/v1/chat/completions?x=1", {
    method: "POST", headers: { authorization: "Bearer friend" }, body, duplex: "half",
  } as RequestInit);
}
function env(fetch: (req: Request) => Promise<Response>) {
  return { HOSTS: { idFromName: (id: string) => id, get: () => ({ fetch }) } } as any;
}
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));

it.each([401, 429, 503])("settles the client read before returning a fast object %s", async (status) => {
  let finish!: () => void;
  const cancelling = new Promise<void>((resolve) => { finish = resolve; });
  const cancel = vi.fn(() => cancelling);
  const source = new ReadableStream({ cancel }, { highWaterMark: 0 });
  const req = request(source);
  let objectRead!: Promise<ReadableStreamReadResult<Uint8Array>>;
  let committed = false;
  const result = worker.fetch(req, env(async (forwarded) => {
    expect(forwarded.url).toBe("https://fixture/v1/chat/completions?x=1");
    expect(forwarded.headers.get("x-bridge-host")).toBe("test");
    objectRead = forwarded.body!.getReader().read();
    return new Response("refused", { status });
  })).then((r) => { committed = true; return r; });
  await tick();
  expect(cancel).toHaveBeenCalledOnce();
  expect(committed).toBe(false);
  finish();
  expect((await result).status).toBe(status);
  expect((await objectRead).done).toBe(true);
  expect(source.locked).toBe(false);
});

it("joins input cancellation even when the object fetch rejects", async () => {
  let finish!: () => void;
  const cancelling = new Promise<void>((resolve) => { finish = resolve; });
  const cancel = vi.fn(() => cancelling);
  const source = new ReadableStream({ cancel }, { highWaterMark: 0 });
  let settled = false;
  const outcome = worker.fetch(request(source), env(async () => { throw new Error("object unavailable"); }))
    .catch((e) => { settled = true; return e; });
  await tick();
  expect(cancel).toHaveBeenCalledOnce();
  expect(settled).toBe(false);
  finish();
  expect((await outcome).message).toBe("object unavailable");
  expect(source.locked).toBe(false);
});

it("forwards each chunk on demand and preserves the object's response", async () => {
  const chunks = ["first", "second"];
  const source = new ReadableStream({ pull(c) {
    const chunk = chunks.shift();
    if (chunk) c.enqueue(new TextEncoder().encode(chunk)); else c.close();
  } }, { highWaterMark: 0 });
  const response = new Response("streamed result", { status: 202 });
  const req = request(source);
  const returned = await worker.fetch(req, env(async (forwarded) => {
    expect(forwarded.headers.get("authorization")).toBe("Bearer friend");
    expect(await forwarded.text()).toBe("firstsecond");
    return response;
  }));
  expect(returned).toBe(response);
  expect(await returned.text()).toBe("streamed result");
  expect(source.locked).toBe(false);
});

it("releases input ownership after a client-stream failure", async () => {
  const source = new ReadableStream({ pull(c) { c.error(new Error("client stream closed")); } });
  const result = await worker.fetch(request(source), env(async (forwarded) => {
    await expect(forwarded.text()).rejects.toThrow("client stream closed");
    return new Response(null, { status: 499 });
  }));
  expect(result.status).toBe(499);
  expect(source.locked).toBe(false);
});

it("does not read ahead of the object or accumulate an edge body buffer", async () => {
  const pull = vi.fn((c: ReadableStreamDefaultController<Uint8Array>) => c.enqueue(new Uint8Array([1])));
  const cancel = vi.fn();
  const source = new ReadableStream({ pull, cancel }, { highWaterMark: 0 });
  const result = await worker.fetch(request(source), env(async (forwarded) => {
    await tick();
    expect(pull).not.toHaveBeenCalled();
    expect((await forwarded.body!.getReader().read()).value).toEqual(new Uint8Array([1]));
    await tick();
    expect(pull).toHaveBeenCalledOnce();
    return new Response(null, { status: 429 });
  }));
  expect(result.status).toBe(429);
  expect(cancel).toHaveBeenCalledOnce();
  expect(source.locked).toBe(false);
});

it("a failed source cancellation does not replace the object's response", async () => {
  const source = new ReadableStream({ cancel: async () => { throw new Error("client already gone"); } });
  const result = await worker.fetch(request(source), env(async () => new Response(null, { status: 401 })));
  expect(result.status).toBe(401);
  expect(source.locked).toBe(false);
});

it("joins the same cancellation when the object cancels its forwarded reader", async () => {
  let finish!: () => void;
  const cancelled = new Promise<void>((resolve) => { finish = resolve; });
  const cancel = vi.fn(() => cancelled);
  const source = new ReadableStream({ cancel }, { highWaterMark: 0 });
  let committed = false;
  const response = worker.fetch(request(source), env(async (forwarded) => {
    const reader = forwarded.body!.getReader();
    const pending = reader.read();
    await reader.cancel();
    expect((await pending).done).toBe(true);
    return new Response(null, { status: 429 });
  })).then((r) => { committed = true; return r; });
  await tick();
  expect(cancel).toHaveBeenCalledOnce();
  expect(committed).toBe(false);
  finish();
  expect((await response).status).toBe(429);
  expect(cancel).toHaveBeenCalledOnce();
  expect(source.locked).toBe(false);
});
