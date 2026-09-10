import { GatewayError, getRun, runEvents, type RunEvent, type RunRecord } from './api';
import type { Transport } from './transport';

/** One subscription per key, not one per row. Cursor advances only after snapshots are read:
 * a disconnected read cannot skip the event that told us it changed. Mutations live elsewhere. */
export async function followRuns(options: {
  transport: Transport; secret: string; signal: AbortSignal;
  changed: (runs: RunRecord[], reset: boolean) => void;
  connected: (value: boolean) => void;
  failed: (error: unknown) => void;
  known?: () => string[];
  delay?: (signal: AbortSignal) => Promise<void>;
}): Promise<void> {
  const { transport, secret, signal, changed, connected, failed } = options;
  let cursor = '';
  const snapshots = new Map<string, RunRecord>(), fromRead = new Set<string>();
  try {
    while (!signal.aborted) {
      try {
        for await (const event of runEvents(transport, secret, cursor, signal)) {
          if (signal.aborted) return;
          if (event.cursor === cursor && !event.reset) continue;
          const ids = event.reset ? [...new Set([...(event.runs ?? []).filter((r) => r.kind === 'agent').map((r) => r.id), ...(options.known?.() ?? [])])] : event.run_id ? [event.run_id] : [];
          if (event.reset) { snapshots.clear(); fromRead.clear(); }
          const records: RunRecord[] = [];
          for (const id of ids) {
            try {
              const prior = snapshots.get(id);
              const direct = event.type === 'step' && event.step && prior && event.state === 'running' && !(fromRead.has(id) && Date.parse(event.time) <= Date.parse(prior.updated));
              const record = direct ? applyRunStep(prior!, event) : await getRun(transport, secret, id, signal);
              if (direct) fromRead.delete(id); else fromRead.add(id);
              snapshots.set(id, record); records.push(record);
            }
            catch (error) { if (!(error instanceof GatewayError && error.status === 404)) throw error; }
          }
          if (signal.aborted) return;
          changed(records, event.reset === true);
          cursor = event.cursor;
          connected(true);
        }
      } catch (error) {
        if (signal.aborted) return;
        // A host restart changes the cursor epoch. An empty read requests the authoritative
        // reset snapshot; never interpret an invalid cursor as a reason to submit again.
        if (error instanceof GatewayError && error.status === 400 && cursor) cursor = '';
        else {
          failed(error);
          if (error instanceof GatewayError && [401, 403, 404].includes(error.status)) return;
        }
      }
      connected(false);
      await (options.delay ?? retryDelay)(signal);
    }
  } catch (error) { if (!signal.aborted) failed(error); }
  finally { connected(false); }
}
function retryDelay(signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const done = () => { clearTimeout(timer); signal.removeEventListener('abort', done); resolve(); };
    const timer = setTimeout(done, 2000);
    signal.addEventListener('abort', done, { once: true });
    if (signal.aborted) done();
  });
}

/** Merge a current snapshot into its existing row; run input is not duplicated into localStorage
 * (image bytes already have the bounded attachment store). Completed words can enter chat context. */
export function runItem(record: RunRecord, id = record.id, keyId = record.key_id): import('./storage').RunItem {
  const snapshot: RunRecord = {
    id: record.id, kind: record.kind, state: record.state, created: record.created, updated: record.updated, expires: record.expires,
    cancel_requested: record.cancel_requested, queue_position: record.queue_position, client_request_id: record.client_request_id, key_id: keyId, reason: record.reason, input: null,
    text: record.text, steps: record.steps, outputs: record.outputs, approval: record.approval,
    attempts: (record.attempts ?? []).map((a) => ({ id: a.id, dispatched: a.dispatched, settled: a.settled, accounting_uncertain: a.accounting_uncertain, usage: { prompt_tokens: a.usage.prompt_tokens, completion_tokens: a.usage.completion_tokens } })),
  };
  return { kind: 'run', id, role: 'assistant', content: record.text ?? '', run: snapshot, keyId, clientRequestId: record.client_request_id };
}
export function mergeRunRecords(convs: import('./storage').Conversation[], records: RunRecord[], keyId?: string): import('./storage').Conversation[] {
  const found = new Set<string>(), known = new Set(convs.flatMap((c) => c.messages.flatMap((m) => m.kind === 'run' ? [m.run?.id ?? m.remoteId ?? ''] : [])));
  const owners = new Map<string, Set<string>>();
  for (const c of convs) for (const m of c.messages) if (m.kind === 'run' && m.clientRequestId && m.keyId === keyId) {
    const ids = owners.get(m.clientRequestId) ?? new Set<string>(); ids.add(c.id); owners.set(m.clientRequestId, ids);
  }
  const next = convs.map((c) => {
    let changed = false;
    const messages = c.messages.flatMap<import('./storage').Message>((m) => {
      if (m.kind !== 'run') return [m];
      const exact = records.find((r) => r.id === (m.run?.id ?? m.remoteId));
      const correlated = keyId && m.keyId === keyId && m.clientRequestId && owners.get(m.clientRequestId)?.size === 1
        ? records.filter((r) => r.client_request_id === m.clientRequestId && !known.has(r.id) && !found.has(r.id)) : [];
      if (!exact && !correlated.length) return [m];
      changed = true;
      if (exact) found.add(exact.id);
      correlated.forEach((r) => found.add(r.id));
      if (!(m.run || m.remoteId) && correlated.length) return correlated.map((r) => runItem(r, correlated.length === 1 ? m.id : r.id, keyId));
      return [exact ? runItem(exact, m.id, keyId) : m, ...correlated.map((r) => runItem(r, r.id, keyId))];
    });
    return changed ? { ...c, messages, updatedAt: Date.now() } : c;
  });
  // Correlation is metadata, never deduplication. Unassociated or multiply-owned values
  // retain independent rows, rather than guessing from matching prompt text.
  for (const record of records) if (record.kind === 'agent' && !found.has(record.id)) {
    const prompt = (record.input as { messages?: { role: string; content: unknown }[] } | null)?.messages?.filter((m) => m.role === 'user').at(-1)?.content;
    const words = typeof prompt === 'string' ? prompt : '';
    next.push({ id: `run:${record.id}`, title: words.slice(0, 40) || record.id, createdAt: Date.parse(record.created), updatedAt: Date.now(), messages: [...(words ? [{ id: `input:${record.id}`, role: 'user' as const, content: words }] : []), runItem(record, record.id, keyId)] });
  }
  return next;
}

export function fitsRunInput(input: unknown): boolean { return new TextEncoder().encode(JSON.stringify(input)).byteLength <= 1024 * 1024; }

/** A replayed update replaces one stable step. A GET can be ahead of replay, so an older
 * event must not move the snapshot backwards or reopen a terminal run. */
export function applyRunStep(record: RunRecord, event: RunEvent): RunRecord {
  if (!event.step || ['done','failed','cancelled'].includes(record.state) || Date.parse(event.time) < Date.parse(record.updated)) return record;
  const steps = [...(record.steps ?? [])], index = steps.findIndex((s) => s.id === event.step!.id);
  if (index < 0) steps.push(event.step); else steps[index] = event.step;
  return { ...record, updated: event.time, state: event.state ?? record.state, steps };
}
