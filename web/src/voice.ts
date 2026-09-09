import { postAudio } from './api';
import type { Transport } from './transport';

export const RECORDING_LIMIT_SECONDS = 300;
export const recordingMime = (): string => ['audio/webm;codecs=opus', 'audio/mp4'].find((type) => MediaRecorder.isTypeSupported(type)) ?? '';
export const voiceTime = (seconds: number): string => Number.isFinite(seconds) ? `${Math.floor(seconds / 60)}:${String(Math.floor(seconds % 60)).padStart(2, '0')}` : '—';
export const voiceWords = (text: string): number => (text.match(/[\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]|[^\s\p{Script=Han}\p{Script=Hiragana}\p{Script=Katakana}\p{Script=Hangul}]+/gu) ?? []).filter((word) => /[\p{L}\p{N}]/u.test(word)).length;
export const voiceCharacters = (text: string): number => Array.from(text).length;

export async function transcribe(t: Transport, secret: string, clip: Blob, language: string, signal: AbortSignal): Promise<string> {
  const form = new FormData();
  const extension = clip.type.includes('mp4') ? 'm4a' : clip.type.includes('ogg') ? 'ogg' : 'webm';
  form.set('file', clip, `recording.${extension}`);
  form.set('language', language);
  form.set('response_format', 'json');
  // Request is only a serializer here; it makes no network call. FormData itself is not a
  // supported tunnel body, and its generated boundary must match the bytes exactly.
  const encoded = new Request('https://serialization.invalid/', { method: 'POST', body: form });
  const body = await encoded.arrayBuffer();
  signal.throwIfAborted();
  const response = await postAudio(t, secret, 'transcriptions', body, encoded.headers.get('content-type')!, signal);
  const result = await response.json() as { text?: unknown };
  if (typeof result.text !== 'string') throw new Error('The host returned no transcription text.');
  return result.text;
}
export function requestSpeech(t: Transport, secret: string, text: string, signal: AbortSignal): Promise<Response> {
  return postAudio(t, secret, 'speech', JSON.stringify({ input: text, response_format: 'mp3' }), 'application/json', signal);
}

export type RecordingState =
  | { kind: 'idle' | 'requesting' | 'blocked' | 'missing' }
  | { kind: 'recording'; seconds: number; level: number; waveform: number[] }
  | { kind: 'transcribing'; seconds: number }
  | { kind: 'done'; seconds: number; text: string }
  | { kind: 'error'; seconds: number; error: unknown };

/** Owns one take. Cancellation also invalidates permission and transcription promises. */
export class VoiceRecorder {
  state: RecordingState = { kind: 'idle' };
  private controller: AbortController | null = null;
  private recorder: MediaRecorder | null = null;
  private stream: MediaStream | null = null;
  private context: AudioContext | null = null;
  private tick: ReturnType<typeof setInterval> | undefined;
  private cap: ReturnType<typeof setTimeout> | undefined;
  private started = 0;
  private stoppedSeconds: number | null = null;
  constructor(private readonly upload: (clip: Blob, signal: AbortSignal) => Promise<string>, private readonly changed: (state: RecordingState) => void) {}
  private emit(state: RecordingState): void { this.state = state; this.changed(state); }
  async start(): Promise<void> {
    this.cancel();
    const controller = new AbortController(); this.controller = controller;
    this.emit({ kind: 'requesting' });
    try {
      if (!navigator.mediaDevices?.getUserMedia || typeof MediaRecorder === 'undefined') {
        this.emit({ kind: 'missing' }); return;
      }
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      if (controller.signal.aborted) { stream.getTracks().forEach((track) => track.stop()); return; }
      this.stream = stream;
      const mimeType = recordingMime();
      const recorder = new MediaRecorder(stream, mimeType ? { mimeType } : undefined);
      this.recorder = recorder;
      const chunks: Blob[] = [];
      const context = new AudioContext(); this.context = context;
      void context.resume().catch(() => {});
      const analyser = context.createAnalyser(); analyser.fftSize = 1024;
      context.createMediaStreamSource(stream).connect(analyser);
      const samples = new Float32Array(analyser.fftSize);
      const waveform: number[] = Array(60).fill(0);
      recorder.ondataavailable = (event) => { if (!controller.signal.aborted && event.data.size) chunks.push(event.data); };
      recorder.onerror = () => {
        if (!controller.signal.aborted) { this.cancel(); this.emit({ kind: 'error', seconds: this.elapsed(), error: new Error('Microphone recording failed.') }); }
      };
      recorder.onstop = () => {
        if (controller.signal.aborted) { chunks.length = 0; return; }
        const seconds = this.stoppedSeconds ?? this.elapsed(); this.releaseMedia();
        const clip = new Blob(chunks, { type: recorder.mimeType || mimeType });
        chunks.length = 0;
        this.emit({ kind: 'transcribing', seconds });
        void this.upload(clip, controller.signal).then((text) => {
          if (!controller.signal.aborted) this.emit({ kind: 'done', seconds, text });
        }, (error: unknown) => {
          if (!controller.signal.aborted) this.emit(error instanceof Error && error.name === 'AbortError' ? { kind: 'idle' } : { kind: 'error', seconds, error });
        });
      };
      this.started = performance.now(); this.stoppedSeconds = null;
      recorder.start(250);
      this.emit({ kind: 'recording', seconds: 0, level: 0, waveform: [...waveform] });
      this.tick = setInterval(() => {
        analyser.getFloatTimeDomainData(samples);
        const rms = Math.sqrt(samples.reduce((sum, n) => sum + n * n, 0) / samples.length);
        const level = Math.min(1, rms * 4); waveform.push(level); waveform.shift();
        this.emit({ kind: 'recording', seconds: this.elapsed(), level, waveform: [...waveform] });
      }, 50);
      this.cap = setTimeout(() => this.stop(), RECORDING_LIMIT_SECONDS * 1000);
    } catch (error) {
      if (controller.signal.aborted) return;
      this.releaseMedia();
      const name = error instanceof Error ? error.name : '';
      this.emit(name === 'NotAllowedError' || name === 'SecurityError' ? { kind: 'blocked' } : name === 'NotFoundError' || name === 'NotSupportedError' ? { kind: 'missing' } : { kind: 'error', seconds: 0, error });
    }
  }
  private elapsed(): number { return Math.min(RECORDING_LIMIT_SECONDS, Math.max(0, (performance.now() - this.started) / 1000)); }
  stop(): void {
    clearInterval(this.tick); clearTimeout(this.cap);
    if (this.recorder?.state === 'recording') { this.stoppedSeconds = this.elapsed(); this.recorder.stop(); }
  }
  cancel(): void {
    this.controller?.abort(); this.controller = null;
    if (this.recorder?.state === 'recording') this.recorder.stop();
    this.releaseMedia(); this.emit({ kind: 'idle' });
  }
  clear(): void { if (this.state.kind === 'done' || this.state.kind === 'error' || this.state.kind === 'blocked' || this.state.kind === 'missing') this.emit({ kind: 'idle' }); }
  private releaseMedia(): void {
    clearInterval(this.tick); clearTimeout(this.cap);
    this.stream?.getTracks().forEach((track) => track.stop()); this.stream = null;
    void this.context?.close().catch(() => {}); this.context = null; this.recorder = null;
  }
}

/** Read the rendered markdown: emphasis markers and code-copy controls are not spoken. */
export function spokenText(element: HTMLElement): string {
  const copy = element.cloneNode(true) as HTMLElement;
  copy.querySelectorAll('button,script,style').forEach((node) => node.remove());
  copy.querySelectorAll('br').forEach((node) => node.replaceWith('\n'));
  copy.querySelectorAll('td,th').forEach((node) => node.append(' '));
  copy.querySelectorAll('p,li,pre,blockquote,h1,h2,h3,h4,h5,h6,tr').forEach((node) => node.append('\n'));
  return (copy.textContent ?? '').replace(/\n{3,}/g, '\n\n').trim();
}

export type SpeechState =
  | { kind: 'idle' }
  | { kind: 'making'; id: string; characters: number }
  | { kind: 'playing'; id: string; characters: number; position: number; duration: number }
  | { kind: 'error'; id: string; error: unknown };
// Tab memory only. Cache identity includes the host/key scope, audio model id and spoken text.
const speechCache = new Map<string, Blob>();
// Short local silence unlocks the same audio element inside the tap on Blob-only browsers.
const SILENT_AUDIO = 'data:audio/wav;base64,UklGRsQAAABXQVZFZm10IBAAAAABAAEAQB8AAIA+AAACABAAZGF0YaAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA';

function mediaEvent(target: EventTarget, event: string, signal: AbortSignal, action?: () => void): Promise<void> {
  return new Promise((resolve, reject) => {
    const cleanup = () => { target.removeEventListener(event, done); target.removeEventListener('error', fail); signal.removeEventListener('abort', abort); };
    const done = () => { cleanup(); resolve(); };
    const fail = () => { cleanup(); reject(new Error('The browser could not play the speech audio.')); };
    const abort = () => { cleanup(); reject(new DOMException('Stopped', 'AbortError')); };
    if (signal.aborted) { abort(); return; }
    target.addEventListener(event, done, { once: true }); target.addEventListener('error', fail, { once: true }); signal.addEventListener('abort', abort, { once: true });
    try { action?.(); } catch (error) { cleanup(); reject(error); }
  });
}

/** One player per chat view; its element is opened in the Listen gesture, before any await. */
export class VoicePlayer {
  state: SpeechState = { kind: 'idle' };
  private controller: AbortController | null = null;
  private element: HTMLAudioElement | null = null;
  private url: string | null = null;
  constructor(private readonly changed: (state: SpeechState) => void) {}
  private emit(state: SpeechState): void { this.state = state; this.changed(state); }
  stop(): void {
    this.controller?.abort(); this.controller = null;
    const element = this.element; this.element = null;
    if (element) {
      element.onended = element.onerror = element.onplaying = element.ontimeupdate = element.ondurationchange = null;
      element.pause(); element.removeAttribute('src'); element.load();
    }
    if (this.url) URL.revokeObjectURL(this.url); this.url = null;
    this.emit({ kind: 'idle' });
  }
  async play(id: string, scope: string, model: string, text: string, request: (signal: AbortSignal) => Promise<Response>): Promise<void> {
    this.stop();
    const key = JSON.stringify([scope, model, text]);
    const controller = new AbortController(); this.controller = controller;
    const signal = controller.signal;
    const element = new Audio(); this.element = element;
    element.disableRemotePlayback = true;
    element.setAttribute('playsinline', '');
    const characters = voiceCharacters(text);
    this.emit({ kind: 'making', id, characters });
    const fail = (error: unknown) => {
      if (signal.aborted) return;
      speechCache.delete(key); this.stop(); this.emit({ kind: 'error', id, error });
    };
    let ready = false;
    const update = () => {
      if (ready && !signal.aborted && !element.paused) this.emit({ kind: 'playing', id, characters, position: element.currentTime, duration: element.duration });
    };
    element.onplaying = element.ontimeupdate = element.ondurationchange = update;
    element.onended = () => { if (ready && !signal.aborted) this.stop(); };
    element.onerror = () => { if (ready) fail(new Error(element.error?.message || 'The browser could not play the speech audio.')); };
    const start = () => { void element.play().catch((error: unknown) => fail(error)); };
    try {
      const cached = speechCache.get(key);
      if (cached) { ready = true; this.url = URL.createObjectURL(cached); element.src = this.url; start(); return; }
      const sourceType = [(globalThis as typeof globalThis & { ManagedMediaSource?: typeof MediaSource }).ManagedMediaSource, globalThis.MediaSource].find((type) => type?.isTypeSupported('audio/mpeg'));
      const source = sourceType ? new sourceType() : null;
      if (source) {
        ready = true;
        // The source and play() are created inside the tap, before the authenticated request.
        await mediaEvent(source, 'sourceopen', signal, () => { this.url = URL.createObjectURL(source); element.src = this.url; start(); });
      } else {
        element.src = SILENT_AUDIO;
        void element.play().catch(() => {});
      }
      const response = await request(signal);
      signal.throwIfAborted();
      if (!response.body) throw new Error('The host returned no speech audio.');
      const buffer = source?.addSourceBuffer('audio/mpeg');
      const reader = response.body.getReader();
      const chunks: Uint8Array<ArrayBuffer>[] = [];
      const cancelReader = () => { void reader.cancel().catch(() => {}); };
      signal.addEventListener('abort', cancelReader, { once: true });
      try {
        for (;;) {
          const { value, done } = await reader.read();
          signal.throwIfAborted();
          if (done) break;
          const chunk = new Uint8Array(value); chunks.push(chunk);
          if (buffer) await mediaEvent(buffer, 'updateend', signal, () => buffer.appendBuffer(chunk));
        }
        if (!chunks.length) throw new Error('The host returned empty speech audio.');
        const blob = new Blob(chunks, { type: 'audio/mpeg' });
        speechCache.set(key, blob);
        if (source) source.endOfStream();
        else { ready = true; this.url = URL.createObjectURL(blob); element.src = this.url; start(); }
      } finally { signal.removeEventListener('abort', cancelReader); reader.releaseLock(); }
    } catch (error) { fail(error); }
  }
}
