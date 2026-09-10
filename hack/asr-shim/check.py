"""Exercise the real running shim; pass an English WAV and a Mandarin WebM."""
import json
import sys
import urllib.error
import urllib.request
from pathlib import Path

base, english, mandarin = sys.argv[1:]
passed = 0

def call(path, body=None, headers=None):
    request = urllib.request.Request(base + path, data=body, headers=headers or {})
    try:
        response = urllib.request.urlopen(request, timeout=60)
    except urllib.error.HTTPError as error:
        response = error
    return response.status, json.load(response)

def upload(path, fmt):
    boundary = 'asr-shim-check'
    body = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="clip"\r\n\r\n'.encode()
            + Path(path).read_bytes()
            + f'\r\n--{boundary}\r\nContent-Disposition: form-data; name="response_format"\r\n\r\n{fmt}\r\n--{boundary}--\r\n'.encode())
    return call('/v1/audio/transcriptions', body, {'Content-Type': 'multipart/form-data; boundary=' + boundary})

status, models = call('/v1/models')
assert status == 200 and models['data'][0]['id'] == 'FunAudioLLM/Fun-ASR-Nano-2512-GGUF-Q8_0'
passed += 1
status, result = upload(english, 'json')
assert status == 200 and set(result) == {'text'} and result['text']
passed += 1
status, result = upload(mandarin, 'verbose_json')
assert status == 200 and result['text'] and 5 < result['duration'] < 6
passed += 1
assert call('/missing')[0] == 404
passed += 1
assert call('/v1/audio/transcriptions', b'')[0] == 413
passed += 1
assert call('/v1/audio/transcriptions', b'x', {'Content-Type': 'text/plain'})[0] == 400
passed += 1
assert upload(english, 'srt')[0] == 400
passed += 1
print(f'ASR shim: {passed} passed / 0 failed / 0 skipped')
