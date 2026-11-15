#!/usr/bin/python3
"""Synthetic BGRA socket peer for native framing/cancellation checks."""
import os
import socket
import struct
import sys
import time

path, mode = sys.argv[1:]
server = socket.socket(socket.AF_UNIX)
server.bind(path)
os.chmod(path, 0o600)
server.listen(1)
server.settimeout(5)
print('ready', flush=True)
try:
    client, _ = server.accept()
    with client:
        frame = struct.pack('>4sHHQQQIIII', b'CSPR', 1, 48, 0x0123456789abcdef, 1,
                            int(time.time() * 1000), 2, 1, 8, 8) + bytes.fromhex('0000ffff00ff00ff')
        if mode == 'malformed':
            frame = frame[:44] + struct.pack('>I', 0xffffffff) + frame[48:]
        if mode == 'partial':
            frame = frame[:51]
        if mode == 'cancel':
            client.recv(1)
        else:
            for offset in range(0, len(frame), 3):
                client.sendall(frame[offset:offset + 3])
                time.sleep(.001)
except (BrokenPipeError, ConnectionResetError, socket.timeout):
    pass
finally:
    server.close()
    os.unlink(path)
