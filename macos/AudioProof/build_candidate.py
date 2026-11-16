#!/usr/bin/env python3
"""T01 candidate only: build pinned sources into an unpacked proof directory."""
import argparse, json, os, shutil, subprocess, sys
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from runtime_pack import ROOT, DEFAULT_CACHE, fetch, extract, sha, inspect_macho


def build(work):
    lock = json.loads((ROOT / 'macos/AudioProof/candidate.lock.json').read_text())
    work.mkdir(parents=True, exist_ok=False)
    prefix = work / 'prefix'
    sdk = subprocess.check_output(['xcrun', '--show-sdk-path'], text=True).strip()
    cc = subprocess.check_output(['xcrun', '--find', 'clang'], text=True).strip()
    flags = f"-arch arm64 -mmacosx-version-min=26.0 -isysroot {sdk}"
    env = {'PATH':'/usr/bin:/bin:/usr/sbin:/sbin', 'HOME':str(work), 'TMPDIR':str(work),
           'LC_ALL':'C', 'CC':cc, 'CFLAGS':flags, 'LDFLAGS':flags, 'SDKROOT':sdk,
           'MACOSX_DEPLOYMENT_TARGET':'26.0', 'PKG_CONFIG_LIBDIR':str(prefix/'lib/pkgconfig'),
           'PKG_CONFIG_PATH':'', 'PKG_CONFIG_ALLOW_SYSTEM_CFLAGS':'1', 'PKG_CONFIG_ALLOW_SYSTEM_LIBS':'1'}
    sources, archives, commands = {}, {}, []
    def run(component, args, cwd):
        commands.append({'component':component,'argv':list(map(str,args))})
        print(component, args[0], flush=True)
        with (work/f'{component}.log').open('ab') as out:
            subprocess.run(args,cwd=cwd,env=env,stdout=out,stderr=subprocess.STDOUT,check=True)
    for name, rec in lock['sources'].items():
        archives[name] = fetch(rec, DEFAULT_CACHE/'sources', offline=True)
        target = work/'source'/name
        extract(archives[name],target)
        sources[name] = target if rec.get('prebuilt') else next(target.iterdir())
    for patch in lock['patches']:
        path = ROOT/patch['file']
        if sha(path)!=patch['sha256']: raise ValueError('patch digest mismatch')
        run('patch',['/usr/bin/patch','--batch','--fuzz=0','-p1','-i',str(path)],sources['ffmpeg'])
    for name in ('pkgconf','x264','opus','ffmpeg'):
        args=[str(sources[name]/'configure'), f'--prefix={prefix}', *lock['configure'][name]]
        if name=='ffmpeg':
            args += [f'--cc={cc}', f"--pkg-config={prefix/'bin/pkgconf'}", '--pkg-config-flags=--static',
                     f'--extra-cflags={flags}', f'--extra-ldflags={flags}', '--arch=aarch64','--target-os=darwin']
        run(name,args,sources[name])
        run(name,['/usr/bin/make','-j8'],sources[name])
        run(name,['/usr/bin/make','install'],sources[name])
    target=work/'candidate'
    for d in ('tools','sources','licenses','recipe'): (target/d).mkdir(parents=True)
    for name in ('ffmpeg','ffprobe'):
        shutil.copy2(prefix/'bin'/name,target/'tools'/name)
    shutil.copy2(sources['mediamtx']/'mediamtx',target/'tools/mediamtx')
    for name,folder in sources.items():
        dest=target/'licenses'/name; dest.mkdir()
        for f in folder.iterdir():
            if f.is_file() and f.name.upper().startswith(('LICENSE','COPYING','AUTHORS')): shutil.copy2(f,dest/f.name)
        if not lock['sources'][name].get('prebuilt'): shutil.copy2(archives[name],target/'sources'/archives[name].name)
    for patch in lock['patches']: shutil.copy2(ROOT/patch['file'],target/'recipe'/Path(patch['file']).name)
    shutil.copy2(__file__,target/'recipe/build_candidate.py')
    (target/'recipe/candidate.lock.json').write_text(json.dumps(lock,indent=2)+'\n')
    (target/'licenses/NOTICE.txt').write_text('T01 development candidate, not a production runtime pack. FFmpeg 9.0.2 is modified by the included Camstream UID patch and built with GPL libx264. Original sources, patch and recipe accompany this candidate. Opus is BSD-3-Clause. Preserve all upstream notices.\n')
    info={'candidate':lock['runtime_id'],'lock':lock,'commands':commands,'tools':{}}
    for f in (target/'tools').iterdir():
        info['tools'][f.name]={'sha256':sha(f),'macho':inspect_macho(f,lock['target'])}
    (target/'BUILDINFO.json').write_text(json.dumps(info,indent=2)+'\n')
    print(target,flush=True)

if __name__=='__main__':
    p=argparse.ArgumentParser();p.add_argument('--work',type=Path,required=True)
    build(p.parse_args().work.resolve())
