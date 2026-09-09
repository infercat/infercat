export interface Limits { rpm: number; tpm: number; max_concurrent: number; max_output_tokens: number; max_context: number; daily_tokens: number; models?: string[]; daily_audio_seconds?: number; daily_speech_chars?: number }
export interface Key { id: string; name: string; status: 'active' | 'paused' | 'revoked'; limits: Limits; created_at: string; last_seen: string; today_tokens: number }
export interface LiveKey { id: string; in_flight: number; rpm_used: number; tpm_used: number; today_tokens: number; last_seen: string }
export interface Stats {
 key_id?: string; requests: number; model_calls: number; app_polls: number; errors: number;
 errors_by_code?: Record<string, number>; model_calls_by_model?: Record<string, number>;
 prompt_tokens: number; completion_tokens: number; ttft_median_ms: number; ttft_p95_ms: number;
 total_median_ms: number; total_p95_ms: number; last_call?: string;
}
export interface Report { total: Stats; keys: Stats[] | null; malformed_lines: number; daily?: { date: string; total: Stats; keys: Stats[] }[] }
export interface Engine { kind: string; url: string; health: { ok: boolean; since: string; err: string }; model_context: number; slots: number; models: string[] | null; probed_at: string }
export interface Status {
 name: string; version: string; uptime_s: number; models_pinned?: string[]; keys: LiveKey[];
 tunnel: { region: string; clients: number; rx_bytes: number; tx_bytes: number };
 queue: { in_flight: number; waiting: number };
 engine: { tokens_per_s_1m: number; metrics: boolean; busy: number; waiting: number; memory_bytes: number; kv_cache_pct: number; slots_peak_sampled: number };
}
export interface Settings { writes_supported?:boolean; default_web_url?:string; configured_console?:string; console_address?:string; running_slots?:number; saved_at?:Record<string,string>; remote?:{enabled:boolean;since?:string;in_use:boolean}; name: string; web_url: string; configured_web_url: string; slots: number; log_requests: boolean; log_requests_remembered: boolean; log_prompts: boolean; upstream: string; derpmap_url: string; region: string; data_dir: string }
export interface Snapshot { status: Status; keys: Key[]; engine: Engine; settings: Settings; today: Report; week: Report }
export const emptyStats: Stats = { requests: 0, model_calls: 0, app_polls: 0, errors: 0, prompt_tokens: 0, completion_tokens: 0, ttft_median_ms: 0, ttft_p95_ms: 0, total_median_ms: 0, total_p95_ms: 0 };
