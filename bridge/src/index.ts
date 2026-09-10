import { DurableObject } from "cloudflare:workers";

type Env = { HOSTS: DurableObjectNamespace<Host>; REGISTRY: KVNamespace };
type Frame = {
  type: string;
  id?: string;
  status?: number;
  headers?: Record<string, string>;
  data?: string;
  hashes?: string[];
  slots?: number;
};
type Job = {
  id: string;
  method: string;
  path: string;
  headers: Record<string, string>;
  signal: AbortSignal;
  resolve: (r: Response) => void;
  writer?: WritableStreamDefaultWriter<Uint8Array>;
  abortResponse?: (reason: Error) => void;
  timer: ReturnType<typeof setTimeout>;
  body: Uint8Array;
  cancelled?: boolean;
  onAbort: () => void;
};
const CHUNK = 512 * 1024,
  MAX_BODY = 4 * 1024 * 1024,
  MAX_FRAME = 1024 * 1024;
const hostID = /^[a-zA-Z0-9_-]{1,64}$/;
const reply = (
  status: number,
  code: string,
  type = "bridge_error",
  message = code,
) => Response.json({ error: { message, type, code } }, { status });
const subset = (headers: Headers, keys: string[]) =>
  Object.fromEntries(
    keys.filter((k) => headers.has(k)).map((k) => [k, headers.get(k)!]),
  );
const hash = async (s: string) =>
  Array.from(
    new Uint8Array(
      await crypto.subtle.digest("SHA-256", new TextEncoder().encode(s)),
    ),
    (b) => b.toString(16).padStart(2, "0"),
  ).join("");
const token = () =>
  Array.from(crypto.getRandomValues(new Uint8Array(32)), (b) =>
    b.toString(16).padStart(2, "0"),
  ).join("");
type TimingSafeSubtle = SubtleCrypto & {
  timingSafeEqual(a: ArrayBufferView, b: ArrayBufferView): boolean;
};
function encode(b: Uint8Array): string {
  let s = "";
  for (let i = 0; i < b.length; i += 8192)
    s += String.fromCharCode(...b.subarray(i, i + 8192));
  return btoa(s);
}
async function bounded(req: Request, limit: number): Promise<Uint8Array> {
  const reader = req.body?.getReader();
  if (!reader) return new Uint8Array();
  const body = new Uint8Array(limit);
  let size = 0,
    timedOut = false;
  // A second cancel() can resolve before the first cancellation finishes.
  let cancellation: Promise<void> | undefined;
  const cancel = () => (cancellation ??= reader.cancel().catch(() => {}));
  const timer = setTimeout(() => {
    timedOut = true;
    void cancel();
  }, 30000);
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (timedOut) throw new Error("body_timeout");
      if (done) break;
      if (size + value.length > limit) throw new Error("body_too_large");
      body.set(value, size);
      size += value.length;
    }
  } finally {
    clearTimeout(timer);
    await cancel();
    reader.releaseLock();
  }
  return body.subarray(0, size);
}
async function discardUnread(req: Request) {
  // Admission owns input until it is consumed or cancellation has settled.
  if (!req.bodyUsed) await req.body?.cancel().catch(() => {});
}
function forwardInput(body: ReadableStream<Uint8Array>) {
  const reader = body.getReader();
  let controller: ReadableStreamDefaultController<Uint8Array>;
  let pending = Promise.resolve(), stopped = false;
  let stopping: Promise<void> | undefined;
  const stop = () => {
    if (!stopping) {
      stopped = true;
      try { controller.close(); } catch {} // The object may already have cancelled it.
      stopping = (async () => {
        await reader.cancel().catch(() => {});
        await pending;
        reader.releaseLock();
      })();
    }
    return stopping;
  };
  const stream = new ReadableStream<Uint8Array>({
    start(c) { controller = c; },
    pull() {
      pending = reader.read().then(({ done, value }) => {
        if (stopped) return;
        if (done) controller.close(); else controller.enqueue(value);
      }).catch((error) => { if (!stopped) controller.error(error); });
      return pending;
    },
    cancel: stop,
  }, { highWaterMark: 0 });
  return { stream, stop };
}
export default {
  async fetch(req: Request, env: Env): Promise<Response> {
    const u = new URL(req.url);
    if (u.pathname === "/register" && req.method === "POST") {
      let code: unknown;
      try {
        code = JSON.parse(
          new TextDecoder().decode(await bounded(req, 1024)),
        ).code;
      } catch {
        return reply(400, "invalid_registration");
      }
      if (typeof code !== "string" || !/^[a-zA-Z0-9_-]{16,128}$/.test(code))
        return reply(400, "invalid_registration");
      const host = await env.REGISTRY.get("reg:" + code);
      if (!host || !hostID.test(host))
        return reply(403, "invalid_registration");
      return env.HOSTS.get(env.HOSTS.idFromName(host)).fetch(
        new Request("https://bridge/register", {
          method: "POST",
          body: JSON.stringify({ code, host }),
        }),
      );
    }
    const m = u.pathname.match(
      /^\/h\/([a-zA-Z0-9_-]{1,64})(\/v1\/.*|\/socket)$/,
    );
    if (
      !m ||
      !["GET", "POST"].includes(req.method) ||
      u.pathname.length + u.search.length > 2048
    ) {
      await discardUnread(req);
      return reply(404, "not_found");
    }
    const internal = new URL(req.url);
    internal.pathname = m[2];
    // Own the native client reader: a fast object refusal must not outlive its input.
    // No whole-body buffer or read-ahead queue; at most one native chunk is in flight.
    const input = req.body ? forwardInput(req.body) : undefined;
    try {
      const forwarded = input ? new Request(internal, {
        method: req.method, headers: req.headers, signal: req.signal,
        redirect: req.redirect, body: input.stream, duplex: "half",
      } as RequestInit) : new Request(internal, req);
      forwarded.headers.set("x-bridge-host", m[1]);
      return await env.HOSTS.get(env.HOSTS.idFromName(m[1])).fetch(forwarded);
    } finally {
      await input?.stop();
    }
  },
} satisfies ExportedHandler<Env>;

export class Host extends DurableObject<Env> {
  private socket?: WebSocket;
  private keyHashes: Uint8Array[] = [];
  private active = new Map<string, Job>();
  private slots?: number;
  private queue: Job[] = [];
  private reading = 0;
  private readBytes = 0;
  constructor(ctx: DurableObjectState, env: Env) {
    super(ctx, env);
    this.socket = ctx.getWebSockets()[0];
    this.slots = this.socket?.deserializeAttachment()?.slots;
    ctx.storage.sql.exec(
      "CREATE TABLE IF NOT EXISTS friend_keys (hash TEXT PRIMARY KEY)",
    );
    this.keyHashes = [
      ...ctx.storage.sql.exec<{ hash: string }>("SELECT hash FROM friend_keys"),
    ].map((row) => new TextEncoder().encode(row.hash));
    ctx.setWebSocketAutoResponse(
      new WebSocketRequestResponsePair('{"type":"ping"}', '{"type":"pong"}'),
    );
  }
  private replaceKeys(hashes: string[]) {
    this.ctx.storage.transactionSync(() => {
      this.ctx.storage.sql.exec("DELETE FROM friend_keys");
      for (const h of new Set(hashes))
        this.ctx.storage.sql.exec(
          "INSERT INTO friend_keys(hash) VALUES (?)",
          h,
        );
    });
    this.keyHashes = [...new Set(hashes)].map((h) =>
      new TextEncoder().encode(h),
    );
  }
  private async admits(req: Request) {
    const match = /^Bearer (\S+)$/.exec(req.headers.get("authorization") || "");
    if (!match) return false;
    const digest = new TextEncoder().encode(await hash(match[1]));
    let allowed = 0;
    for (const known of this.keyHashes)
      allowed |= Number(
        (crypto.subtle as TimingSafeSubtle).timingSafeEqual(digest, known),
      );
    return allowed !== 0;
  }
  private invalidKey(req: Request) {
    const malformed = !/^Bearer (\S+)$/.test(
      req.headers.get("authorization") || "",
    );
    return reply(
      401,
      "invalid_key",
      "authentication_error",
      malformed
        ? "missing or malformed Authorization header; expected: Bearer <invite secret>"
        : "unknown key; check the invite",
    );
  }
  private send(f: object) {
    if (!this.socket) throw new Error("host_offline");
    this.socket.send(JSON.stringify(f));
  }
  async fetch(req: Request): Promise<Response> {
    try {
      return await this.admitRequest(req);
    } finally {
      // Every refusal settles unused input before workerd commits the response.
      await discardUnread(req);
    }
  }
  private async admitRequest(req: Request): Promise<Response> {
    const u = new URL(req.url);
    if (u.pathname === "/register") {
      const { code, host } = (await req.json()) as {
        code: string;
        host: string;
      };
      if (await this.env.REGISTRY.get("disabled:" + host))
        return reply(403, "host_disabled");
      const digest = await hash(code),
        secret = token(),
        secretHash = await hash(secret);
      const accepted = await this.ctx.storage.transaction(async (tx) => {
        if (await tx.get("used:" + digest)) return false;
        await tx.put({ ["used:" + digest]: true, token: secretHash, host });
        return true;
      });
      if (!accepted) return reply(409, "registration_used");
      this.replaceKeys([]);
      this.disconnect();
      return Response.json(
        { host, token: secret },
        { headers: { "cache-control": "no-store" } },
      );
    }
    const host = req.headers.get("x-bridge-host")!;
    if (await this.env.REGISTRY.get("disabled:" + host)) {
      this.disconnect();
      return reply(403, "host_disabled");
    }
    if (u.pathname === "/socket") {
      if (
        req.method !== "GET" ||
        req.headers.get("upgrade")?.toLowerCase() !== "websocket"
      )
        return reply(400, "websocket_required");
      const auth = req.headers.get("authorization") || "";
      if (
        !/^Bearer [a-f0-9]{64}$/.test(auth) ||
        (await hash(auth.slice(7))) !== (await this.ctx.storage.get("token"))
      )
        return reply(401, "unauthorized");
      this.disconnect();
      this.replaceKeys([]);
      const pair = new WebSocketPair();
      this.socket = pair[1];
      this.ctx.acceptWebSocket(pair[1]);
      pair[1].serializeAttachment({ host });
      return new Response(null, { status: 101, webSocket: pair[0] });
    }
    if (!(await this.admits(req))) return this.invalidKey(req);
    // Persistent per-host fixed window: hibernation and reconnect cannot reset admission.
    const allowed = await this.ctx.storage.transaction(async (tx) => {
      const minute = Math.floor(Date.now() / 60000),
        old = await tx.get<{ minute: number; n: number }>("rate");
      const n = old?.minute === minute ? old.n : 0;
      if (n >= 60) return false;
      await tx.put("rate", { minute, n: n + 1 });
      return true;
    });
    if (!allowed) return reply(429, "host_busy");
    if (!this.socket) return reply(503, "host_offline");
    if (this.queue.length >= 4) return reply(429, "host_busy");
    let body: Uint8Array = new Uint8Array();
    if (req.body) {
      // Reserve the full buffer before reading; untrusted Content-Length never sets the budget.
      if (this.reading >= 4 || this.readBytes + MAX_BODY > 16 * 1024 * 1024)
        return reply(429, "host_busy");
      this.reading++;
      this.readBytes += MAX_BODY;
      try {
        body = await bounded(req, MAX_BODY);
      } catch (e) {
        return (e as Error).message === "body_too_large"
          ? reply(413, "body_too_large")
          : reply(408, "body_timeout");
      } finally {
        this.reading--;
        this.readBytes -= MAX_BODY;
      }
    }
    if (!this.socket) return reply(503, "host_offline");
    if (this.queue.length >= 4) return reply(429, "host_busy");
    if (req.signal.aborted) return reply(499, "client_closed");
    return new Promise<Response>((resolve) => {
      const job: Job = {
        id: crypto.randomUUID(),
        method: req.method,
        path: u.pathname + u.search,
        headers: subset(req.headers, ["authorization", "content-type", "accept"]),
        signal: req.signal,
        resolve,
        body,
        onAbort: () => this.fail(job, 499, "client_closed"),
        timer: setTimeout(() => this.fail(job, 504, "bridge_timeout"), 180000),
      };
      req.signal.addEventListener("abort", job.onAbort, { once: true });
      this.queue.push(job);
      this.next();
    });
  }
  private next() {
    while (this.active.size < (this.slots ?? 1) && this.queue.length) {
      const job = this.queue.shift()!;
      this.active.set(job.id, job);
      this.start(job);
    }
  }
  private start(job: Job) {
    try {
      this.send({
        type: "request",
        id: job.id,
        method: job.method,
        path: job.path,
        headers: job.headers,
      });
      for (let i = 0; i < job.body.length; i += CHUNK)
        this.send({
          type: "body",
          id: job.id,
          data: encode(job.body.subarray(i, i + CHUNK)),
        });
      job.body = new Uint8Array();
      this.send({ type: "end", id: job.id });
    } catch {
      this.disconnect();
    }
  }
  async webSocketMessage(ws: WebSocket, message: string | ArrayBuffer) {
    if (ws !== this.socket) return;
    try {
      if (
        typeof message !== "string" ||
        new TextEncoder().encode(message).length > MAX_FRAME
      )
        throw new Error();
      const f = JSON.parse(message) as Frame;
      if (f.type === "keys") {
        const hashes = f.hashes ?? [];
        if (
          !Array.isArray(hashes) ||
          hashes.some((h) => typeof h !== "string" || !/^[a-f0-9]{64}$/.test(h))
        )
          throw new Error();
        if (f.slots !== undefined && !Number.isSafeInteger(f.slots))
          throw new Error();
        const slots = Math.min(64, Math.max(1, f.slots ?? 1));
        if (this.slots !== undefined && this.slots !== slots) throw new Error();
        this.slots = slots;
        ws.serializeAttachment({ ...ws.deserializeAttachment(), slots });
        this.replaceKeys(hashes);
        this.send({ type: "keys_ready", ...(f.slots === undefined ? {} : { slots }) });
        return;
      }
      const job = this.active.get(f.id ?? "");
      if (!job) throw new Error();
      // Drain in-flight response frames until the host confirms the cancelled handler stopped.
      if (job.cancelled) {
        if (f.type === "ready") {
          clearTimeout(job.timer);
          this.active.delete(job.id);
          this.next();
        } else if (!["response", "data", "end", "cancel"].includes(f.type))
          throw new Error();
        return;
      }
      if (f.type === "cancel") {
        this.fail(job, 504, "bridge_timeout");
        return;
      }
      if (f.type === "response") {
        if (
          job.writer ||
          !Number.isInteger(f.status) ||
          f.status! < 200 ||
          f.status! > 599
        )
          throw new Error();
        const stream = new TransformStream<Uint8Array, Uint8Array>({
          start: (controller) => {
            job.abortResponse = (reason) => controller.error(reason);
          },
        });
        job.writer = stream.writable.getWriter();
        void job.writer.closed.catch(() =>
          this.fail(job, 499, "client_closed"),
        );
        const headers = new Headers();
        for (const k of [
          "content-type",
          "cache-control",
          "retry-after",
          "x-request-id",
        ])
          if (f.headers?.[k]) headers.set(k, f.headers[k]);
        headers.set("cache-control", "no-store");
        // Bodyless statuses still drain the host protocol without exposing an HTTP body.
        if ([204, 205, 304].includes(f.status!)) {
          void stream.readable.pipeTo(new WritableStream()).catch(() => {});
          job.resolve(new Response(null, { status: f.status, headers }));
        } else
          job.resolve(
            new Response(stream.readable, { status: f.status, headers }),
          );
      } else if (f.type === "data") {
        if (
          !job.writer ||
          typeof f.data !== "string" ||
          f.data.length > Math.ceil(CHUNK / 3) * 4
        )
          throw new Error();
        const data = Uint8Array.from(atob(f.data), (c) => c.charCodeAt(0));
        if (data.length > CHUNK) throw new Error();
        try {
          await job.writer.write(data);
        } catch {
          this.fail(job, 499, "client_closed");
          return;
        }
        if (this.active.get(job.id) === job && !job.cancelled)
          this.send({ type: "ack", id: job.id });
      } else if (f.type === "end") {
        if (!job.writer) throw new Error();
        try {
          await job.writer.close();
        } catch {
          this.fail(job, 499, "client_closed");
          return;
        }
        if (ws !== this.socket || this.active.get(job.id) !== job || job.cancelled) return;
        job.signal.removeEventListener("abort", job.onAbort);
        clearTimeout(job.timer);
        this.active.delete(job.id);
        this.send({ type: "ready", id: job.id });
        this.next();
      } else throw new Error();
    } catch {
      if (ws === this.socket) this.disconnect();
    }
  }
  private fail(job: Job, status: number, code: string) {
    if (job.cancelled) return;
    job.cancelled = true;
    clearTimeout(job.timer);
    job.signal.removeEventListener("abort", job.onAbort);
    job.resolve(reply(status, code));
    job.abortResponse?.(new Error(code));
    void job.writer?.abort(new Error(code)).catch(() => {});
    this.queue = this.queue.filter((j) => j !== job);
    if (this.active.get(job.id) === job) {
      try {
        this.send({ type: "cancel", id: job.id });
      } catch {
        this.disconnect();
        return;
      }
      // Failing to acknowledge cancellation is a host protocol failure.
      job.timer = setTimeout(() => this.disconnect(), 30000);
    } else this.next();
  }
  private disconnect() {
    const ws = this.socket;
    this.socket = undefined;
    this.slots = undefined;
    try {
      ws?.close(1011, "bridge disconnected");
    } catch {}
    for (const job of [...this.active.values(), ...this.queue]) {
      job.cancelled = true;
      clearTimeout(job.timer);
      job.signal.removeEventListener("abort", job.onAbort);
      job.resolve(reply(502, "bridge_disconnected"));
      job.abortResponse?.(new Error("bridge disconnected"));
      void job.writer?.abort(new Error("bridge disconnected")).catch(() => {});
    }
    this.active.clear();
    this.queue = [];
  }
  webSocketClose(ws: WebSocket) {
    if (ws === this.socket) this.disconnect();
  }
  webSocketError(ws: WebSocket) {
    if (ws === this.socket) this.disconnect();
  }
}
