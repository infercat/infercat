#!/usr/bin/env python3
"""Loopback proof tool: stdlib HTTP/multipart -> ffmpeg -> resident pinned audio.cpp server."""
import argparse
import json
import subprocess
import tempfile
import threading
import time
import urllib.request
import wave
from email import policy
from email.parser import BytesParser
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--runtime-port', type=int, default=18084)
parser.add_argument('--port', type=int, default=18082)
args = parser.parse_args()
RUNTIME = 'http://127.0.0.1:' + str(args.runtime_port)
MODEL_ID = 'FunAudioLLM/Fun-ASR-Nano-2512-GGUF-Q8_0'
busy = threading.Lock()

class Handler(BaseHTTPRequestHandler):
    def reply(self, status, body):
        data = json.dumps(body, ensure_ascii=False).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(data)))
        if hasattr(self, 'timings'):
            self.send_header('Server-Timing', ', '.join(f'{key};dur={value:.3f}' for key, value in self.timings.items()))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path != '/v1/models':
            return self.reply(404, {'error': 'Not found'})
        try:
            with urllib.request.urlopen(RUNTIME + '/v1/models', timeout=3) as response:
                self.reply(200, json.load(response))
        except OSError:
            self.reply(502, {'error': 'ASR runtime is unavailable'})

    def do_POST(self):
        if self.path != '/v1/audio/transcriptions':
            return self.reply(404, {'error': 'Not found'})
        if not busy.acquire(blocking=False):
            return self.reply(429, {'error': 'ASR is busy'})
        try:
            started = time.perf_counter(); self.timings = {}
            self.connection.settimeout(30)
            size = int(self.headers.get('Content-Length', '0'))
            if not 0 < size <= 25 * 1024 * 1024:
                return self.reply(413, {'error': 'Expected a body of at most 25 MiB'})
            mime = ('Content-Type: ' + self.headers.get('Content-Type', '') + '\r\nMIME-Version: 1.0\r\n\r\n').encode()
            form = BytesParser(policy=policy.default).parsebytes(mime + self.rfile.read(size))
            parts = {p.get_param('name', header='content-disposition'): p.get_payload(decode=True) for p in form.iter_parts()}
            fmt = parts.get('response_format', b'json').decode()
            language = parts.get('language', b'auto').decode()
            if fmt not in ('json', 'verbose_json') or language not in ('auto', 'zh', 'en', 'ja') or not parts.get('file'):
                return self.reply(400, {'error': 'Expected file, json/verbose_json, and auto/zh/en/ja language'})
            self.timings['upload'] = (time.perf_counter() - started) * 1000
            with tempfile.TemporaryDirectory(prefix='asr-shim-') as directory:
                src, wav = (Path(directory) / name for name in ('upload', 'audio.wav'))
                started = time.perf_counter()
                src.write_bytes(parts['file'])
                subprocess.run(['ffmpeg', '-nostdin', '-v', 'error', '-protocol_whitelist', 'file,pipe', '-i', str(src), '-t', '301', '-ac', '1', '-ar', '16000', str(wav)], check=True, timeout=30, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
                with wave.open(str(wav)) as audio:
                    duration = audio.getnframes() / audio.getframerate()
                if duration > 300:
                    return self.reply(413, {'error': 'Audio exceeds 300 seconds'})
                self.timings['decode'] = (time.perf_counter() - started) * 1000
                started = time.perf_counter()
                request = urllib.request.Request(RUNTIME + '/v1/audio/transcriptions', data=json.dumps({'model': MODEL_ID, 'audio_path': str(wav), 'language': language}).encode(), headers={'Content-Type': 'application/json'})
                with urllib.request.urlopen(request, timeout=300) as response:
                    decoded = json.load(response)
                self.timings['runtime'] = (time.perf_counter() - started) * 1000
                self.timings['inference'] = decoded['timing']['wall_ms']
                result = {'text': decoded['text']}
                if fmt == 'verbose_json':
                    result['duration'] = duration
                self.reply(200, result)
        except (ValueError, OSError, subprocess.SubprocessError) as error:
            self.reply(502, {'error': 'Audio decode or ASR failed: ' + type(error).__name__})
        finally:
            busy.release()

ThreadingHTTPServer(('127.0.0.1', args.port), Handler).serve_forever()
