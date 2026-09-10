#!/usr/bin/env python3
"""Loopback Qwen speech proof: resident audio.cpp WAV -> requested MP3. No ML packages."""
import argparse, json, signal, subprocess, threading, urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--config', required=True, help='audio.cpp config (one model, loopback)')
parser.add_argument('--runtime', help='optional owned audiocpp_server executable')
parser.add_argument('--port', type=int, default=18088)
args = parser.parse_args()
with open(args.config) as f:
    config = json.load(f)
assert config['host'] == '127.0.0.1' and len(config['models']) == 1
model = config['models'][0]['id']
upstream = 'http://127.0.0.1:' + str(config['port'])
busy = threading.Lock()

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *unused): pass
    def send(self, status, body, kind='application/json'):
        self.send_response(status)
        self.send_header('Content-Type', kind)
        self.send_header('Content-Length', str(len(body)))
        self.send_header('Connection', 'close')
        self.end_headers()
        self.close_connection = True
        self.wfile.write(body)
    def fail(self, status, message):
        self.send(status, json.dumps({'error': {'message': message}}).encode())
    def do_GET(self):
        if self.path != '/v1/models': return self.fail(404, 'Not found')
        try:
            with urllib.request.urlopen(upstream + '/v1/models', timeout=3) as r: r.read(65536)
            self.send(200, json.dumps({'object': 'list', 'data': [{'id': model, 'object': 'model'}]}).encode())
        except Exception: self.fail(503, 'Speech runtime is unavailable')
    def do_POST(self):
        if self.path != '/v1/audio/speech': return self.fail(404, 'Not found')
        if not busy.acquire(blocking=False): return self.fail(429, 'Speech runtime is busy')
        try:
            self.connection.settimeout(10)
            n = int(self.headers.get('Content-Length', '0'))
            if self.headers.get('Transfer-Encoding') or not 0 < n <= 16384:
                return self.fail(413, 'Use a JSON body of at most 16 KiB')
            data = json.loads(self.rfile.read(n))
            if not isinstance(data, dict) or not isinstance(data.get('input'), str) or not data['input'].strip():
                return self.fail(400, 'input must be nonempty text')
            fmt = data.get('response_format', 'mp3')
            if fmt not in ('mp3', 'wav'): return self.fail(400, 'Supported formats: mp3, wav')
            payload = {'model': model, 'input': data['input'], 'response_format': 'wav'}
            if 'voice' in data: payload['voice'] = data['voice']
            body = json.dumps(payload).encode()
            request = urllib.request.Request(upstream + '/v1/audio/speech', data=body, headers={'Content-Type': 'application/json'})
            with urllib.request.urlopen(request, timeout=300) as response: audio = response.read(32 * 1024 * 1024 + 1)
            if len(audio) > 32 * 1024 * 1024: return self.fail(502, 'Speech response exceeds 32 MiB')
            if fmt == 'mp3':
                audio = subprocess.run(['ffmpeg', '-v', 'error', '-i', 'pipe:0', '-f', 'mp3', '-codec:a', 'libmp3lame', '-b:a', '96k', 'pipe:1'], input=audio, capture_output=True, timeout=30, check=True).stdout
            self.send(200, audio, 'audio/mpeg' if fmt == 'mp3' else 'audio/wav')
        except (ValueError, UnicodeError): self.fail(400, 'Invalid JSON request')
        except Exception: self.fail(502, 'Speech generation failed')
        finally: busy.release()

def stop(*unused): raise KeyboardInterrupt
signal.signal(signal.SIGTERM, stop)
child = subprocess.Popen([args.runtime, '--config', args.config]) if args.runtime else None
try:
    with ThreadingHTTPServer(('127.0.0.1', args.port), Handler) as server: server.serve_forever()
except KeyboardInterrupt: pass
finally:
    if child:
        child.terminate()
        try: child.wait(timeout=5)
        except subprocess.TimeoutExpired: child.kill(); child.wait()
