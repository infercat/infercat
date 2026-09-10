/** One image per non-empty paragraph; lines inside a paragraph stay together. */
export function imagePrompts(text: string): string[] { return text.trim().split(/\r?\n[\t ]*\r?\n(?:[\t ]*\r?\n)*/).map((p) => p.trim()).filter(Boolean); }

import { call, type RunRecord } from './api';
import type { Transport } from './transport';
import type { Conversation, RunItem } from './storage';
export interface ImageJob extends RunRecord {
  kind: 'image'; position?: number; started?: string;
  input: { prompt: string; conversation?: string; client_request_id?: string };
  batch: { id: string; index: number; count: number };
  output?: { url: string; mime: string; w: number; h: number; bytes: number; expiresAt: string; gone?: boolean };
}
export async function imageJobs(t: Transport, secret: string, signal: AbortSignal, input?: { prompts: string[]; conversation: string; client_request_id: string }): Promise<ImageJob[]> {
  const response = await call(t, secret, '/v1/images/jobs', { signal, ...(input ? { method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(input) } : {}) });
  const { jobs } = await response.json() as { jobs: ImageJob[] };
  if (!Array.isArray(jobs) || jobs.some((j) => !j.id || j.kind !== 'image' || !j.batch || !j.input)) throw new Error('Invalid image jobs response');
  if (input && (jobs.length !== input.prompts.length || new Set(jobs.map((j) => j.batch.id)).size !== 1)) throw new Error('Incomplete image batch response');
  return jobs;
}
export function imageElapsed(job: ImageJob, now = Date.now()): number { return Math.max(0, ((['done','failed','cancelled'].includes(job.state) ? Date.parse(job.updated) : now) - Date.parse(job.started ?? job.created)) / 1000); }
export function imageGone(job: ImageJob, now = Date.now()): boolean { return Boolean(job.output && (job.output.gone || Date.parse(job.output.expiresAt) <= now)); }
export function imageItem(job: ImageJob): RunItem { return { id: job.id, kind: 'run', runKind: 'image', role: 'assistant', content: '', job, keyId: job.key_id, clientRequestId: job.input.client_request_id }; }
/** Correlation locates the asking, never chooses a winning batch or suppresses a duplicate. */
export function mergeImageJobs(convs: Conversation[], jobs: ImageJob[], keyId: string): Conversation[] {
  const seen = new Set<string>();
  const owners = new Map<string, Set<string>>();
  for (const c of convs) for (const m of c.messages) if (m.kind === 'run' && m.runKind === 'image' && m.keyId === keyId && m.clientRequestId) { const ids = owners.get(m.clientRequestId) ?? new Set<string>(); ids.add(c.id); owners.set(m.clientRequestId, ids); }
  const next = convs.map((c) => ({ ...c, messages: c.messages.flatMap((m) => {
    if (m.kind !== 'run' || m.runKind !== 'image' || m.keyId !== keyId) return [m];
    const matches = jobs.filter((j) => !seen.has(j.id) && (j.id === m.job?.id || (j.input.client_request_id && j.input.client_request_id === m.clientRequestId && owners.get(m.clientRequestId)?.size === 1))).sort((a,b) => a.batch.id.localeCompare(b.batch.id) || a.batch.index - b.batch.index);
    matches.forEach((j) => seen.add(j.id));
    if (matches.length) return matches.map(imageItem);
    return m.job && seen.has(m.job.id) ? [] : [m];
  }) }));
  const recovered = new Map<string, ImageJob[]>();
  for (const j of jobs) if (!seen.has(j.id)) { const group = recovered.get(j.batch.id) ?? []; group.push(j); recovered.set(j.batch.id, group); }
  for (const [batch, group] of recovered) {
    group.sort((a,b) => a.batch.index - b.batch.index); const j = group[0]!;
    next.push({ id: `images:${batch}`, title: j.input.prompt.slice(0,40), createdAt: Date.parse(j.created), updatedAt: Date.parse(j.updated), messages: [{ id: `prompt:${batch}`, role: 'user', content: group.map((j) => j.input.prompt).join('\n\n') }, ...group.map(imageItem)] });
  }
  return next;
}
/** Never follow the host-supplied URL; the authenticated route is fixed and key scoped. */
export async function imageArtifact(t: Transport, secret: string, id: string, signal: AbortSignal, download = false): Promise<Blob> {
  const response = await call(t, secret, `/v1/images/outputs/${encodeURIComponent(id)}${download ? '?download=1' : ''}`, { signal });
  const mime = response.headers.get('content-type')?.split(';')[0];
  if (!['image/png','image/jpeg'].includes(mime ?? '') || !response.body) { await response.body?.cancel(); throw new Error('Invalid image output'); }
  const reader = response.body.getReader(), chunks: Uint8Array<ArrayBuffer>[] = []; let size = 0;
  try { for (;;) { const { value, done } = await reader.read(); if (done) break; size += value.byteLength; if (size > 8 * 1024 * 1024) throw new Error('Image exceeds 8 MiB'); chunks.push(new Uint8Array(value)); } }
  finally { await reader.cancel().catch(() => {}); reader.releaseLock(); }
  return new Blob(chunks, { type: mime });
}

export function imageLimit(value: number | undefined, fallback: number): number { return value === undefined || value === 0 ? fallback : value; }
