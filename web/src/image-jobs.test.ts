import { expect, it } from 'vitest';
import { imagePrompts } from './imageJobs';
it('makes one image per nonempty paragraph, preserving lines within it', () => {
  expect(imagePrompts(' A poster\nwith blue ink.\n\n Another image.\n\n\n ')).toEqual(['A poster\nwith blue ink.', 'Another image.']);
  expect(imagePrompts(' first\r\n \t\r\nsecond\r\n\r\n')).toEqual(['first', 'second']);
  expect(imagePrompts(' \n\n\t ')).toEqual([]);
  expect(imagePrompts('一只猫\n蓝色背景\n\n一座桥')).toEqual(['一只猫\n蓝色背景', '一座桥']);
});

import { imageArtifact, imageElapsed, imageJobs, mergeImageJobs, type ImageJob } from './imageJobs';
import type { Transport } from './transport';
import type { Conversation } from './storage';
const signal = new AbortController().signal;
const job = (id: string, batch = 'batch', index = 0): ImageJob => ({ id, kind: 'image', key_id: 'key', state: 'done', position: 0, created: '2026-09-12T12:00:00Z', started: '2026-09-12T12:01:00Z', updated: '2026-09-12T12:01:30Z', expires: '2026-09-19T12:01:30Z', cancel_requested: false, input: { prompt: id, client_request_id: 'request' }, batch: { id: batch, index, count: 2 }, attempts: [] });
const asking: Conversation = { id: 'chat', title: 'Images', createdAt: 1, updatedAt: 1, messages: [{ id: 'user', role: 'user', content: 'Two prompts' }, { id: 'request', kind: 'run', runKind: 'image', role: 'assistant', content: '', keyId: 'key', clientRequestId: 'request', submission: 'uncertain' }] };
it('reconciles all siblings and repeated batches without duplication on later snapshots', () => {
  const jobs = [job('b','batch',1),job('a'),job('c','duplicate',0)];
  let convs = mergeImageJobs([asking],jobs,'key');
  expect(convs).toHaveLength(1);expect(convs[0]!.messages.map((m)=>m.id)).toEqual(['user','a','b','c']);
  convs = mergeImageJobs(convs,jobs,'key');
  expect(convs[0]!.messages.map((m)=>m.id)).toEqual(['user','a','b','c']);
});
it('does not attach another key or ambiguous local mapping to an asking', () => {
  const convs = mergeImageJobs([asking], [job('a')], 'other-key');
  expect(convs).toHaveLength(2);expect(convs[0]!.messages.at(-1)!.id).toBe('request');
  const duplicate = mergeImageJobs([asking,{...asking,id:'another'}], [job('a')], 'key');
  expect(duplicate).toHaveLength(3);
});
it('uses actual image execution age once started, and preserves terminal elapsed',()=>{
  expect(imageElapsed(job('a'),Date.parse('2026-09-12T15:00:00Z'))).toBe(30);
  expect(imageElapsed({...job('a'),state:'running'},Date.parse('2026-09-12T12:01:45Z'))).toBe(45);
});
it('sends the atomic batch once, with correlation outside prompts',async()=>{
  let count=0;const t={fetch:async(path:string,init:RequestInit)=>{count++;expect(path).toBe('/v1/images/jobs');expect(new Headers(init.headers).get('authorization')).toBe('Bearer secret');expect(JSON.parse(String(init.body))).toEqual({prompts:['a','b'],conversation:'chat',client_request_id:'request'});throw new Error('lost');}} as unknown as Transport;
  await expect(imageJobs(t,'secret',signal,{prompts:['a','b'],conversation:'chat',client_request_id:'request'})).rejects.toThrow('lost');expect(count).toBe(1);
});
it('image output is authenticated, fixed-route, PNG/JPEG-only and bounded at 8 MiB',async()=>{
  const t={fetch:async(path:string,init:RequestInit)=>{expect(path).toBe('/v1/images/outputs/a%2Fb?download=1');expect(new Headers(init.headers).get('authorization')).toBe('Bearer secret');return new Response(new Uint8Array(8*1024*1024+1),{headers:{'content-type':'image/png'}});}} as unknown as Transport;
  await expect(imageArtifact(t,'secret','a/b',signal,true)).rejects.toThrow('exceeds 8 MiB');
  const svg={fetch:async()=>new Response('<svg/>',{headers:{'content-type':'image/svg+xml'}})} as unknown as Transport;
  await expect(imageArtifact(svg,'secret','a',signal)).rejects.toThrow('Invalid image output');
  const png={fetch:async()=>new Response(new Uint8Array(2*1024*1024),{headers:{'content-type':'image/png'}})} as unknown as Transport;
  expect((await imageArtifact(png,'secret','a',signal)).size).toBe(2*1024*1024);
});

it('accepts a POST snapshot without a position and never invents queue rank', async () => {
  const created = { ...job('a'), state: 'queued' as const }; delete created.position;
  const t = { fetch: async () => Response.json({ jobs: [created] }, { status: 202 }) } as unknown as Transport;
  const result = await imageJobs(t, 'secret', signal, { prompts: ['a'], conversation: 'chat', client_request_id: 'request' });
  expect(result[0]!.position).toBeUndefined();
});

import { tr } from './i18n/text';
it('discloses that both prompts and pictures are kept for the reported days in EN/ZH', () => {
  expect(tr('app_privacy_images', { days: 7 }, 'en')).toBe('Pictures you ask for, and the words you asked with, stay on the host under your key for 7 days.');
  expect(tr('app_privacy_images', { days: 3 }, 'zh')).toBe('你请求生成的图片和所用的文字，会按你的密钥在主机上保留 3 天。');
});

import { privacy } from './i18n/text';
it('adds exact retention only when the host reports an image engine, also on logging hosts', () => {
  expect(privacy('Owned host', false)).not.toContain('Pictures you ask for');
  for (const logging of [false, true]) expect(privacy('Owned host', logging, 7)).toContain(tr('app_privacy_images', { days: 7 }));
  expect(privacy('Owned host', false, 7)).toContain('request logs record counts, never text');
});

it('uses the approved daily-image refusal in both languages', () => {
  expect(tr('app_job_daily_exhausted', {}, 'en')).toBe("The host's images for today are used up. Try again tomorrow.");
  expect(tr('app_job_daily_exhausted', {}, 'zh')).toBe('主机今天的图片额度已用完，明天再试。');
});
