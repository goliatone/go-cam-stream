"""Opt-in camera-free smoke for the actual preview-capable runtime tools."""
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
import unittest


@unittest.skipUnless(os.environ.get('CAMSTREAM_PREVIEW_RUNTIME'), 'set CAMSTREAM_PREVIEW_RUNTIME to verified tools directory')
class PreviewRuntimeTests(unittest.TestCase):
    def test_authenticated_h264_to_bounded_bgra(self):
        tools = Path(os.environ['CAMSTREAM_PREVIEW_RUNTIME'])
        with tempfile.TemporaryDirectory(prefix='camera-preview-runtime-') as temp:
            folder = Path(temp)
            with socket.socket() as sock:
                sock.bind(('127.0.0.1', 0))
                port = sock.getsockname()[1]
            password = secrets.token_hex(20)
            config = {'logLevel': 'info', 'logDestinations': ['stdout'],
                      **{key: False for key in ('api', 'metrics', 'pprof', 'playback', 'rtmp', 'hls', 'webrtc', 'srt', 'moq')},
                      'rtsp': True, 'rtspAddress': f'127.0.0.1:{port}', 'rtspTransports': ['tcp'],
                      'rtspAuthMethods': ['basic'], 'authMethod': 'internal',
                      'authInternalUsers': [
                          {'user': 'any', 'ips': ['127.0.0.1'], 'permissions': [{'action': 'publish', 'path': 'test'}]},
                          {'user': 'camera', 'pass': password, 'permissions': [{'action': 'read', 'path': 'test'}]}],
                      'paths': {'test': {'source': 'publisher', 'overridePublisher': False, 'record': False}}}
            conf = folder / 'server.json'
            conf.touch(mode=0o600)
            conf.write_text(json.dumps(config))
            children = []
            env = {'PATH': '/usr/bin:/bin:/usr/sbin:/sbin'}
            try:
                with (folder / 'server.log').open('w') as log:
                    server = subprocess.Popen([str(tools / 'mediamtx'), str(conf)], stdout=log, stderr=subprocess.STDOUT, env=env)
                    children.append(server)
                    def wait_for(text):
                        deadline = time.monotonic() + 10
                        while time.monotonic() < deadline:
                            if text in (folder / 'server.log').read_text():
                                return
                            self.assertIsNone(server.poll(), 'server exited')
                            time.sleep(.05)
                        self.fail('synthetic stream readiness timeout')
                    wait_for('[RTSP] started with listeners')
                    publisher = subprocess.Popen([str(tools / 'ffmpeg'), '-hide_banner', '-loglevel', 'error',
                        '-re', '-f', 'lavfi', '-i', 'testsrc2=size=1280x720:rate=30', '-an', '-c:v', 'libx264',
                        '-preset', 'ultrafast', '-tune', 'zerolatency', '-pix_fmt', 'yuv420p', '-g', '30',
                        '-f', 'rtsp', '-rtsp_transport', 'tcp', f'rtsp://127.0.0.1:{port}/test'],
                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env)
                    children.append(publisher)
                    wait_for('publishing to path')
                    for auth, accepted in [('', False), ('camera:wrong@', False), (f'camera:{password}@', True)]:
                        source = folder / 'reader.ffconcat'
                        source.touch(mode=0o600)
                        source.write_text(f"ffconcat version 1.0\nfile 'rtsp://{auth}127.0.0.1:{port}/test'\noption rtsp_transport tcp\noption rw_timeout 5000000\n")
                        result = subprocess.run([str(tools / 'ffmpeg'), '-hide_banner', '-loglevel', 'error',
                            '-protocol_whitelist', 'file,tcp,rtsp,rtp,udp', '-f', 'concat', '-safe', '0', '-i', str(source),
                            '-an', '-vf', 'fps=15,scale=640:360', '-frames:v', '3', '-pix_fmt', 'bgra',
                            '-c:v', 'rawvideo', '-f', 'rawvideo', 'pipe:1'], capture_output=True, timeout=20, env=env)
                        if accepted:
                            self.assertEqual(result.returncode, 0, 'authenticated H.264 decode failed; diagnostics omitted')
                            self.assertEqual(len(result.stdout), 3 * 640 * 360 * 4)
                            self.assertNotEqual(result.stdout[:640*360*4], result.stdout[640*360*4:2*640*360*4])
                        else:
                            self.assertNotEqual(result.returncode, 0)
                            self.assertIn(b'401 Unauthorized', result.stderr)
            finally:
                for child in reversed(children):
                    if child.poll() is None:
                        child.terminate()
                    try:
                        child.wait(timeout=5)
                    except subprocess.TimeoutExpired:
                        child.kill()
                        child.wait(timeout=3)
            with socket.socket() as sock:
                self.assertNotEqual(sock.connect_ex(('127.0.0.1', port)), 0)


if __name__ == '__main__':
    unittest.main()
