#!/usr/bin/env python3
"""Bounded loopback proof reader. Prints metrics only; never stores media or credentials."""
import argparse
import json
import math
import os
from pathlib import Path
import selectors
import subprocess
import tempfile
import time


def observe(tools, state, seconds, wrong_password=False):
    config = json.loads((state / 'camera.json').read_text())
    secret = json.loads((state / 'credentials.json').read_text())
    password = 'invalid-proof-password' if wrong_password else secret['password']
    # Credential characters are generated hex by the unchanged service.
    for value in (secret['username'], password):
        if not value or any(c not in 'abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-' for c in value):
            raise ValueError('unsafe credential syntax')
    started = time.monotonic()
    counts, first, last, gap, pts = {}, {}, {}, {}, {}
    expected_pts, durations, discontinuities, missing, maximum_pts_gap = {}, {}, {}, {}, {}
    with tempfile.TemporaryDirectory(prefix='camstream-audio-observe-') as temp:
        source = Path(temp) / 'source.ffconcat'
        fd = os.open(source, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
        with os.fdopen(fd, 'w') as out:
            out.write(f"ffconcat version 1.0\nfile 'rtsp://{secret['username']}:{password}@127.0.0.1:{config['port']}/{config['path']}'\noption rtsp_transport tcp\noption rw_timeout 5000000\noption probesize 32768\noption analyzeduration 500000\n")
        args = [str(tools / 'ffprobe'), '-v', 'error', '-protocol_whitelist', 'file,tcp,rtsp,rtp,udp',
                '-f', 'concat', '-safe', '0', '-i', str(source), '-show_packets',
                '-show_entries', 'packet=codec_type,pts_time,duration_time', '-of', 'compact=p=0']
        child = subprocess.Popen(args, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
        poll = selectors.DefaultSelector()
        poll.register(child.stdout, selectors.EVENT_READ)
        pending = b''
        next_report = 30
        reason = 'duration'
        try:
            while time.monotonic() - started < seconds:
                elapsed = time.monotonic() - started
                events = poll.select(.2)
                for key, _ in events:
                    block = os.read(key.fd, 16384)
                    if not block:
                        reason = 'reader_exit'; break
                    pending += block
                    while b'\n' in pending:
                        raw, pending = pending.split(b'\n', 1)
                        values = dict(field.split('=', 1) for field in raw.decode().split('|') if '=' in field)
                        kind = values.get('codec_type')
                        if kind not in ('audio', 'video'): continue
                        now = time.monotonic() - started
                        counts[kind] = counts.get(kind, 0) + 1
                        first.setdefault(kind, now)
                        gap[kind] = max(gap.get(kind, 0), now - last.get(kind, now))
                        last[kind] = now
                        try:
                            stamp = float(values.get('pts_time', 'nan'))
                            duration = float(values.get('duration_time', 'nan'))
                        except ValueError:
                            continue
                        if not math.isfinite(stamp) or not math.isfinite(duration) or duration <= 0:
                            continue
                        delta = stamp - expected_pts.get(kind, stamp)
                        if abs(delta) > .002:
                            discontinuities[kind] = discontinuities.get(kind, 0) + 1
                            missing[kind] = missing.get(kind, 0) + max(delta, 0)
                            maximum_pts_gap[kind] = max(maximum_pts_gap.get(kind, 0), delta)
                        pts[kind] = stamp
                        expected_pts[kind] = stamp + duration
                        durations[kind] = durations.get(kind, 0) + duration
                if reason == 'reader_exit': break
                if elapsed >= next_report:
                    print(json.dumps({'elapsed_s': round(elapsed, 2), 'packets': counts, 'last_pts': pts,
                                      'pts_discontinuities': discontinuities, 'missing_duration_s': missing}), flush=True)
                    next_report += 30
                if 'audio' not in counts and elapsed > 20:
                    reason = 'audio_startup_deadline'; break
                if 'audio' in last and elapsed - last['audio'] > 5:
                    reason = 'audio_stall_deadline'; break
        finally:
            if child.poll() is None: child.terminate()
            try: child.wait(timeout=3)
            except subprocess.TimeoutExpired: child.kill(); child.wait(timeout=3)
            poll.close()
            child.stdout.close()
        print(json.dumps({'result': reason, 'elapsed_s': round(time.monotonic()-started, 3),
                          'packets': counts, 'first_packet_s': first, 'maximum_arrival_gap_s': gap,
                          'last_pts': pts, 'wrong_password': wrong_password,
                          'duration_sum_s': durations, 'pts_discontinuities': discontinuities,
                          'missing_duration_s': missing, 'maximum_pts_gap_s': maximum_pts_gap}), flush=True)


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--tools', type=Path, required=True)
    parser.add_argument('--state', type=Path, default=Path.home() / 'Library/Application Support/camstream-audio-native-proof')
    parser.add_argument('--seconds', type=float, default=15)
    parser.add_argument('--wrong-password', action='store_true')
    args = parser.parse_args()
    if not 1 <= args.seconds <= 660: parser.error('duration must be 1–660 seconds')
    observe(args.tools.resolve(), args.state, args.seconds, args.wrong_password)
