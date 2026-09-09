import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { GatewayError } from './api';
import { VoiceRecorder, transcribe, requestSpeech, recordingMime, voiceCharacters, voiceWords, voiceTime, type RecordingState } from './voice';
import type { Transport } from './transport';

class FakeRecorder {
  static instances: FakeRecorder[] = [];
  static isTypeSupported = (mime: string) => mime.includes('webm');
  state = 'inactive';
  mimeType: string;
  ondataavailable: ((event: { data: Blob }) => void) | null = null;
  onstop: (() => void) | null = null;
  onerror: (() => void) | null = null;
  constructor(_stream: unknown, options?: { mimeType: string }) { this.mimeType = options?.mimeType ?? ''; FakeRecorder.instances.push(this); }
  start() { this.state = 'recording'; }
  stop() { this.state = 'inactive'; queueMicrotask(() => { this.ondataavailable?.({ data: new Blob(['clip']) }); this.onstop?.(); }); }
}
class FakeContext {
  static level = .1;
  close = vi.fn(async () => {});
  resume = vi.fn(async () => {});
  createAnalyser = () => ({ fftSize: 1024, getFloatTimeDomainData: (data: Float32Array) => data.fill(FakeContext.level) });
  createMediaStreamSource = () => ({ connect: vi.fn() });
}
const flush = async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); };
describe('voice recorder ownership', () => {
  const stopTrack = vi.fn();
  const stream = { getTracks: () => [{ stop: stopTrack }] };
  beforeEach(() => {
    vi.useFakeTimers({ toFake: ['setTimeout', 'clearTimeout', 'setInterval', 'clearInterval', 'performance'] });
    FakeRecorder.instances = []; FakeContext.level = .1;
    FakeRecorder.isTypeSupported = (mime) => mime.includes('webm');
    vi.stubGlobal('requestAnimationFrame', (cb: () => void) => setTimeout(cb, 0));
    vi.stubGlobal('cancelAnimationFrame', (id: ReturnType<typeof setTimeout>) => clearTimeout(id));
    vi.stubGlobal('MediaRecorder', FakeRecorder); vi.stubGlobal('AudioContext', FakeContext);
    vi.stubGlobal('navigator', { mediaDevices: { getUserMedia: vi.fn(async () => stream) } });
    stopTrack.mockClear();
  });
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });
  it('records level/time, stops at five minutes, transcribes once and releases media', async () => {
    const upload = vi.fn(async () => 'hello');
    const states: RecordingState[] = [];
    const recorder = new VoiceRecorder(upload, (s) => states.push(s));
    await recorder.start(); await vi.advanceTimersByTimeAsync(7000);
    expect(recorder.state).toMatchObject({ kind: 'recording', seconds: 7 });
    expect((recorder.state as { level: number }).level).toBeGreaterThan(0);
    expect((recorder.state as { waveform: number[] }).waveform).toHaveLength(60);
    FakeContext.level = 0; await vi.advanceTimersByTimeAsync(3000);
    expect((recorder.state as { waveform: number[] }).waveform.every((n) => n === 0)).toBe(true);
    await vi.advanceTimersByTimeAsync(293000); await flush();
    expect(upload).toHaveBeenCalledTimes(1);
    expect(recorder.state).toEqual({ kind: 'done', seconds: 300, text: 'hello' });
    expect(states).toContainEqual({ kind: 'transcribing', seconds: 300 });
    expect(stopTrack).toHaveBeenCalled();
    recorder.clear(); expect(recorder.state.kind).toBe('idle');
  });
  it('activates audio on the gesture and starts capture before showing recording', async () => {
    let permit!: (value: unknown) => void;
    let activated = false;
    class GestureContext extends FakeContext { resume = vi.fn(async () => { activated = true; }); }
    vi.stubGlobal('AudioContext', GestureContext);
    vi.stubGlobal('navigator', { mediaDevices: { getUserMedia: () => {
      expect(activated).toBe(true);
      return new Promise((resolve) => { permit = resolve; });
    } } });
    const recorder = new VoiceRecorder(vi.fn(), (state) => {
      if (state.kind === 'recording') {
        expect(FakeRecorder.instances.at(-1)?.state).toBe('recording');
        expect(state.waveform.at(-1)).toBeGreaterThan(0);
      }
    });
    const pending = recorder.start();
    expect(recorder.state.kind).toBe('requesting');
    permit(stream); await pending;
    expect(recorder.state.kind).toBe('recording');
    recorder.cancel();
    expect(stopTrack).toHaveBeenCalledTimes(1);
  });
  it('cancel never uploads and a delayed old stop cannot stop a new take', async () => {
    const upload = vi.fn(async () => 'hello');
    const recorder = new VoiceRecorder(upload, () => {});
    await recorder.start(); recorder.cancel(); const starting = recorder.start(); await starting; await flush();
    expect(FakeRecorder.instances.at(-1)?.state).toBe('recording');
    expect(recorder.state.kind).toBe('recording'); expect(upload).not.toHaveBeenCalled();
    recorder.cancel(); await flush(); expect(upload).not.toHaveBeenCalled();
  });
  it('late microphone permission is disposed after cancellation', async () => {
    let permit!: (value: unknown) => void;
    vi.stubGlobal('navigator', { mediaDevices: { getUserMedia: () => new Promise((resolve) => { permit = resolve; }) } });
    const upload = vi.fn(); const recorder = new VoiceRecorder(upload, () => {});
    const starting = recorder.start(); recorder.cancel(); permit(stream); await starting;
    expect(stopTrack).toHaveBeenCalledTimes(1); expect(recorder.state.kind).toBe('idle'); expect(upload).not.toHaveBeenCalled();
  });
  it('cancel aborts an upload and ignores a late transcript', async () => {
    let finish!: (value: string) => void;
    const upload = vi.fn(() => new Promise<string>((resolve) => { finish = resolve; }));
    const recorder = new VoiceRecorder(upload, () => {});
    await recorder.start(); recorder.stop(); await flush();
    expect(recorder.state.kind).toBe('transcribing'); recorder.cancel(); finish('late'); await flush();
    expect((upload.mock.calls[0] as unknown as [Blob, AbortSignal])[1].aborted).toBe(true);
    expect(recorder.state.kind).toBe('idle');
  });
  it.each([['NotAllowedError', 'blocked'], ['NotFoundError', 'missing'], ['NotReadableError', 'error']])('maps %s without sending audio', async (name, kind) => {
    vi.stubGlobal('navigator', { mediaDevices: { getUserMedia: async () => { throw new DOMException('denied', name); } } });
    const upload = vi.fn(); const recorder = new VoiceRecorder(upload, () => {}); await recorder.start();
    expect(recorder.state.kind).toBe(kind); expect(upload).not.toHaveBeenCalled();
  });
  it('uses WebM/Opus first and MP4 when that is the available recording format', () => {
    expect(recordingMime()).toBe('audio/webm;codecs=opus');
    FakeRecorder.isTypeSupported = (mime) => mime === 'audio/mp4'; expect(recordingMime()).toBe('audio/mp4');
  });
});

describe('voice requests and measurements', () => {
  const transport = (fetch: Transport['fetch']): Transport => ({ kind: 'direct', fetch, ping: async () => null, close: () => {} });
  it('serializes multipart bytes with the matching boundary and no model for the tunnel', async () => {
    const t = transport(async (_path, init) => {
      expect(_path).toBe('/v1/audio/transcriptions'); expect(new Headers(init?.headers).get('authorization')).toBe('Bearer test-key');
      expect(init?.body).toBeInstanceOf(ArrayBuffer);
      const form = await new Response(init?.body, { headers: init?.headers }).formData();
      expect(form.get('model')).toBeNull(); expect(form.get('language')).toBe('zh'); expect(form.get('response_format')).toBe('json');
      expect(await (form.get('file') as File).text()).toBe('audio');
      return Response.json({ text: '你好' });
    });
    expect(await transcribe(t, 'test-key', new Blob(['audio'], { type: 'audio/mp4' }), 'zh', new AbortController().signal)).toBe('你好');
  });
  it('posts MP3 speech without model and retains the raw refusal for Details', async () => {
    const raw = '{"error":{"code":"speech_budget_exhausted","message":"budget spent"}}';
    const t = transport(async (_path, init) => {
      expect(_path).toBe('/v1/audio/speech'); expect(JSON.parse(init?.body as string)).toEqual({ input: '你好🦊', response_format: 'mp3' });
      return new Response(raw, { status: 429, headers: { 'retry-after': '10' } });
    });
    try { await requestSpeech(t, 'test-key', '你好🦊', new AbortController().signal); throw new Error('missing rejection'); }
    catch (error) { expect(error).toBeInstanceOf(GatewayError); expect(error).toMatchObject({ rawBody: raw, retryAfterS: 10 }); }
  });
  it('counts CJK characters as words, Unicode code points as characters, and formats time', () => {
    expect(voiceWords('Hello world, 你好。')).toBe(4); expect(voiceCharacters('你好🦊')).toBe(3);
    expect(voiceTime(307)).toBe('5:07'); expect(voiceTime(Infinity)).toBe('—');
  });
});

class FakeAudio {
  static latest: FakeAudio;
  src = ''; paused = true; currentTime = 0; duration = Infinity; error = null;
  onended: (() => void) | null = null; onerror: (() => void) | null = null;
  onplaying: (() => void) | null = null; ontimeupdate: (() => void) | null = null; ondurationchange: (() => void) | null = null;
  constructor() { FakeAudio.latest = this; }
  setAttribute() {} removeAttribute() {} load() {}
  play() { this.paused = false; if (mediaURLs.get(this.src) instanceof Blob) queueMicrotask(() => this.onplaying?.()); return Promise.resolve(); }
  pause() { this.paused = true; }
}
const mediaURLs = new Map<string, object>();
class FakeSourceBuffer extends EventTarget {
  appendBuffer() { queueMicrotask(() => { FakeAudio.latest.onplaying?.(); this.dispatchEvent(new Event('updateend')); }); }
}
class FakeSource extends EventTarget {
  static isTypeSupported = (type: string) => type === 'audio/mpeg';
  addSourceBuffer() { return new FakeSourceBuffer(); }
  endOfStream() { FakeAudio.latest.duration = 2; FakeAudio.latest.ondurationchange?.(); }
}
import { VoicePlayer } from './voice';
describe('speech player', () => {
  beforeEach(() => {
    mediaURLs.clear();
    vi.stubGlobal('Audio', FakeAudio); vi.stubGlobal('MediaSource', FakeSource);
    vi.spyOn(URL, 'createObjectURL').mockImplementation((value) => {
      const url = `blob:test-${mediaURLs.size}`; mediaURLs.set(url, value);
      if (value instanceof FakeSource) queueMicrotask(() => value.dispatchEvent(new Event('sourceopen')));
      return url;
    });
    vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  });
  afterEach(() => { vi.unstubAllGlobals(); vi.restoreAllMocks(); });
  it('plays before the response ends and Stop cancels the active reader', async () => {
    let stream!: ReadableStreamDefaultController<Uint8Array>;
    const cancel = vi.fn();
    const request = vi.fn(async () => new Response(new ReadableStream({ start(c) { stream = c; }, cancel })));
    const player = new VoicePlayer(() => {});
    const playing = player.play('one', 'stream-case', 'kokoro', 'hello', request);
    await vi.waitFor(() => expect(request).toHaveBeenCalled());
    stream.enqueue(new Uint8Array([1, 2]));
    await vi.waitFor(() => expect(player.state.kind).toBe('playing'));
    player.stop(); await playing;
    expect(cancel).toHaveBeenCalledTimes(1); expect(player.state.kind).toBe('idle');
    expect(URL.revokeObjectURL).toHaveBeenCalled();
  });
  it('caches completed audio only in memory, and cache identity includes model and scope', async () => {
    const request = vi.fn(async () => new Response(new Uint8Array([1, 2, 3])));
    const player = new VoicePlayer(() => {});
    await player.play('first', 'cache-case', 'kokoro', '你好🦊', request);
    expect(player.state).toMatchObject({ kind: 'playing', characters: 3, duration: 2 });
    FakeAudio.latest.onended?.(); expect(player.state.kind).toBe('idle');
    await player.play('same', 'cache-case', 'kokoro', '你好🦊', request); await flush();
    expect(request).toHaveBeenCalledTimes(1);
    await player.play('model', 'cache-case', 'other', '你好🦊', request);
    await player.play('host', 'another-host', 'kokoro', '你好🦊', request);
    expect(request).toHaveBeenCalledTimes(3); player.stop();
  });
  it('switching replies aborts the old request and a late refusal cannot replace the new player', async () => {
    let reject!: (error: Error) => void;
    let oldSignal!: AbortSignal;
    const player = new VoicePlayer(() => {});
    const first = player.play('old', 'switch-case', 'kokoro', 'old', (signal) => { oldSignal = signal; return new Promise((_resolve, fail) => { reject = fail; }); });
    await vi.waitFor(() => expect(oldSignal).toBeDefined());
    await player.play('new', 'switch-case', 'kokoro', 'new', async () => new Response(new Uint8Array([1])));
    expect(oldSignal.aborted).toBe(true); reject(new Error('late')); await first;
    expect(player.state).toMatchObject({ kind: 'playing', id: 'new' }); player.stop();
  });
  it('keeps request failures on the requested reply and releases the element', async () => {
    const error = new GatewayError(429, 'speech_budget_exhausted', '', 'spent', 10);
    const player = new VoicePlayer(() => {});
    await player.play('failed', 'error-case', 'kokoro', 'hello', async () => { throw error; });
    expect(player.state).toEqual({ kind: 'error', id: 'failed', error }); expect(FakeAudio.latest.paused).toBe(true);
  });
});

import { handleFake } from '../dev/fake-backend';
it('fake audio routes expose independent model ids and binary MP3 without spending chat tokens', () => {
  const req = { method: 'POST', path: '/v1/audio/speech', headers: { authorization: 'Bearer test-key' }, body: '{"input":"hello"}' };
  const me = { ...req, method: 'GET', path: '/me', body: '' };
  const before = JSON.parse(handleFake(me).body!);
  expect(handleFake(req).status).toBe(404);
  const response = handleFake(req, { speech: true });
  expect(response.headers['content-type']).toBe('audio/mpeg'); expect(response.bytes?.length).toBeGreaterThan(100);
  const current = JSON.parse(handleFake(me, { speech: true }).body!);
  expect(current.host.audio).toEqual({ transcriptions: null, speech: 'kokoro' });
  expect(current.usage.today_tokens).toBe(before.usage.today_tokens);
  expect(handleFake({ ...req, path: '/v1/audio/transcriptions' }, { transcriptions: true }).body).toContain('Please answer');
  const refused = handleFake(req, { speech: true, audioFailure: 429 });
  expect(refused.headers['retry-after']).toBe('12'); expect(refused.body).toContain('speech_budget_exhausted');
});

it('Blob fallback unlocks inside the tap, waits for EOF, and plays on the same element', async () => {
  mediaURLs.clear();
  vi.stubGlobal('Audio', FakeAudio); vi.stubGlobal('MediaSource', undefined); vi.stubGlobal('ManagedMediaSource', undefined);
  vi.spyOn(URL, 'createObjectURL').mockImplementation((value) => { const url = 'blob:fallback'; mediaURLs.set(url, value); return url; });
  vi.spyOn(URL, 'revokeObjectURL').mockImplementation(() => {});
  let stream!: ReadableStreamDefaultController<Uint8Array>;
  const request = vi.fn(async () => new Response(new ReadableStream({ start(c) { stream = c; } })));
  const player = new VoicePlayer(() => {});
  try {
    const playing = player.play('blob', 'blob-case', 'kokoro', 'hello', request);
    const element = FakeAudio.latest;
    expect(element.src).toMatch(/^data:audio\/wav/); expect(element.paused).toBe(false);
    await vi.waitFor(() => expect(stream).toBeDefined());
    stream.enqueue(new Uint8Array([1, 2, 3])); await flush();
    expect(player.state.kind).toBe('making');
    element.onended?.(); expect(player.state.kind).toBe('making');
    stream.close(); await playing; await flush();
    expect(FakeAudio.latest).toBe(element); expect(element.src).toBe('blob:fallback');
    expect(player.state).toMatchObject({ kind: 'playing', id: 'blob' });
  } finally { player.stop(); vi.unstubAllGlobals(); vi.restoreAllMocks(); }
});
