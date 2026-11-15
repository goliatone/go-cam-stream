"""Check credential transport without reading or uploading real secrets."""
from pathlib import Path
import runpy
import subprocess
import unittest
from unittest.mock import patch


provision = runpy.run_path(str(Path(__file__).with_name('provision-release-secrets.py')))


class ReleaseSecretTests(unittest.TestCase):
    def test_secret_goes_to_stdin_not_arguments(self):
        result = subprocess.CompletedProcess([], 0, b'', b'')
        with patch.object(subprocess, 'run', return_value=result) as run:
            provision['gh']('secret', 'set', 'TEST', value=b'private-value')
        self.assertEqual(run.call_args.args[0], ['gh', 'secret', 'set', 'TEST'])
        self.assertEqual(run.call_args.kwargs['input'], b'private-value')
        self.assertTrue(run.call_args.kwargs['capture_output'])

    def test_failure_does_not_echo_secret_or_tool_diagnostics(self):
        result = subprocess.CompletedProcess([], 1, b'private-value', b'private-value')
        with patch.object(subprocess, 'run', return_value=result):
            with self.assertRaises(ValueError) as error:
                provision['gh']('secret', 'set', 'TEST', value=b'private-value')
        self.assertNotIn('private-value', str(error.exception))


if __name__ == '__main__':
    unittest.main()
