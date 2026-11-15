"""Exercise release Git mutations only in disposable repos with a local bare remote."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]


class TaskfileReleaseTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='camstream-release-test-')
        self.addCleanup(self.temp.cleanup)
        self.base = Path(self.temp.name)
        self.repo = self.base / 'repo'
        self.remote = self.base / 'origin.git'
        self.repo.mkdir()
        self.bin = self.base / 'bin'
        self.bin.mkdir()
        self.env = {**os.environ, 'PATH': str(self.bin) + ':' + os.environ['PATH'],
                    'GIT_CONFIG_GLOBAL': '/dev/null', 'GIT_CONFIG_NOSYSTEM': '1',
                    'GIT_TERMINAL_PROMPT': '0'}
        self.run_cmd('git', 'init', '--bare', str(self.remote))
        self.git('init', '-b', 'main')
        self.git('config', 'user.name', 'Release Test')
        self.git('config', 'user.email', 'release-test@example.invalid')
        self.git('config', 'commit.gpgsign', 'false')
        shutil.copy(ROOT / 'taskfile', self.repo / 'taskfile')
        shutil.copy(ROOT / 'cliff.toml', self.repo / 'cliff.toml')
        (self.repo / '.version').write_text('0.1.3\n')
        (self.repo / '.gitignore').write_text('.release-notes.md\n')
        self.git('add', '.')
        self.git('commit', '-m', 'fixture')
        self.git('tag', 'v0.1.3')
        self.git('remote', 'add', 'origin', str(self.remote))
        self.git('push', 'origin', 'main', 'v0.1.3')
        self.original = self.git('rev-parse', 'HEAD').stdout.strip()
        # Substitute only the changelog formatter; Git/tag/push are real.
        cliff = self.bin / 'git-cliff'
        cliff.write_text('#!/bin/bash\nwhile [ "$#" -gt 0 ]; do\n'
                         ' if [ "$1" = --output ]; then shift; printf "# Changelog\\n" > "$1"; exit; fi\n'
                         ' shift\ndone\nexit 1\n')
        cliff.chmod(0o755)

    def run_cmd(self, *args, check=True, cwd=None):
        return subprocess.run(args, cwd=cwd or self.repo, env=self.env,
                              text=True, capture_output=True, check=check)

    def git(self, *args):
        return self.run_cmd('git', *args)

    def task(self, *args, ok=True):
        result = self.run_cmd('bash', str(self.repo / 'taskfile'), *args, check=False)
        self.assertEqual(result.returncode == 0, ok, result.stdout + result.stderr)
        return result

    def unchanged(self):
        self.assertEqual(self.git('rev-parse', 'HEAD').stdout.strip(), self.original)
        self.assertEqual(self.git('tag', '--list').stdout.strip(), 'v0.1.3')
        self.assertFalse((self.repo / 'CHANGELOG.md').exists())

    def test_all_bumps_and_dry_runs_leave_source_and_refs_unchanged(self):
        for level, expected in [('patch', '0.1.4'), ('minor', '0.2.0'), ('major', '1.0.0')]:
            self.assertEqual(self.task('version:bump', level).stdout.strip(), expected)
            self.assertIn(expected, self.task('release', level, '--dry-run').stdout)
            self.unchanged()
            self.assertEqual((self.repo / '.version').read_text(), '0.1.3\n')
            self.assertEqual(self.git('status', '--porcelain').stdout, '')

    def test_release_commit_and_annotated_tag_are_atomically_pushed(self):
        (self.repo / '.release-notes.md').write_text('Camera and server\n')
        self.task('release', 'patch')
        self.assertEqual((self.repo / '.version').read_text(), '0.1.4\n')
        self.assertEqual(self.git('show', 'v0.1.4:.version').stdout, '0.1.4\n')
        self.assertEqual(self.git('cat-file', '-t', 'v0.1.4').stdout.strip(), 'tag')
        sha = self.git('rev-parse', 'HEAD').stdout.strip()
        remote = self.git('ls-remote', 'origin', 'refs/heads/main', 'refs/tags/v0.1.4^{}').stdout
        self.assertEqual(remote.count(sha), 2)
        self.assertEqual(self.git('diff-tree', '--no-commit-id', '--name-only', '-r', 'HEAD').stdout.splitlines(),
                         ['.version', 'CHANGELOG.md'])
        self.assertFalse((self.repo / '.release-notes.md').exists())
        self.assertEqual(self.git('status', '--porcelain').stdout, '')

    def test_version_commands_use_checkout_version_from_another_directory(self):
        caller = self.base / 'caller'
        caller.mkdir()
        (caller / '.version').write_text('9.8.7\n')
        for args, expected in [(('version:get',), '0.1.3'),
                               (('version:bump', 'patch'), '0.1.4')]:
            result = self.run_cmd('bash', str(self.repo / 'taskfile'), *args, cwd=caller)
            self.assertEqual(result.stdout.strip(), expected)
        self.run_cmd('bash', str(self.repo / 'taskfile'), 'version:set', '0.1.4', cwd=caller)
        self.assertEqual((self.repo / '.version').read_text(), '0.1.4\n')
        self.assertEqual((caller / '.version').read_text(), '9.8.7\n')

    def test_relative_notes_use_callers_directory(self):
        caller = self.base / 'caller with spaces'
        caller.mkdir()
        notes = caller / 'notes.md'
        notes.write_text('Caller release notes\n')
        (self.bin / 'git-cliff').write_text(
            '#!/bin/bash\nwhile [ "$#" -gt 0 ]; do\n'
            ' case "$1" in\n'
            ' --with-tag-message) shift; message=$1 ;;\n'
            ' --output) shift; output=$1 ;;\n'
            ' esac\n shift\ndone\nprintf "%s\\n" "$message" > "$output"\n')
        self.run_cmd('bash', str(self.repo / 'taskfile'), 'release', 'patch',
                     '--notes', 'notes.md', cwd=caller)
        self.assertIn('Caller release notes', (self.repo / 'CHANGELOG.md').read_text())
        self.assertFalse(notes.exists())

    def test_notes_edited_during_release_are_preserved(self):
        notes = self.repo / '.release-notes.md'
        notes.write_text('Included notes\n')
        cliff = self.bin / 'git-cliff'
        cliff.write_text(cliff.read_text().replace(
            'while [', 'printf "New notes for next release\\n" > .release-notes.md\nwhile [', 1))
        self.task('release', 'patch')
        self.assertEqual(notes.read_text(), 'New notes for next release\n')

    def test_missing_explicit_notes_fail_before_release(self):
        self.assertIn('does not exist', self.task('release', 'patch', '--notes',
                                                 'missing.md', ok=False).stderr)
        self.unchanged()

    @unittest.skipUnless(shutil.which('git-cliff'), 'git-cliff not installed')
    def test_release_with_real_changelog_formatter(self):
        (self.bin / 'git-cliff').unlink()
        (self.repo / 'feature.txt').write_text('new release input\n')
        self.git('add', 'feature.txt')
        self.git('commit', '-m', 'feat: add camera capability')
        self.task('release', 'minor', '--no-release-notes')
        self.assertIn('0.2.0', (self.repo / 'CHANGELOG.md').read_text())
        self.assertIn('Add camera capability', (self.repo / 'CHANGELOG.md').read_text())
        self.assertEqual(self.git('show', 'v0.2.0:.version').stdout, '0.2.0\n')

    def test_dirty_tracked_and_untracked_inputs_are_rejected(self):
        for name in ['taskfile', 'untracked.txt']:
            path = self.repo / name
            old = path.read_bytes() if path.exists() else None
            with path.open('a') as stream:
                stream.write('\n# dirty\n')
            self.task('release', 'patch', ok=False)
            self.unchanged()
            if old is None:
                path.unlink()
            else:
                path.write_bytes(old)

    def test_invalid_and_missing_versions_do_not_initialize_or_rewrite(self):
        path = self.repo / '.version'
        for version in ['01.2.3', '1.2.3;false', '1.2', '1.100.0', '9999.99.99']:
            path.write_text(version + '\n')
            self.task('version:bump', 'patch', ok=False)
            self.assertEqual(path.read_text(), version + '\n')
        path.unlink()
        self.task('version:get', ok=False)
        self.assertFalse(path.exists())

    def test_wrong_branch_and_invalid_options_fail_without_changes(self):
        self.task('release', '--unknown', ok=False)
        self.task('release', 'minor', 'patch', ok=False)
        self.git('checkout', '-b', 'feature')
        self.task('release', 'patch', ok=False)
        self.unchanged()

    def test_newer_remote_tag_blocks_stale_version_baseline(self):
        self.run_cmd('git', '--git-dir', str(self.remote), 'tag', 'v0.2.0', self.original)
        self.task('release', 'patch', ok=False)
        self.unchanged()

    def test_remote_main_ahead_is_not_merged_or_released(self):
        self.git('commit', '--allow-empty', '-m', 'remote advance')
        self.git('push', 'origin', 'main')
        self.git('reset', '--hard', self.original)
        self.task('release', 'patch', ok=False)
        self.unchanged()

    def test_changelog_failure_preserves_inputs_and_notes(self):
        (self.bin / 'git-cliff').write_text('#!/bin/bash\nexit 1\n')
        (self.repo / '.release-notes.md').write_text('keep me\n')
        self.task('release', 'patch', ok=False)
        self.unchanged()
        self.assertEqual((self.repo / '.version').read_text(), '0.1.3\n')
        self.assertTrue((self.repo / '.release-notes.md').exists())

    def test_atomic_push_failure_preserves_recoverable_tag_without_bumping_again(self):
        hook = self.remote / 'hooks/pre-receive'
        hook.write_text('#!/bin/sh\nexit 1\n')
        hook.chmod(0o755)
        (self.repo / '.release-notes.md').write_text('keep until pushed\n')
        self.assertIn('local release commit/tag retained', self.task('release', 'patch', ok=False).stderr)
        self.assertTrue((self.repo / '.release-notes.md').exists())
        self.assertEqual(self.run_cmd('git', '--git-dir', str(self.remote), 'rev-parse', 'main').stdout.strip(), self.original)
        self.assertEqual(self.git('ls-remote', 'origin', 'refs/tags/v0.1.4').stdout, '')
        self.assertIn('Recover its atomic push', self.task('release', 'patch', ok=False).stderr)
        self.assertEqual((self.repo / '.version').read_text(), '0.1.4\n')


if __name__ == '__main__':
    unittest.main()
