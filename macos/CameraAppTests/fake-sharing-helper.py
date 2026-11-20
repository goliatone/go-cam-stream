#!/usr/bin/python3
"""Camera-free NDJSON peer for native sharing tests only.

Replays the published data/api/sharing_examples.json replies and exits with a
distinct status when a request differs from its published (Go-validated)
request shape. Every received line is appended to requests.jsonl in the state
directory so tests can check where write-only credentials travel.
"""
import json
from pathlib import Path
import sys

state = Path(sys.argv[-1])
mode = state.name
root = Path(__file__).resolve().parent.parent.parent
examples = json.loads((root / 'data/api/sharing_examples.json').read_text())
log = (state / 'requests.jsonl').open('a')


def send(value):
    print(json.dumps(value), flush=True)


def reply(request, result=None, error=None):
    body = {'version': 1, 'id': request['id']}
    if error is None:
        body['result'] = result
    else:
        body['error'] = error
    send(body)


def shape(value):
    return {key: item for key, item in value.items() if key != 'id'}


ready = json.loads(json.dumps(examples['native_ready']))
if mode in ('legacy', 'unexpected-event'):
    del ready['result']['sharing']
send(ready)
if mode == 'unexpected-event':
    # A helper that did not advertise sharing must not deliver sharing events.
    send(examples['native_changed'])

created = examples['created']
share_id = created['share']['share_id']
stopped = {'camera_id': 'local-camera', 'state': 'stopped', 'media': 'idle', 'viewers': 0, 'viewer_limit': 0}
setup = examples['setup']
relay_tests = 0
for line in sys.stdin:
    log.write(line)
    log.flush()
    request = json.loads(line)
    method = request.get('method', '')
    if mode == 'legacy' and method.startswith('sharing_'):
        sys.exit(93)
    if method == 'sharing_setup':
        if shape(request) != shape(examples['native_setup']):
            sys.exit(94)
        reply(request, setup)
    elif method == 'sharing_status':
        if request.get('sharing') != {'camera_id': 'local-camera'}:
            sys.exit(95)
        reply(request, stopped)
    elif method == 'sharing_configure':
        if shape(request) != shape(examples['native_configure']):
            reply(request, error={'code': 'invalid_request', 'message': 'PRIVATE-DETAIL', 'retryable': False})
            continue
        # A new configuration has not passed Test Connection yet.
        setup = dict(examples['native_configured']['result'], relay_state='setup_needed')
        reply(request, setup)
    elif method == 'sharing_test_relay':
        if shape(request) != shape(examples['native_relay_test']):
            sys.exit(96)
        relay_tests += 1
        if relay_tests == 1:
            reply(request, error=examples['native_relay_failure']['error'])
        else:
            setup = dict(setup, relay_state='ready')
            reply(request, setup)
    elif method == 'sharing_create':
        if shape(request) != shape(examples['create']):
            sys.exit(97)
        reply(request, created)
        changed = json.loads(json.dumps(examples['native_changed']))
        changed['result'].update(viewers=1, media='live')
        send(changed)
    elif method == 'sharing_copy_link':
        if request.get('sharing') != {'share_id': share_id}:
            sys.exit(98)
        reply(request, created['link'])
    elif method == 'sharing_revoke':
        if request.get('sharing') != {'share_id': share_id}:
            sys.exit(99)
        reply(request, dict(created['share'], state='revoked', media='idle', viewers=0))
        send({'version': 1, 'event': 'sharing_changed', 'result': stopped})
    else:
        reply(request, error={'code': 'not_configured', 'message': 'fixture'})
