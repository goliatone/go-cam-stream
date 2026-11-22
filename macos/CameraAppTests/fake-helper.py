#!/usr/bin/python3
"""Camera-free NDJSON peer for native transport failure/EOF tests only."""
import json
import os
from pathlib import Path
import signal
import sys
import subprocess
import time

state = Path(sys.argv[-1])
mode = state.name
if mode == 'startup-busy':
    print(json.dumps({'version': 1, 'event': 'session_error', 'error': {'code': 'busy'}}), flush=True)
    sys.exit(71)
if mode == 'startup-settings':
    sys.exit(72)
if mode == 'startup-runtime':
    sys.exit(70)
fixtures = json.loads((Path(__file__).parent.parent / 'fixtures/audio-protocol.json').read_text())
ready = fixtures['ready'] if mode == 'audio-contract' else fixtures['legacy_ready']
if mode == 'future-audio-contract':
    ready['result']['audio'] = 2
if mode == 'owner-pipe':
    ready['result']['sharing'] = 1
if mode == 'closed-input':
    os.close(0)
    (state / 'closed-input-pid').write_text(str(os.getpid()))
print(json.dumps(ready), flush=True)
if mode in ('closed-input', 'backpressured'):
    time.sleep(4)
    sys.exit(0)
if mode == 'hung':
    # A suspended helper can neither process EOF nor handle SIGTERM.
    os.kill(os.getpid(), signal.SIGSTOP)
if mode == 'eof':
    sys.exit(0)
if mode == 'exit-before-eof':
    # Keep stdout open briefly after the direct helper exits, making process
    # termination precede pipe EOF without relying on scheduler luck.
    subprocess.Popen([sys.executable, '-c', 'import time; time.sleep(0.5)'],
                     stdin=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    sys.exit(0)
for line in sys.stdin:
    request = json.loads(line)
    if mode in ('audio-contract', 'legacy-contract', 'future-audio-contract'):
        if request['method'] == 'configure':
            config = request['config']
            if mode != 'audio-contract' and ('audio_enabled' in config or 'audio_device_id' in config):
                sys.exit(90)
            (state / 'received-config.json').write_text(json.dumps(config))
            result = config
        elif request['method'] == 'audio_devices':
            if mode != 'audio-contract':
                sys.exit(91)
            result = fixtures['discovery_reply']['result']
        else:
            result = {'state': 'stopped'}
        print(json.dumps({'version': 1, 'id': request['id'], 'result': result}), flush=True)
    elif mode == 'bad-version':
        print(json.dumps({'version': 2}), flush=True)
    elif mode == 'wrong-id':
        print(json.dumps({'version': 1, 'id': 'other', 'result': {}}), flush=True)
    elif mode == 'oversized':
        print('x' * 300000, flush=True)
    else:
        print(json.dumps({'version': 1, 'id': request['id'], 'result': {'state': 'stopped'}}), flush=True)
# This final write must not fail with BrokenPipe before cleanup on normal Quit.
print(json.dumps({'version': 1, 'event': 'capture_stopped', 'result': {'state': 'stopped'}}), flush=True)
(state / 'cleaned').write_text('done')
