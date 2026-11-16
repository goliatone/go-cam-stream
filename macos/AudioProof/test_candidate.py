"""Opt-in sensor-free smoke for the T01 H.264/Opus candidate."""
import contextlib
import io
import json
from .observe import observe
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
import unittest


@unittest.skipUnless(os.environ.get('CAMSTREAM_AUDIO_PROOF_RUNTIME'), 'set CAMSTREAM_AUDIO_PROOF_RUNTIME to verified tools directory')
class AudioCandidateTests(unittest.TestCase):
    def test_authenticated_h264_opus_and_silence(self):
        tools = Path(os.environ['CAMSTREAM_AUDIO_PROOF_RUNTIME'])
        with tempfile.TemporaryDirectory(prefix='audio-candidate-runtime-') as temp:
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
                        '-re', '-f', 'lavfi', '-i', 'testsrc2=size=640x360:rate=30', '-re', '-f', 'lavfi', '-i',
                        'anullsrc=r=48000:cl=mono,aformat=sample_fmts=s16', '-map', '0:v:0', '-map', '1:a:0',
                        '-c:a', 'libopus', '-ar', '48000', '-ac', '1', '-b:a', '64k', '-frame_duration', '20', '-c:v', 'libx264',
                        '-preset', 'ultrafast', '-tune', 'zerolatency', '-pix_fmt', 'yuv420p', '-g', '30',
                        '-f', 'rtsp', '-rtsp_transport', 'tcp', f'rtsp://127.0.0.1:{port}/test'],
                        stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, env=env)
                    children.append(publisher)
                    wait_for('publishing to path')
                    self.assertIn('Opus', (folder / 'server.log').read_text())
                    for auth, accepted in [('', False), ('camera:wrong@', False), (f'camera:{password}@', True)]:
                        source = folder / 'reader.ffconcat'
                        source.touch(mode=0o600)
                        source.write_text(f"ffconcat version 1.0\nfile 'rtsp://{auth}127.0.0.1:{port}/test'\noption rtsp_transport tcp\noption rw_timeout 5000000\n")
                        result = subprocess.run([str(tools / 'ffmpeg'), '-hide_banner', '-loglevel', 'error',
                            '-protocol_whitelist', 'file,tcp,rtsp,rtp,udp', '-f', 'concat', '-safe', '0', '-i', str(source),
                            '-an', '-vf', 'scale=640:360', '-fps_mode', 'passthrough', '-frames:v', '3', '-pix_fmt', 'bgra',
                            '-c:v', 'rawvideo', '-f', 'rawvideo', 'pipe:1'], capture_output=True, timeout=20, env=env)
                        if accepted:
                            self.assertEqual(result.returncode, 0, 'authenticated H.264 decode failed; diagnostics omitted')
                            self.assertEqual(len(result.stdout), 3 * 640 * 360 * 4)
                            self.assertTrue(result.stdout[:640*360*4] != result.stdout[640*360*4:2*640*360*4], 'consecutive source video frames repeated')
                            audio = subprocess.run([str(tools / 'ffmpeg'), '-hide_banner', '-loglevel', 'error',
                                '-protocol_whitelist', 'file,tcp,rtsp,rtp,udp', '-f', 'concat', '-safe', '0', '-i', str(source),
                                '-map', '0:a:0', '-t', '2', '-ar', '48000', '-ac', '1', '-c:a', 'pcm_s16le',
                                '-f', 's16le', 'pipe:1'], capture_output=True, timeout=15, env=env)
                            self.assertEqual(audio.returncode, 0, 'Opus decode failed; diagnostics omitted')
                            self.assertGreaterEqual(len(audio.stdout), 48000 * 2)
                            self.assertEqual(set(audio.stdout), {0}, 'synthetic silent source decoded nonzero')
                            (folder / 'config.json').write_text(json.dumps({'port': port, 'path': 'test'}))
                            (folder / 'credentials.json').touch(mode=0o600)
                            (folder / 'credentials.json').write_text(json.dumps({'username': 'camera', 'password': password}))
                            metrics = io.StringIO()
                            with contextlib.redirect_stdout(metrics):
                                observe(tools, folder, 3)
                            result = json.loads(metrics.getvalue().splitlines()[-1])
                            self.assertGreater(result['packets'].get('audio', 0), 0)
                            self.assertGreater(result['packets'].get('video', 0), 0)
                            self.assertEqual(result['result'], 'duration')


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
