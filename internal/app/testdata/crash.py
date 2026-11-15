#!/usr/bin/env python3
"""Exercise the actual application controller and HTTPS transport in a dedicated init container.
Arguments: Linux test binary, T15 host peer binary, output directory.
Requires the pinned camstream-t15-feasibility image and unused localhost
35200/35100 TCP ports. Uses no camera credentials or real cameras.
"""
import json, os, pathlib, socket, subprocess as sp, sys, time, urllib.request
binary, peer, dest = map(lambda p: pathlib.Path(p).resolve(), sys.argv[1:])
dest.mkdir(parents=True, exist_ok=True)
name = 'camstream-t07-crash'
def call(*args): return sp.check_output(args, text=True).strip()
def emit(event, data): print(json.dumps({'event': event, 'data': data}), flush=True)
def inspect(): return json.loads(call('docker', 'inspect', name))[0]
def ready(generation):
 for _ in range(150):
  if call('docker', 'logs', name).count('controller_ready') >= generation:
   with urllib.request.urlopen('http://127.0.0.1:35200/ready', timeout=1) as r: return int(r.read())
  time.sleep(.1)
 raise RuntimeError('controller not ready')
def client(path):
 env=dict(os.environ, ENDPOINT='http://127.0.0.1:35200', TRANSPORT='tcp', T15_ROOT=str(dest/'peer'))
 (dest/'peer').mkdir(exist_ok=True)
 return sp.Popen([str(peer),'remote'], env=env, stdout=open(path,'w'), stderr=sp.STDOUT)
def media(path):
 for _ in range(150):
  if path.exists() and 'remote_media' in path.read_text(): return
  time.sleep(.1)
 raise RuntimeError('host media not ready')
prior={row.split()[0] for row in call('docker','ps','--format','{{.ID}} {{.Names}}').splitlines() if not row.split()[1].startswith('camstream-t07-')}; clients=[]; created=False
try:
 call('docker','run','-d','--init','--name',name,'--cpus','2','--memory','768m',
      '-e','CAMSTREAM_T07_CRASH=1',
      '-v',str(binary)+':/tests:ro',
      '-v',str(pathlib.Path(__file__).resolve().parents[3]/'config')+':/src/config:ro','-w','/src/internal/app','-p','127.0.0.1:35200:35200/tcp','-p','127.0.0.1:35100:35100/tcp',
      'camstream-t15-feasibility:1.9.15','/tests','-test.v','-test.run','^TestT07CrashController$','-test.timeout','90s')
 created=True; pid=ready(1)
 path=dest/'crash-client.jsonl';c=client(path);clients.append(c);media(path)
 time.sleep(1)
 top=call('docker','top',name,'-eo','pid,ppid,comm'); assert 'ffmpeg' in top
 emit('owned_workers_and_helper_before_crash',{'controller_pid':pid,'processes':top})
 start=time.monotonic();call('docker','exec',name,'kill','-KILL',str(pid))
 for _ in range(100):
  state=inspect()['State']
  if not state['Running']:break
  time.sleep(.05)
 assert not state['Running'] and state['ExitCode']==137 and not state['OOMKilled']
 emit('controller_crash_contained',{'ms':round((time.monotonic()-start)*1000),'state':state})
 assert c.wait(timeout=25)==0
 samples=[r['data'][0]['video_packets'] for r in map(json.loads,path.read_text().splitlines()) if r['event']=='remote_media']
 assert samples[0]>0 and len(set(samples[-4:]))==1
 emit('external_media_stopped',samples)
 for port in [35200,35100]:
  with socket.socket() as sock:
   sock.settimeout(.2);assert sock.connect_ex(('127.0.0.1',port))!=0
 call('docker','start',name);ready(2)
 logs=call('docker','logs',name)
 assert 'stale_generation_retired' in logs and 'old_session_reset' in logs
 emit('same_installation_restart_retired_files_and_sessions', True)
 path=dest/'restart-client.jsonl';c=client(path);clients.append(c);media(path);assert c.wait(timeout=25)==0
 emit('fresh_generation_media_after_restart',True)
 call('docker','stop','-t','5',name)
 assert inspect()['State']['ExitCode']==0
 emit('bounded_graceful_shutdown',True)
finally:
 for c in clients:
  if c.poll() is None:c.terminate();c.wait(timeout=5)
 if created:sp.run(['docker','rm','-f',name],check=True,stdout=sp.DEVNULL)
 remaining=set(call('docker','ps','-q').splitlines());assert prior.issubset(remaining)
 emit('unrelated_containers_preserved',True)
