import { imageRefresh } from '../imageRefresh';
import { imageLimit } from '../imageJobs';
import { hostImages } from '../api';
import { useEffect, useRef, useState, type Dispatch, type SetStateAction } from 'react';
import { call, cancelRun, getMe, GatewayError, timeoutSignal } from '../api';
import { imageJobs, mergeImageJobs, type ImageJob } from '../imageJobs';
import { tr } from '../i18n/text';
import type { Live, SessionEvent } from '../session';
import { hostScope, saveChat, titleFrom, type Conversation, type RunItem } from '../storage';
export function useImageJobs(live: Live, enabled: boolean, setConvs: Dispatch<SetStateAction<Conversation[]>>, dispatch: (e: SessionEvent) => void) {
  const [jobs, setJobs] = useState<ImageJob[]>([]), [pending, setPending] = useState(new Set<string>()), [notice, setNotice] = useState('');
  const current = useRef({ live, enabled }); current.current = { live, enabled };
  const queuedRead = useRef<() => Promise<void>>(() => Promise.resolve());
  const active = useRef(new Set<string>()), controllers = useRef(new Set<AbortController>());
  useEffect(() => { const held = controllers.current; return () => held.forEach((c) => c.abort()); }, [live.transport, live.secret, enabled]);
  useEffect(() => {
    const ac = new AbortController();
    queuedRead.current = imageRefresh(async (signal) => {
      const all = await imageJobs(live.transport, live.secret, timeoutSignal(15000, signal));
      if (!signal.aborted) { setJobs(all); setConvs((prev) => mergeImageJobs(prev, all, live.me.key.id)); }
    }, ac.signal, live.me.limits.rpm > 0 ? Math.max(1000, 120000 / live.me.limits.rpm) : 1000);
    return () => ac.abort();
  }, [live.transport, live.secret, live.me.key.id, live.me.limits.rpm, live.offline, enabled, setConvs]);
  async function refresh(signal: AbortSignal) {
    if (!signal.aborted && current.current.enabled && !current.current.live.offline && hostImages(current.current.live.me)) await queuedRead.current();
  }
  const allowed = () => current.current.enabled && !current.current.live.offline && current.current.live.key === 'active';
  async function submit(conv: Conversation, prompts: string[]): Promise<boolean> {
    if (!allowed() || active.current.has('submit') || !prompts.length) return false;
    active.current.add('submit'); setPending(new Set(active.current)); setNotice('');
    const ac = new AbortController(); controllers.current.add(ac); const signal = () => timeoutSignal(15000, ac.signal);
    let marker: RunItem | undefined, turnId = '', posted = false, confirmed = false;
    try {
      const me = await getMe(live.transport, live.secret, signal());
      if (!allowed() || ac.signal.aborted) return false;
      dispatch({ t: 'meOk', me });
      const cap = hostImages(me);
      if (!cap?.model) throw new Error(tr('app_job_make'));
      if (imageLimit(cap.queue_cap, 8) >= 0 && cap.queued + prompts.length > imageLimit(cap.queue_cap, 8)) { setNotice(tr('app_job_refused', { host: me.host.name, n: imageLimit(cap.queue_cap, 8) })); return false; }
      const id = crypto.randomUUID(); turnId = crypto.randomUUID();
      marker = { id, kind: 'run', runKind: 'image', role: 'assistant', content: '', clientRequestId: id, keyId: me.key.id, submission: 'pending' };
      const messages = [{ id: turnId, role: 'user' as const, content: prompts.join('\n\n') }, marker];
      const next = { ...conv, title: conv.messages.length ? conv.title : titleFrom(prompts[0]!), updatedAt: Date.now(), messages: [...conv.messages, ...messages] };
      saveChat(hostScope(live.addr), next); // correlation survives an uncertain POST or page close
      setConvs((prev) => prev.map((c) => c.id === conv.id ? { ...next, messages: [...c.messages, ...messages] } : c));
      posted = true;
      const made = await imageJobs(live.transport, live.secret, signal(), { prompts, conversation: conv.id, client_request_id: id });
      confirmed = true;
      if (!ac.signal.aborted) { setConvs((prev) => mergeImageJobs(prev, made, me.key.id)); void refresh(signal()).catch(() => {}); }
      return true;
    } catch (error) {
      if (ac.signal.aborted) return posted;
      if (confirmed) { setNotice(String(error)); return true; }
      const refused = !posted || error instanceof GatewayError && error.status < 500;
      if (refused) {
        if (marker) setConvs((prev) => prev.map((c) => ({ ...c, ...(c.id === conv.id && !conv.messages.length && c.title === titleFrom(prompts[0]!) ? { title: conv.title } : {}), messages: c.messages.filter((m) => m.id !== marker!.id && m.id !== turnId) })));
        setNotice(error instanceof GatewayError && error.code === 'image_budget_exhausted' ? tr('app_job_daily_exhausted') : error instanceof GatewayError && error.code === 'image_queue_full' ? tr('app_job_refused', { host: live.me.host.name, n: error.limit ?? hostImages(live.me)!.queue_cap }) : String(error instanceof Error ? error.message : error));
        return false;
      }
      setConvs((prev) => prev.map((c) => ({ ...c, messages: c.messages.map((m) => m.id === marker?.id ? { ...m, submission: 'uncertain' as const, details: String(error) } : m) })));
      void refresh(signal()).catch(() => {}); // Keep the uncertain asking; never replay its POST.
      return true;
    } finally { controllers.current.delete(ac); active.current.delete('submit'); setPending(new Set(active.current)); }
  }
  async function act(job: ImageJob, discard = false) {
    if (!allowed() || active.current.has(job.id)) return;
    active.current.add(job.id); setPending(new Set(active.current)); setNotice('');
    const ac = new AbortController(); controllers.current.add(ac);
    try {
      if (discard) await call(live.transport, live.secret, `/v1/images/outputs/${encodeURIComponent(job.id)}`, { method: 'DELETE', signal: timeoutSignal(15000, ac.signal) });
      else await cancelRun(live.transport, live.secret, job.id, timeoutSignal(15000, ac.signal));
    } catch (error) { if (!ac.signal.aborted) setNotice(String(error)); }
    finally {
      if (!ac.signal.aborted) try { await refresh(timeoutSignal(15000, ac.signal)); } catch (error) { setNotice(String(error)); }
      controllers.current.delete(ac); active.current.delete(job.id); setPending(new Set(active.current));
    }
  }
  return { jobs, pending, notice, setNotice, refresh, submit, act };
}
