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
    if not any(rule.get('type') == 'required_reviewers' and rule.get('reviewers')
               for rule in environment.get('protection_rules', [])):
        parser.error('configure required reviewers on camera-release before uploading signing secrets')
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
