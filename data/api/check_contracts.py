#!/usr/bin/env python3
"""Verify external-module consumption and compile representative wire data in TS.
Run from any directory; needs Go and a pinned TypeScript tsc executable (--tsc).
All generated module/proxy/frontend files are temporary; no replaces or go.work.
"""
import argparse, hashlib, json, os, pathlib, shutil, subprocess, tempfile, zipfile
ROOT = pathlib.Path(__file__).resolve().parents[2]
MODULE = 'github.com/goliatone/go-cam-stream'

def run(args, cwd, env=None):
    subprocess.run(args, cwd=cwd, env=env, check=True)

def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--tsc', required=True, help='Path to TypeScript 5.9.3 tsc')
    args = parser.parse_args()
    with tempfile.TemporaryDirectory(prefix='camstream-t02-') as directory:
        temp = pathlib.Path(directory)
        files = [ROOT/'go.mod', ROOT/'go.sum'] + sorted((ROOT/'pkg/camstream').glob('*.go'))
        digest = hashlib.sha256(b''.join(str(p.relative_to(ROOT)).encode()+p.read_bytes() for p in files)).hexdigest()[:16]
        version = 'v0.0.0-t02.g'+digest
        proxy = temp/'proxy'/MODULE/'@v'; proxy.mkdir(parents=True)
        (proxy/(version+'.mod')).write_bytes((ROOT/'go.mod').read_bytes())
        (proxy/(version+'.info')).write_text(json.dumps({'Version':version,'Time':'2026-11-13T00:00:00Z'}))
        (proxy/'list').write_text(version+'\n')
        with zipfile.ZipFile(proxy/(version+'.zip'),'w') as archive:
            for path in files:
                archive.write(path,MODULE+'@'+version+'/'+str(path.relative_to(ROOT)))
        consumer=temp/'consumer'; consumer.mkdir()
        (consumer/'go.mod').write_text('module example.test/camstream-consumer\n\ngo 1.27.4\n\nrequire '+MODULE+' '+version+'\n')
        shutil.copyfile(ROOT/'pkg/camstream/testdata/consumer/main.go',consumer/'main.go')
        env=dict(os.environ,GOWORK='off',GOFLAGS='')
        original_proxy=subprocess.check_output(['go','env','GOPROXY'],text=True).strip()
        env['GOPROXY']=(temp/'proxy').as_uri()+','+original_proxy
        env['GONOSUMDB']=MODULE # Only this synthetic local version is exempt.
        run(['go','mod','tidy'],consumer,env)
        run(['go','run','-mod=readonly','.'],consumer,env)
        if 'replace ' in (consumer/'go.mod').read_text(): raise RuntimeError('consumer has a replace')
        deps=subprocess.check_output(['go','list','-mod=readonly','-deps',MODULE+'/pkg/camstream'],cwd=consumer,env=env,text=True).splitlines()
        forbidden=[p for p in deps if p.startswith((MODULE+'/internal/', 'github.com/goliatone/go-admin', 'github.com/gofiber/', 'github.com/AlexxIT/', 'github.com/pion/'))]
        if forbidden: raise RuntimeError('core imports host/media implementation: '+str(forbidden))
        print('PASS: external module via temporary file proxy; no replace/workspace; core dependency boundary',flush=True)
        frontend=temp/'frontend';frontend.mkdir()
        shutil.copyfile(ROOT/'frontend/src/services/contracts.ts',frontend/'contracts.ts')
        fixtures=json.loads((ROOT/'data/api/examples.json').read_text())
        types={'empty_catalog':'CameraList','catalog':'CameraList','offline_status':'CameraStatus','unknown_status':'StreamStatus','denied':'ErrorResponse','offer':'CreateSessionRequest','session':'Session','diagnose_request':'DiagnoseRequest','diagnose_response':'DiagnoseResponse','reconnect_target':'StreamRef','reconnect_response':'ReconnectResponse','csrf':'CSRFResponse'}
        source='import type * as API from "./contracts";\n'
        for key,typ in types.items(): source+='const '+key+' = '+json.dumps(fixtures[key])+' as const satisfies API.'+typ+';\n'
        (frontend/'examples.ts').write_text(source)
        shutil.copyfile(ROOT/'frontend/tsconfig.contracts.json',frontend/'tsconfig.json')
        cfg=json.loads((frontend/'tsconfig.json').read_text());cfg['include']=['*.ts'];(frontend/'tsconfig.json').write_text(json.dumps(cfg))
        run([args.tsc,'--project','tsconfig.json'],frontend)
        print('PASS: TypeScript contracts and all representative Go/JSON wire examples',flush=True)

if __name__=='__main__': main()
