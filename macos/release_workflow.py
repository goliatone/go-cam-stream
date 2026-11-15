"""Resolve release events and bind publication to the verified draft assets."""
import hashlib
import json
import os
from pathlib import Path
import re
import runpy
import sys

validate_version = runpy.run_path(str(Path(__file__).with_name('build-proof.py')))['validate_version']


def plan(event, ref, version='', distribution=False, draft=False):
    if event == 'push':
        if not ref.startswith('refs/tags/v'):
            raise ValueError('release push must be a version tag')
        version, distribution, draft = ref[len('refs/tags/v'):], True, True
    elif event != 'workflow_dispatch':
        raise ValueError('unsupported release event')
    validate_version(version)
    if draft and not distribution:
        raise ValueError('draft creation requires signed distribution')
    if distribution and ref != 'refs/tags/v' + version:
        raise ValueError('signed distribution must run on its exact version tag')
    return {'version': version, 'distribution': str(distribution).lower(),
            'draft': str(draft).lower(), 'publish': str(event == 'push').lower()}


def draft_snapshot(release, version, commit):
    validate_version(version)
    if (release.get('draft') is not True or release.get('tag_name') != 'v' + version
            or release.get('target_commitish') != commit or not re.fullmatch(r'[0-9a-f]{40}', commit)
            or not isinstance(release.get('id'), int)):
        raise ValueError('release must be the expected draft at the verified source commit')
    names = {f'camstream-camera-{version}-darwin-arm64.dmg', 'camera-release.json',
             'camera-SHA256SUMS', f'camstream-{version}-linux-arm64.tar.gz',
             f'camstream-{version}-linux-arm64.tar.gz.sha256'}
    assets = release.get('assets', [])
    if len(assets) != len(names) or {a['name'] for a in assets} != names:
        raise ValueError('draft asset set differs from the validated build')
    inventory = []
    for asset in assets:
        if (asset.get('state') != 'uploaded' or asset.get('size', 0) <= 0
                or not re.fullmatch(r'sha256:[0-9a-f]{64}', asset.get('digest') or '')):
            raise ValueError('draft asset is incomplete or lacks a digest')
        inventory.append({k: asset[k] for k in ('id', 'name', 'size', 'digest')})
    fingerprint = hashlib.sha256(json.dumps(sorted(inventory, key=lambda a: a['name']), sort_keys=True).encode()).hexdigest()
    return {'release_id': str(release['id']), 'fingerprint': fingerprint, 'url': release['html_url']}


def main():
    if sys.argv[1] == 'plan':
        output = plan(os.environ['GITHUB_EVENT_NAME'], os.environ['GITHUB_REF'],
                      os.environ.get('INPUT_VERSION', ''), os.environ.get('INPUT_DISTRIBUTION') == 'true',
                      os.environ.get('INPUT_DRAFT') == 'true')
    else:
        output = draft_snapshot(json.loads(Path(sys.argv[2]).read_text()),
                                os.environ['RELEASE_VERSION'], os.environ['RELEASE_SHA'])
        if sys.argv[1] == 'verify':
            if (output['release_id'] != os.environ['EXPECTED_RELEASE_ID']
                    or output['fingerprint'] != os.environ['EXPECTED_FINGERPRINT']):
                raise ValueError('draft assets changed while awaiting approval')
            return
        if sys.argv[1] != 'snapshot':
            raise ValueError('unsupported action')
    with open(os.environ['GITHUB_OUTPUT'], 'a') as stream:
        for key, value in output.items():
            if '\n' in value or '\r' in value:
                raise ValueError('invalid output')
            stream.write(f'{key}={value}\n')


if __name__ == '__main__':
    main()
