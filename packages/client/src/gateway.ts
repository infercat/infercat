export interface Limits {
  rpm: number;
  tpm: number;
  max_concurrent: number;
  max_output_tokens: number;
  max_context: number;
  daily_tokens: number;
  models?: string[];
}

export interface Me {
  key: { id: string; name: string; status: 'active' | 'paused' | 'revoked' };
  limits: Limits;
  usage: { rpm_used: number; tpm_used: number; today_tokens: number; in_flight: number };
  host: {
    name: string;
    upstream: { kind: string; healthy: boolean; model_context: number };
    models: string[];
    vision: Record<string, boolean | null>;
    relay: { region: string };
    /** The host runs with --log-prompts. Absent on a gateway older than ticket 006: absent = false. */
    log_prompts?: boolean;
  };
}

export type GatewayErrorCode =
  | 'invalid_key' | 'key_paused' | 'key_revoked' | 'model_not_allowed'
  | 'body_too_large' | 'context_too_long' | 'rate_limited' | 'concurrency_limited'
  | 'budget_exhausted' | 'queue_timeout' | 'upstream_down' | 'upstream_error'
  | 'invalid_request' | 'not_found' | 'client_closed';

/** Unknown future gateway codes remain available unchanged in `code`. */
export class GatewayError extends Error {
  readonly name = 'GatewayError';
  constructor(
    readonly status: number,
    readonly code: GatewayErrorCode | (string & Record<never, never>),
    readonly type: string,
    message: string,
    readonly retryAfterS?: number,
    readonly limit?: number,
    readonly inFlight?: number,
  ) { super(message); }
}

export async function gatewayError(response: Response): Promise<GatewayError> {
  let error: Record<string, unknown> = {};
  try {
    const body = await response.json();
    if (body?.error && typeof body.error === 'object') error = body.error;
  } catch { /* An HTTP error remains useful even when its body is not JSON. */ }
  const text = (key: string) => typeof error[key] === 'string' ? error[key] : '';
  const number = (key: string) => typeof error[key] === 'number' ? error[key] : undefined;
  const retry = Number(response.headers.get('retry-after'));
  return new GatewayError(response.status, text('code'), text('type'),
    text('message') || `Host returned HTTP ${response.status}`,
    Number.isFinite(retry) && retry > 0 ? Math.ceil(retry) : undefined,
    number('limit'), number('in_flight'));
}
