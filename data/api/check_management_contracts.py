#!/usr/bin/env python3
"""Compile the same management JSON fixtures used by Go against TypeScript."""
import argparse,json,pathlib,subprocess,tempfile
root=pathlib.Path(__file__).resolve().parents[2]
p=argparse.ArgumentParser();p.add_argument('--tsc',required=True);args=p.parse_args()
with tempfile.TemporaryDirectory(prefix='camera-management-contracts-') as d:
    work=pathlib.Path(d)
    (work/'contracts.ts').write_bytes((root/'frontend/src/services/contracts.ts').read_bytes())
    source='import type * as API from "./contracts";\n'
    for i,(name,fixture) in enumerate(json.loads((root/'data/api/management_examples.json').read_text()).items()):
        source+=f'// {name}\nconst fixture{i} = {json.dumps(fixture["value"])} as const satisfies API.{fixture["type"]};\n'
    (work/'fixtures.ts').write_text(source)
    subprocess.run([str(pathlib.Path(args.tsc).resolve()),'--noEmit','--strict','--exactOptionalPropertyTypes','--target','ES2022','--moduleResolution','bundler','--module','ESNext',str(work/'fixtures.ts')],check=True)
print('PASS: management Go/JSON fixtures compile against TypeScript contracts')
