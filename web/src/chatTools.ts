import { hostImages, type Me, type RunRecord } from './api';
import type { Conversation, Message } from './storage';
import { runItem } from './runs';
export const offersImageTool = (me: Me): boolean => Boolean(hostImages(me)?.model && me.host_tools?.includes('make_image'));
/** Correlation never deduplicates creation: all matching parent ids remain visible. */
export function mergeChatRuns(convs: Conversation[], records: RunRecord[], keyId?: string): Conversation[] {
  const owners = new Map<string, number>();
  for (const c of convs) for (const m of c.messages) if (m.kind !== 'run' && m.hostRun && m.hostRun.keyId === keyId) owners.set(m.hostRun.requestId, (owners.get(m.hostRun.requestId) ?? 0) + 1);
  return convs.map(c => !c.messages.some(m => m.kind !== 'run' && m.hostRun) ? c : ({ ...c, messages: c.messages.map((m): Message => {
    if (m.kind === 'run' || !m.hostRun || m.hostRun.keyId !== keyId) return m;
    const link = m.hostRun, held = new Map(link.records.map(r => [r.id, r]));
    for (const r of records) if (r.kind === 'chat' && (link.ids.includes(r.id) || (r.client_request_id === link.requestId && owners.get(link.requestId) === 1))) held.set(r.id, runItem(r).run!);
    return { ...m, hostRun: { ...link, ids: [...new Set([...link.ids, ...held.keys()])], records: [...held.values()] } };
  }) }));
}
