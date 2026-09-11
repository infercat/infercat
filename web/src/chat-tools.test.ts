import { fakeTransport } from './test/fakes';
import { expect, it } from 'vitest';
import { offersHostTools, mergeChatRuns } from './chatTools';
import { fakeMe } from '../dev/fake-backend';
import { scriptedRun } from '../dev/fake-runs';
import { mergeImageJobs, type ImageJob } from './imageJobs';
import { chatEvents } from './api';
import type { Conversation } from './storage';
import { NEW_REPLY, reduceReply } from './stream';
const conv = (): Conversation => ({id:'c',title:'fox',createdAt:1,updatedAt:1,messages:[{id:'u',role:'user',content:'fox'},{id:'reply',role:'assistant',content:'Here it is.',hostRun:{keyId:'key',requestId:'request',ids:[],records:[]}}]});
const parent = (id='parent') => ({...scriptedRun(id,'done'),kind:'chat',client_request_id:'request',key_id:'key'});
const child = (id='image'):ImageJob => ({...scriptedRun(id,'running'),kind:'image',output:undefined,key_id:'key',input:{prompt:'fox',parent_run_id:'parent',client_request_id:'request',tool_call_id:'tool'},batch:{id:'batch',index:0,count:1}});
it('opts in for any offered host tool, independent of image and agent capabilities',()=>{
 const me=fakeMe({});expect(offersHostTools(me)).toBe(false);me.agent=true;expect(offersHostTools(me)).toBe(false);
 for(const tools of [['make_image'],['web_search'],['make_image','web_search']]){me.host_tools=tools;expect(offersHostTools(me)).toBe(true);}
 me.host_tools=[];expect(offersHostTools(me)).toBe(false);
});
it('associates all parent ids without replacing the streamed prose or duplicating on reset',()=>{
 let cs=mergeChatRuns([conv()],[parent(),parent('other')],'key');cs=mergeChatRuns(cs,[parent()],'key');
 const m=cs[0]!.messages[1]!;expect(m.content).toBe('Here it is.');expect(m.kind!=='run'&&m.hostRun?.ids).toEqual(['parent','other']);
 expect(mergeChatRuns([conv()],[parent()],'another')[0]!.messages[1]).toEqual(conv().messages[1]);
});
it('places child rows under their reply, keeps state in place, and reconciles a lost handoff by unique metadata',()=>{
 let cs=mergeImageJobs([conv()],[child()],'key');expect(cs[0]!.messages.map(m=>m.id)).toEqual(['u','reply','image']);
 cs=mergeImageJobs(cs,[{...child(),state:'done'}],'key');expect(cs).toHaveLength(1);expect(cs[0]!.messages).toHaveLength(3);expect(cs[0]!.messages[2]!.kind==='run'&&cs[0]!.messages[2]!.job?.state).toBe('done');
 const ambiguous=mergeImageJobs([conv(),{...conv(),id:'other'}],[child()],'key');expect(ambiguous).toHaveLength(3);
});
it('moves a recovered child only after the reply association exists',()=>{
 const orphan=mergeImageJobs([], [child()], 'key');const cs=mergeImageJobs([...orphan,conv()],[child()],'key');expect(cs).toHaveLength(1);expect(cs[0]!.id).toBe('c');
});
it('reads the explicit handoff then normal deltas and preserves the inner refusal Retry-After',async()=>{
 let sends=0;const t=fakeTransport(async()=>{sends++;return new Response('data: {"code":"run_handoff","charged":0}\n\nevent: run\ndata: {"run_id":"parent"}\n\ndata: {"choices":[{"delta":{"content":"Fox"}}]}\n\ndata: {"error":{"code":"rate_limited","message":"wait","retry_after":60}}\n\n');});
 const events=[];for await(const e of chatEvents(t,'secret',{model:'m',messages:[]})) events.push(e);
 expect(events.map(e=>e.kind)).toEqual(['run','content','error']);expect(events[2]).toMatchObject({error:{retryAfterS:60}});expect(sends).toBe(1);expect(reduceReply(NEW_REPLY,{kind:'run',id:'parent'})).toEqual(NEW_REPLY);
});
