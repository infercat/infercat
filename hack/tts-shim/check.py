#!/usr/bin/env python3
"""Live checks against the isolated adapter and real runtime; synthetic text only."""
import concurrent.futures, json, os, pathlib, subprocess, sys, tempfile, time, urllib.error, urllib.request
base = sys.argv[1] if len(sys.argv) > 1 else 'http://127.0.0.1:18088'
passed = 0

def request(path, data=None):
    req = urllib.request.Request(base + path, data=data, headers={'Content-Type': 'application/json'})
    try:
        with urllib.request.urlopen(req, timeout=120) as r: return r.status, r.headers.get('Content-Type'), r.read()
    except urllib.error.HTTPError as r: return r.code, r.headers.get('Content-Type'), r.read()
def check(ok):
    global passed
    assert ok
    passed += 1
status, kind, body = request('/v1/models')
check(status == 200 and '1.7B' in json.loads(body)['data'][0]['id'])
for data, expected in [(b'{',400),(b'{"input":""}',400),(json.dumps({'input':'hello','response_format':'pcm'}).encode(),400),(b'x'*16385,413)]:
    check(request('/v1/audio/speech',data)[0] == expected)
for fmt in ['wav','mp3']:
    status, kind, audio = request('/v1/audio/speech',json.dumps({'input':'你好，欢迎使用语音。','response_format':fmt}).encode())
    check(status == 200 and kind == ('audio/wav' if fmt == 'wav' else 'audio/mpeg'))
    decoded = subprocess.run(['ffmpeg','-v','error','-i','pipe:0','-f','null','-'],input=audio,capture_output=True)
    check(decoded.returncode == 0 and len(audio) > 100)
with concurrent.futures.ThreadPoolExecutor(max_workers=2) as pool:
    first = pool.submit(request,'/v1/audio/speech',json.dumps({'input':'今天我们测试这台电脑上的语音合成。请仔细听每个词的发音，以及句子之间自然的停顿。'}).encode())
    time.sleep(.15)
    check(request('/v1/audio/speech',b'{"input":"second"}')[0] == 429)
    check(first.result()[0] == 200)
with tempfile.TemporaryDirectory() as directory:
    root = pathlib.Path(directory); pid_file = root / 'child.pid'; child = root / 'runtime'
    child.write_text('#!' + sys.executable + '\nimport os,time\nopen(' + repr(str(pid_file)) + ',"w").write(str(os.getpid()))\ntime.sleep(60)\n')
    child.chmod(0o700)
    config = root / 'runtime.json'; config.write_text(json.dumps({'host':'127.0.0.1','port':1,'models':[{'id':'test'}]}))
    owned = subprocess.Popen([sys.executable,str(pathlib.Path(__file__).with_name('server.py')),'--config',str(config),'--runtime',str(child),'--port','0'])
    try:
        for _ in range(100):
            if pid_file.exists(): break
            time.sleep(.02)
        pid = int(pid_file.read_text()); owned.terminate(); owned.wait(timeout=10)
        try: os.kill(pid,0); alive = True
        except ProcessLookupError: alive = False
        check(not alive and owned.returncode == 0)
    finally:
        if owned.poll() is None: owned.terminate(); owned.wait(timeout=10)
print(f'TTS adapter: {passed} passed / 0 failed / 0 skipped')
