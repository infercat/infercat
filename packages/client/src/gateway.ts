import type { GatewayErrorCode, GatewayErrorBody } from './contract';
export type { Limits, Me, GatewayErrorCode, GatewayErrorBody, GatewayErrorPayload } from './contract';

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
    const body: Partial<GatewayErrorBody> = await response.json();
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
