#!/usr/bin/env python3
"""Interactively provision hosted signing secrets; values never enter argv or logs."""
import argparse
import base64
import getpass
import json
from pathlib import Path
import subprocess
import sys


def gh(*args, value=None):
    result = subprocess.run(['gh', *args], input=value, capture_output=True)
    if result.returncode:
        # Never include input, command diagnostics or secrets in errors.
        raise ValueError('GitHub operation failed; check gh authentication and repository admin access')
    return result.stdout


def check_environments(signing, branches, publication):
    policy = signing.get('deployment_branch_policy') or {}
    rules = branches.get('branch_policies', [])
    if (policy.get('custom_branch_policies') is not True or policy.get('protected_branches') is not False
            or len(rules) != 1 or rules[0].get('type') != 'tag' or rules[0].get('name') != 'v*'):
        raise ValueError('camera-release must permit only v* tags before uploading signing secrets')
    if not any(rule.get('type') == 'required_reviewers' and rule.get('reviewers')
               for rule in publication.get('protection_rules', [])):
        raise ValueError('configure required reviewers on release-publish before uploading signing secrets')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--certificate', required=True, type=Path, help='exported Developer ID Application certificate AND private key (.p12)')
    parser.add_argument('--repo', default='goliatone/go-cam-stream')
    args = parser.parse_args()
    if not sys.stdin.isatty():
        parser.error('run interactively in Terminal; do not pass passwords in arguments or chat')
    cert = args.certificate.expanduser().read_bytes()
    if not cert or len(cert) > 32000:
        parser.error('expected a single .p12 signing identity (1–32000 bytes)')
    environment = json.loads(gh('api', f'repos/{args.repo}/environments/camera-release'))
    branches = json.loads(gh('api', f'repos/{args.repo}/environments/camera-release/deployment-branch-policies'))
    publication = json.loads(gh('api', f'repos/{args.repo}/environments/release-publish'))
    check_environments(environment, branches, publication)
    print(f'Provisioning four encrypted secrets in {args.repo} / camera-release.')
    print('Use the exported .p12 password and an Apple app-specific password, not your Apple account password.')
    password = getpass.getpass('Exported .p12 password: ')
    apple_id = input('Apple Account email: ').strip()
    app_password = getpass.getpass('Apple app-specific password: ')
    if not all((password, apple_id, app_password)):
        parser.error('all credentials are required; nothing was uploaded')
    values = {
        'CAMERA_CERTIFICATE_P12_BASE64': base64.b64encode(cert),
        'CAMERA_CERTIFICATE_PASSWORD': password.encode(),
        'CAMERA_APPLE_ID': apple_id.encode(),
        'CAMERA_APP_PASSWORD': app_password.encode(),
    }
    for name, value in values.items():
        gh('secret', 'set', name, '--repo', args.repo, '--env', 'camera-release', value=value)
        print('Set ' + name)
    print('Provisioning complete. Verify secret names with gh secret list --env camera-release.')
    print('The exported .p12 remains at its original path; keep it outside the repository.')


if __name__ == '__main__':
    try:
        main()
    except (OSError, ValueError) as error:
        raise SystemExit(str(error)) from None
