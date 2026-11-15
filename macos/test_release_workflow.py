"""Release event admission and approval-to-draft binding; no GitHub mutations."""
import copy
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

from release_workflow import plan, draft_snapshot


class ReleaseWorkflowTests(unittest.TestCase):
    def test_tag_push_selects_signed_draft_and_publication_gate(self):
        self.assertEqual(plan('push', 'refs/tags/v1.2.3'),
                         {'version': '1.2.3', 'distribution': 'true', 'draft': 'true', 'publish': 'true'})

    def test_manual_builds_never_select_publication(self):
        self.assertEqual(plan('workflow_dispatch', 'refs/heads/main', '1.2.3')['distribution'], 'false')
        self.assertEqual(plan('workflow_dispatch', 'refs/tags/v1.2.3', '1.2.3', True, True)['publish'], 'false')

    def test_invalid_events_versions_and_mismatched_refs_fail_before_signing(self):
        for args in [('push', 'refs/heads/main'), ('push', 'refs/tags/v1.2.3-rc.1'),
                     ('push', 'refs/tags/v1.2.3;echo bad'), ('push', 'refs/tags/v01.2.3'),
                     ('pull_request', 'refs/tags/v1.2.3'),
                     ('workflow_dispatch', 'refs/heads/main', '1.2.3', True, True),
                     ('workflow_dispatch', 'refs/tags/v1.2.4', '1.2.3', True, True),
                     ('workflow_dispatch', 'refs/heads/main', '1.2.3', False, True)]:
            with self.subTest(args=args), self.assertRaises(ValueError):
                plan(*args)

    def release(self):
        names = ['camstream-camera-1.2.3-darwin-arm64.dmg', 'camera-release.json',
                 'camera-SHA256SUMS', 'camstream-1.2.3-linux-arm64.tar.gz',
                 'camstream-1.2.3-linux-arm64.tar.gz.sha256']
        return {'id': 100, 'draft': True, 'tag_name': 'v1.2.3', 'target_commitish': 'a' * 40,
                'html_url': 'https://github.com/example/repo/releases/tag/v1.2.3',
                'assets': [dict(id=i, name=name, size=100, state='uploaded', digest='sha256:' + 'b' * 64)
                           for i, name in enumerate(names)]}

    def test_real_cli_outputs_and_changed_draft_refusal(self):
        script = str(Path(__file__).with_name('release_workflow.py'))
        with tempfile.TemporaryDirectory() as temp:
            output = Path(temp) / 'output'
            data = Path(temp) / 'draft.json'
            (Path(temp) / '.version').write_text('1.2.3\n')
            data.write_text(json.dumps(self.release()))
            env = {**os.environ, 'GITHUB_OUTPUT': str(output), 'GITHUB_EVENT_NAME': 'push',
                   'GITHUB_REF': 'refs/tags/v1.2.3', 'RELEASE_VERSION': '1.2.3', 'RELEASE_SHA': 'a' * 40}
            subprocess.run([sys.executable, script, 'plan'], cwd=temp, env=env, check=True)
            self.assertIn('publish=true', output.read_text())
            for version in ['1.2.4\n', '01.2.3\n']:
                (Path(temp) / '.version').write_text(version)
                result = subprocess.run([sys.executable, script, 'plan'], cwd=temp,
                                        env=env, capture_output=True)
                self.assertNotEqual(result.returncode, 0)
            subprocess.run([sys.executable, script, 'snapshot', str(data)], env=env, check=True)
            values = dict(line.split('=', 1) for line in output.read_text().splitlines())
            env.update(EXPECTED_RELEASE_ID=values['release_id'], EXPECTED_FINGERPRINT=values['fingerprint'])
            subprocess.run([sys.executable, script, 'verify', str(data)], env=env, check=True)
            changed = self.release()
            changed['assets'][0]['id'] = 900
            data.write_text(json.dumps(changed))
            result = subprocess.run([sys.executable, script, 'verify', str(data)], env=env, capture_output=True)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(b'changed while awaiting approval', result.stderr)

    def test_asset_order_and_release_notes_do_not_change_approval_binding(self):
        release = self.release()
        expected = draft_snapshot(release, '1.2.3', 'a' * 40)
        release['assets'].reverse()
        release['body'] = 'Reviewed release notes'
        self.assertEqual(expected, draft_snapshot(release, '1.2.3', 'a' * 40))

    def test_replacement_asset_changes_approval_binding(self):
        release = self.release()
        expected = draft_snapshot(release, '1.2.3', 'a' * 40)['fingerprint']
        for field, value in [('digest', 'sha256:' + 'c' * 64), ('size', 101), ('id', 999)]:
            modified = copy.deepcopy(release)
            modified['assets'][0][field] = value
            self.assertNotEqual(expected, draft_snapshot(modified, '1.2.3', 'a' * 40)['fingerprint'])

    def test_wrong_source_tag_public_release_and_partial_assets_rejected(self):
        for field, value in [('draft', False), ('target_commitish', 'main'), ('tag_name', 'v1.2.4')]:
            release = self.release()
            release[field] = value
            with self.assertRaises(ValueError):
                draft_snapshot(release, '1.2.3', 'a' * 40)
        for change in ['missing', 'extra', 'digest', 'state']:
            release = self.release()
            if change == 'missing':
                release['assets'].pop()
            elif change == 'extra':
                release['assets'].append(release['assets'][0])
            else:
                release['assets'][0][change] = ''
            with self.assertRaises(ValueError):
                draft_snapshot(release, '1.2.3', 'a' * 40)


if __name__ == '__main__':
    unittest.main()
