// Gateway wire contract, versioned with @infercat/client. Both the web app and client use this
// type-only module; localized parsing and presentation belong to each caller.
export interface Limits {
  rpm: number;
  tpm: number;
  max_concurrent: number;
  max_output_tokens: number;
  max_context: number;
  daily_tokens: number;
  daily_audio_seconds?: number;
  daily_speech_chars?: number;
  daily_images?: number;
  max_queued_images?: number;
  models?: string[];
}

export interface Me {
  /** Agent runs are available only when the host explicitly reports this capability. */
  agent?: boolean;
  key: { id: string; name: string; status: 'active' | 'paused' | 'revoked' };
  limits: Limits;
  usage: { today_images?: number; rpm_used: number; tpm_used: number; today_tokens: number; in_flight: number };
  host: {
    name: string;
    upstream: { kind: string; healthy: boolean; model_context: number };
    models: string[];
    /** Per-model capability; null means the engine did not report it. */
    vision?: Record<string, boolean | null>;
    images?: { model: string; retention_days: number; queue_cap: number; queued: number };
    audio?: { transcriptions: string | null; speech: string | null };
    relay: { region: string };
    /** The host runs with --log-prompts. Absent on a gateway older than ticket 006: absent = false. */
    log_prompts?: boolean;
  };
}

export type GatewayErrorCode =
  | 'invalid_key' | 'key_paused' | 'key_revoked' | 'model_not_allowed'
  | 'body_too_large' | 'context_too_long' | 'rate_limited' | 'concurrency_limited'
  | 'budget_exhausted' | 'queue_timeout' | 'upstream_down' | 'upstream_error'
  | 'image_queue_full' | 'image_budget_exhausted'
  | 'audio_budget_exhausted' | 'speech_budget_exhausted' | 'images_not_supported'
  | 'invalid_request' | 'not_found' | 'client_closed';

/** JSON error payload; retry_after appears in streamed errors after headers are sent. */
export type GatewayErrorPayload = {
  message: string;
  code: GatewayErrorCode | (string & Record<never, never>);
  type: string;
  limit?: number;
  in_flight?: number;
  retry_after?: number;
};
export type GatewayErrorBody = { error: GatewayErrorPayload };
