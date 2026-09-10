import type { FakeRequest, FakeResponse } from './fake-backend';
import { runs, updateRun } from './fake-runs';
import type { ImageJob } from '../src/imageJobs';
export const imageCalls: { method: string; path: string; body: unknown }[] = [];
export const imageControl = { cap: 8, daily: 20, budget: false, list429: false, refuse: false, lose: false, png: '' };
const json = (body: unknown, status = 200): FakeResponse => ({ status, headers: { 'content-type': 'application/json' }, body: JSON.stringify(body) });
export const fakeImageJobs = () => [...runs.values()].filter((r) => r.kind === 'image') as ImageJob[];
export function handleImageJobs(req: FakeRequest): FakeResponse | undefined {
  Object.assign(globalThis, { __fakeImages: { calls: imageCalls, control: imageControl, list: fakeImageJobs, update: (id: string, patch: Partial<ImageJob>) => updateRun(id, patch) } });
  const path = req.path.split('?')[0]!;
  if (!path.startsWith('/v1/images/') && !(path.startsWith('/v1/runs/') && runs.get(path.slice(9))?.kind === 'image' && req.method === 'DELETE')) return;
  imageCalls.push({ path: req.path, method: req.method, body: req.body ? JSON.parse(req.body) : null });
  if (path === '/v1/images/jobs') {
    if (req.method === 'GET' && imageControl.list429) { imageControl.list429 = false; const response = json({ error: { code: 'rate_limited', message: 'Wait before reading again.' } },429); response.headers['retry-after'] = '2'; return response; }
    if (req.method === 'GET') return json({ jobs: fakeImageJobs().sort((a,b) => b.created.localeCompare(a.created)) });
    const input = JSON.parse(req.body) as { prompts: string[]; conversation: string; client_request_id: string };
    const queued = fakeImageJobs().filter((j) => j.state === 'queued').length;
    if (imageControl.budget) return json({ error: { code: 'image_budget_exhausted', message: 'Image budget used up.' } }, 429);
    if (imageControl.refuse || (imageControl.cap >= 0 && queued + input.prompts.length > (imageControl.cap || 8))) return json({ error: { code: 'image_queue_full', limit: imageControl.cap, in_flight: queued, message: 'queue full' } }, 429);
    const batch = crypto.randomUUID(), created = new Date().toISOString();
    const jobs = input.prompts.map((prompt, index): ImageJob => ({ id: `image-${runs.size + index + 1}`, key_id: 'k_7f3a2b', kind: 'image', state: index ? 'queued' : 'running', position: index, created, updated: created, started: index ? undefined : created, expires: new Date(Date.now()+604800000).toISOString(), cancel_requested: false, input: { prompt, conversation: input.conversation, client_request_id: input.client_request_id }, batch: { id: batch, index, count: input.prompts.length }, attempts: [] }));
    for (const job of jobs) { runs.set(job.id, job); updateRun(job.id, {}); }
    if (imageControl.lose) return json({ error: { code: 'upstream_error', message: 'response lost after commit' } }, 502);
    return json({ jobs: jobs.map((job) => { const run = { ...job }; delete run.position; return run; }) }, 202);
  }
  const id = decodeURIComponent(path.split('/').at(-1)!); const job = runs.get(id) as ImageJob | undefined;
  if (!job) return json({ error: { code: 'not_found' } },404);
  if (path.startsWith('/v1/runs/')) return json(updateRun(id, { state: job.state === 'queued' ? 'cancelled' : job.state, cancel_requested: true }));
  if (req.method === 'DELETE') { if (job.output) updateRun(id, { output: { ...job.output, gone: true } }); return json({ discarded: true }); }
  if (!job.output || job.output.gone) return json({ error: { code: 'not_found' } },404);
  const data = imageControl.png || 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+a2ioAAAAASUVORK5CYII=';
  return { status: 200, headers: { 'content-type': 'image/png', ...(req.path.includes('download=1') ? { 'content-disposition': 'attachment; filename="image.png"' } : {}) }, bytes: Uint8Array.from(atob(data), (c) => c.charCodeAt(0)) };
}
