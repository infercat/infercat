"""Same-payload loopback comparison; output raw times, never a rounded speed claim."""
import json
import sys
import time
import urllib.request
from pathlib import Path

english, mandarin, *urls = sys.argv[1:]
for base in urls:
    for language, path in [('en', english), ('zh', mandarin)]:
        boundary = 'asr-profile'
        body = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="clip"\r\n\r\n'.encode()
                + Path(path).read_bytes()
                + f'\r\n--{boundary}\r\nContent-Disposition: form-data; name="language"\r\n\r\n{language}\r\n--{boundary}--\r\n'.encode())
        for run in range(3):
            request = urllib.request.Request(base + '/v1/audio/transcriptions', data=body,
                                            headers={'Content-Type': 'multipart/form-data; boundary=' + boundary})
            start = time.perf_counter()
            with urllib.request.urlopen(request, timeout=60) as response:
                result = json.load(response)
                timings = {item.strip().split(';dur=')[0]: float(item.split(';dur=')[1])
                           for item in response.headers.get('Server-Timing', '').split(',') if ';dur=' in item}
            total = (time.perf_counter() - start) * 1000
            other = total - sum(timings.get(key, 0) for key in ('upload', 'decode', 'runtime'))
            print(json.dumps({'url': base, 'language': language, 'run': run, 'total_ms': round(total, 3),
                              'response_transport_other_ms': round(other, 3), 'stages_ms': timings, 'result': result},
                             ensure_ascii=False), flush=True)
