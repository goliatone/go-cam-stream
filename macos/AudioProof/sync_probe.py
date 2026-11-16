#!/usr/bin/env python3
"""In-memory physical clap measurement; never writes samples or images to disk."""
import argparse, array, base64, binascii, http.server, json, math, os, re, secrets, struct, subprocess, tempfile, threading, time, zlib
from pathlib import Path


def png(rgb, width=320, height=180):
    def chunk(kind, data):
        return struct.pack('!I', len(data)) + kind + data + struct.pack('!I', binascii.crc32(kind + data) & 0xffffffff)
    rows = b''.join(b'\0' + rgb[i:i+width*3] for i in range(0, len(rgb), width*3))
    return b'\x89PNG\r\n\x1a\n' + chunk(b'IHDR', struct.pack('!2I5B', width, height, 8, 2, 0, 0, 0)) + chunk(b'IDAT', zlib.compress(rows)) + chunk(b'IEND', b'')


def capture(tools, state, seconds):
    c = json.loads((state / 'camera.json').read_text())
    s = json.loads((state / 'credentials.json').read_text())
    frames, video_pts, audio_meta = [], [], []
    pcm = bytearray()
    r, w = os.pipe()
    with tempfile.TemporaryDirectory(prefix='audio-sync-proof-') as tmp:
        source = Path(tmp) / 'source.ffconcat'
        source.touch(mode=0o600)
        source.write_text(f"ffconcat version 1.0\nfile 'rtsp://{s['username']}:{s['password']}@127.0.0.1:{c['port']}/{c['path']}'\noption rtsp_transport tcp\noption rw_timeout 5000000\noption analyzeduration 500000\n")
        args = [str(tools / 'ffmpeg-real'), '-nostdin', '-hide_banner', '-loglevel', 'info', '-copyts',
                '-protocol_whitelist', 'file,tcp,rtsp,rtp,udp', '-f', 'concat', '-safe', '0', '-i', str(source),
                '-map', '0:v:0', '-t', str(seconds), '-vf', 'scale=320:180,format=rgb24,showinfo',
                '-fps_mode', 'passthrough', '-c:v', 'rawvideo', '-threads:v', '1', '-f', 'rawvideo', 'pipe:1',
                '-map', '0:a:0', '-t', str(seconds), '-af', 'aformat=sample_fmts=s16:sample_rates=48000:channel_layouts=mono,ashowinfo',
                '-c:a', 'pcm_s16le', '-f', 's16le', f'pipe:{w}']
        child = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, pass_fds=(w,))
        os.close(w)
        def read_video():
            while True:
                frame = child.stdout.read(320*180*3)
                if not frame: break
                if len(frame) == 320*180*3 and len(frames) < 1000: frames.append(frame)
        def read_audio():
            with os.fdopen(r, 'rb') as inp:
                while True:
                    block = inp.read(8192)
                    if not block: break
                    if len(pcm) < 4000000: pcm.extend(block)
        def read_timing():
            ready = False
            for raw in child.stderr:
                line = raw.decode(errors='replace')
                if 'ashowinfo' in line:
                    m = re.search(r'pts_time:([-\d.]+).*nb_samples:(\d+)', line)
                    if m: audio_meta.append((float(m[1]), int(m[2])))
                elif 'showinfo' in line:
                    m = re.search(r'\bn:\s*\d+\s+pts:.*?pts_time:([-\d.]+)', line)
                    if m: video_pts.append(float(m[1]))
                if not ready and video_pts and audio_meta:
                    print('READY: clap once visibly in front of the camera now.', flush=True); ready = True
        threads = [threading.Thread(target=f, daemon=True) for f in (read_video, read_audio, read_timing)]
        for thread in threads: thread.start()
        try: child.wait(timeout=seconds+15)
        finally:
            if child.poll() is None: child.kill(); child.wait()
            for thread in threads: thread.join(timeout=3)
            child.stdout.close(); child.stderr.close()
        if child.returncode != 0 or not frames or not audio_meta:
            raise RuntimeError('bounded decode failed; diagnostics omitted to protect credentials')
    values = array.array('h', pcm); del pcm
    peak, onset, offset = 0, None, 0
    # First find the loudest received acoustic impulse; all calculations remain in memory.
    for pts, count in audio_meta:
        for j in range(min(count, len(values)-offset)):
            amplitude = abs(values[offset+j])
            if amplitude > peak: peak = amplitude; onset = pts + j/48000
        offset += count
    if not peak: raise RuntimeError('no acoustic event detected')
    # Locate the first strong edge within 50 ms before that impulse peak.
    edge, offset = onset, 0
    for pts, count in audio_meta:
        for j in range(min(count, len(values)-offset)):
            stamp = pts+j/48000
            if onset-.05 <= stamp <= onset and abs(values[offset+j]) >= peak*.4:
                edge = min(edge, stamp)
        offset += count
    del values
    pairs = list(zip(video_pts, frames)); del frames
    selected = [(i, pts, image) for i, (pts, image) in enumerate(pairs) if abs(pts-edge) <= .5]
    # Only the temporary comparison window remains. No audio payload is served.
    pictures = [{'index': i, 'pts': pts, 'image': base64.b64encode(png(frame)).decode()} for i, pts, frame in selected]
    del pairs
    return {'audio_onset_s': edge, 'audio_peak_s': onset, 'peak_dbfs': 20*math.log10(peak/32768),
            'video_frames': len(video_pts), 'audio_blocks': len(audio_meta), 'frames': pictures}


def serve(result, port):
    token = secrets.token_hex(12)
    data = json.dumps(result)
    page = '''<!doctype html><meta charset="utf-8"><title>Temporary clap timing proof</title>
<style>body{font:16px system-ui;margin:24px;background:#eee}#frames{display:grid;grid-template-columns:repeat(3,320px);gap:10px}button{padding:0;border:2px solid #888;background:white}img{display:block;width:320px}#result{position:sticky;top:0;background:white;padding:12px}</style>
<h1>Clap timing proof — memory only</h1><p>Choose the first frame where the hands make contact. Images disappear when this local probe exits.</p><div id="result"></div><div id="frames"></div>
<script>const data=DATA; document.querySelector('#result').textContent=`Audio onset: ${data.audio_onset_s.toFixed(6)} s; peak: ${data.peak_dbfs.toFixed(1)} dBFS`;
for(const f of data.frames){let b=document.createElement('button');b.innerHTML=`<img src="data:image/png;base64,${f.image}"><span>Frame ${f.index}: ${f.pts.toFixed(6)} s</span>`;b.onclick=async()=>{const r=await fetch(location.pathname+'/choose',{method:'POST',body:JSON.stringify({index:f.index})});document.querySelector('#result').textContent=await r.text()};document.querySelector('#frames').appendChild(b)}</script>'''.replace('DATA', data)
    class Handler(http.server.BaseHTTPRequestHandler):
        def log_message(self, *_): pass
        def do_GET(self):
            if self.path != '/'+token: self.send_error(404); return
            body=page.encode();self.send_response(200);self.send_header('Content-Type','text/html');self.send_header('Cache-Control','no-store');self.end_headers();self.wfile.write(body)
        def do_POST(self):
            if self.path != '/'+token+'/choose': self.send_error(404); return
            length=int(self.headers.get('Content-Length',0))
            if not 0 < length <= 256: self.send_error(400); return
            index=json.loads(self.rfile.read(length))['index']
            frame=next(f for f in result['frames'] if f['index']==index)
            metric={k:result[k] for k in ('audio_onset_s','audio_peak_s','peak_dbfs','video_frames','audio_blocks')}
            metric.update({'contact_frame':index,'contact_frame_pts_s':frame['pts'],'audio_minus_contact_ms':round(1000*(result['audio_onset_s']-frame['pts']),3),'video_resolution_ms':33.334})
            body=json.dumps(metric).encode();print(body.decode(),flush=True)
            self.send_response(200);self.send_header('Cache-Control','no-store');self.end_headers();self.wfile.write(body)
    server=http.server.HTTPServer(('127.0.0.1',port),Handler)
    server.timeout=1
    print(f'VIEW http://127.0.0.1:{port}/{token}',flush=True)
    deadline=time.monotonic()+600
    try:
        while time.monotonic()<deadline:server.handle_request()
    finally:server.server_close()

if __name__=='__main__':
    p=argparse.ArgumentParser(description=__doc__);p.add_argument('--tools',type=Path,required=True);p.add_argument('--seconds',type=int,default=20);p.add_argument('--port',type=int,default=18660)
    a=p.parse_args()
    if not 5 <= a.seconds <= 30:p.error('window must be 5–30 seconds')
    serve(capture(a.tools.resolve(),Path.home()/'Library/Application Support/camstream-audio-native-proof',a.seconds),a.port)
