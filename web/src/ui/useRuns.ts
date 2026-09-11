import { hostImages } from '../api';
import { useEffect, useRef, useState, type Dispatch, type SetStateAction } from 'react';
import { timeoutSignal, answerRun, cancelRun, describeError, GatewayError, getRun, listRuns, submitRun, type ChatRequest, type RunRecord } from '../api';
import type { Live, SessionEvent } from '../session';
import { type Conversation, type RunItem, type Message, hostScope, loadChats, saveChat } from '../storage';
import { followRuns, mergeRunRecords, runItem } from '../runs';

export function useRuns(live: Live, enabled: boolean, convs: Conversation[], setConvs: Dispatch<SetStateAction<Conversation[]>>, dispatch: (e: SessionEvent) => void, refresh: () => Promise<boolean>, imagesChanged?: (signal: AbortSignal) => Promise<void>) {
  const [connected, setConnected] = useState(false), [pending, setPending] = useState<Set<string>>(new Set());
  const [observed, setObserved] = useState<Set<string>>(new Set());
  const latest = useRef({ live, enabled, convs, refresh, imagesChanged }); latest.current = { live, enabled, convs, refresh, imagesChanged };
  const active = useRef(new Set<string>()), requests = useRef(new Set<AbortController>());
  useEffect(() => { const held = requests.current; return () => { held.forEach((c) => c.abort()); }; }, [live.transport, live.secret, enabled]);
  const merge = (records: RunRecord[]) => setConvs((prev) => mergeRunRecords(prev, records.filter((r) => ['agent','chat'].includes(r.kind)), live.me.key.id));
  useEffect(() => {
    if (!enabled || live.offline || live.key !== 'active' || (!live.me.agent && !hostImages(live.me)?.model)) return;
    const ac = new AbortController();
    void followRuns({ transport: live.transport, secret: live.secret, signal: ac.signal,
      onEvent: (signal) => { void latest.current.imagesChanged?.(signal).catch((error) => { if (!signal.aborted) dispatch({ t: 'streamError', code: error instanceof GatewayError ? error.code : '', error: describeError(error, live.me.host.name) }); }); return Promise.resolve(); },
      known: () => latest.current.convs.flatMap((c) => c.messages.flatMap((m) => m.kind !== 'run' ? m.hostRun?.ids ?? [] : m.runKind !== 'image' ? [m.run?.id ?? m.remoteId ?? ''].filter(Boolean) : [])),
      changed(records, reset) { if (!ac.signal.aborted) { setObserved((old) => new Set([...(reset ? [] : old), ...records.map((r) => r.id)])); setConvs((prev) => mergeRunRecords(prev, records.filter((r) => ['agent','chat'].includes(r.kind)), live.me.key.id)); if (records.some((r) => ['done','failed','cancelled'].includes(r.state))) void latest.current.refresh(); } },
      connected(value) { if (!ac.signal.aborted) { setConnected(value); if (!value) dispatch({ t: 'streamError', code: '', error: describeError(new Error('Run event stream ended'), live.me.host.name) }); } },
      failed(error) { if (!ac.signal.aborted) dispatch({ t: 'streamError', code: error instanceof GatewayError ? error.code : '', error: describeError(error, live.me.host.name) }); },
    });
    return () => { ac.abort(); setConnected(false); };
  }, [enabled, live.offline, live.key, live.me.agent, hostImages(live.me)?.model, live.transport, live.secret, live.me.host.name, live.me.key.id, setConvs, dispatch]);
  async function act(item: RunItem, answer?: { id: string; allow: boolean }) {
    const current = latest.current;
    if (!current.enabled || current.live.offline || current.live.key !== 'active' || !item.run || active.current.has(item.id)) return;
    active.current.add(item.id); setPending(new Set(active.current));
    const ac = new AbortController(); requests.current.add(ac);
    const { transport, secret } = current.live;
    try { const record = await (answer ? answerRun(transport, secret, item.run.id, answer.id, answer.allow, timeoutSignal(15000, ac.signal)) : cancelRun(transport, secret, item.run.id, timeoutSignal(15000, ac.signal))); if (!ac.signal.aborted) merge([record]); }
    catch (error) {
      if (!ac.signal.aborted) {
        try { merge([await getRun(transport, secret, item.run.id, timeoutSignal(15000, ac.signal))]); }
        catch { dispatch({ t: 'streamError', code: error instanceof GatewayError ? error.code : '', error: describeError(error, current.live.me.host.name) }); }
        if (!ac.signal.aborted) setConvs((prev) => prev.map((c) => ({ ...c, messages: c.messages.map((m) => m.id === item.id ? { ...m, details: error instanceof Error ? error.message : String(error) } : m) })));
      }
    } finally { requests.current.delete(ac); active.current.delete(item.id); setPending(new Set(active.current)); void latest.current.refresh(); }
  }
  async function submit(convId: string, input: ChatRequest, history: Message[], retryId?: string) {
    const current = latest.current;
    if (!current.enabled || current.live.offline || current.live.key !== 'active' || (retryId && active.current.has(retryId))) return;
    if (retryId) { active.current.add(retryId); setPending(new Set(active.current)); }
    const id = crypto.randomUUID(), ac = new AbortController(); requests.current.add(ac);
    const item: RunItem = { id, kind: 'run', role: 'assistant', content: '', submission: 'pending', clientRequestId: id, keyId: current.live.me.key.id };
    // Persist the correlation mapping before POST, including a user turn whose React update
    // has not committed yet. Binary attachments remain in their existing bounded store.
    const scope = hostScope(current.live.addr), memory = current.convs.find((c) => c.id === convId), saved = loadChats(scope).find((c) => c.id === convId);
    const held = saved && (!memory || saved.updatedAt >= memory.updatedAt) ? saved : memory;
    const append = (c: Conversation): Conversation => ({ ...c, updatedAt: Date.now(), messages: [...c.messages, ...history.filter((m) => !c.messages.some((old) => old.id === m.id)), item] });
    if (held) saveChat(scope, append(held));
    setConvs((prev) => prev.map((c) => c.id === convId ? append(c) : c));
    const patch = (change: Partial<RunItem>) => setConvs((prev) => prev.map((c) => c.id === convId ? { ...c, updatedAt: Date.now(), messages: c.messages.map((m) => m.id === id && !(m.kind === 'run' && m.run && change.submission) ? { ...m, ...change } as RunItem : m) } : c));
    let remoteId = '';
    try {
      const result = await submitRun(current.live.transport, current.live.secret, input, timeoutSignal(15000, ac.signal), id);
      remoteId = result.id; patch({ remoteId });
      const record = await getRun(current.live.transport, current.live.secret, remoteId, timeoutSignal(15000, ac.signal));
      patch({ ...runItem(record, id, current.live.me.key.id), submission: undefined });
      // The stream may discover the run before POST returns. Its provisional recovered row
      // is removed only after the exact server id links this row; prompts are never compared.
      setConvs((prev) => prev.filter((c) => c.id !== `run:${remoteId}`));
    } catch (error) {
      if (ac.signal.aborted) return;
      patch({ submission: !remoteId && error instanceof GatewayError && error.status < 500 ? 'refused' : 'uncertain', details: error instanceof Error ? error.message : String(error) });
      try { const list = await listRuns(current.live.transport, current.live.secret, timeoutSignal(15000)); const records = await Promise.all(list.runs.filter((r) => r.kind === 'agent').map((r) => getRun(current.live.transport, current.live.secret, r.id, timeoutSignal(15000)))); merge(records); } catch { /* The pending row remains; no POST replay. */ }
    } finally { requests.current.delete(ac); if (retryId) { active.current.delete(retryId); setPending(new Set(active.current)); } void latest.current.refresh(); }
  }
  return { connected, observed, pending, submit, act };
}
