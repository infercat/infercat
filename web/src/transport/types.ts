// The wasm bridge's JS API (docs/ARCHITECTURE.md §wasm bridge, built by ticket 001) and the
// Transport seam the rest of the app talks to. These declarations are the contract; the fake in
// web/dev/fake-infercat-tunnel.ts implements the same shapes so everything below can be tested.

export interface Conn {
  /** Resolves null at EOF. No concurrent reads. */
  read(): Promise<Uint8Array | null>;
  write(data: Uint8Array): Promise<void>;
  closeWrite(): Promise<void>;
  close(): void;
}

export interface PingResult {
  rttMs: number;
  /** e.g. "DERP(sfo)" or "203.0.113.7:41641". */
  via: string;
  direct: boolean;
}

export interface Session {
  addr: string;
  /** Persist to keep the same client identity across reloads. */
  privateKeyJSON: string;
  /** Default port 80. Must not redo the handshake. */
  dial(port?: number): Promise<Conn>;
  ping(): Promise<PingResult>;
  close(): void;
}

export interface TunnelConnectOptions {
  addr: string;
  derpMapURL?: string;
  privateKey?: string;
  verbose?: boolean;
  onLog?: (line: string) => void;
}

export interface InfercatTunnel {
  /** Resolves after the first successful ping (handshake up); rejects on a 60 s timeout. */
  connect(opts: TunnelConnectOptions): Promise<Session>;
}

declare global {
  interface Window {
    InfercatTunnel: InfercatTunnel;
  }
}

/** What the app uses to reach the gateway, whichever path it took to get there. */
export interface Transport {
  readonly kind: 'direct' | 'tunnel';
  /** `input` is a gateway path such as "/me". */
  fetch(input: string, init?: RequestInit): Promise<Response>;
  /** null when the path cannot be measured (Direct mode reports its own loopback rtt). */
  ping(): Promise<PingResult | null>;
  close(): void;
}

/** window.InfercatTunnel, reachable from Node too so the connect flow is unit-testable. */
export function tunnelGlobal(): InfercatTunnel | undefined {
  return (globalThis as unknown as Partial<Window>).InfercatTunnel;
}
