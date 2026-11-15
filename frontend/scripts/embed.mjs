import { createHash } from 'node:crypto';
import { cp, mkdir, readFile, readdir, rename, rm, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
const root = fileURLToPath(new URL('../..', import.meta.url));
const source = path.join(root, 'frontend/dist');
const stage = path.join(root, 'data/.web-staging');
await rm(stage, { recursive: true, force: true });
await mkdir(stage, { recursive: true });
await cp(path.join(source, 'index.html'), path.join(stage, 'index.html'));
await cp(path.join(source, 'assets'), path.join(stage, 'assets'), { recursive: true });
const manifest = {};
for (const name of ['index.html', ...(await readdir(path.join(stage, 'assets'))).sort().map(n => `assets/${n}`)]) {
  if (!/^(index\.html|assets\/[A-Za-z0-9_-]+\.(js|css))$/.test(name)) throw new Error('Unexpected production asset');
  manifest[name] = createHash('sha256').update(await readFile(path.join(stage, name))).digest('hex');
}
await writeFile(path.join(stage, 'manifest.json'), JSON.stringify(manifest, null, 2) + '\n');
await rm(path.join(root, 'data/web'), { recursive: true, force: true });
await rename(stage, path.join(root, 'data/web'));
console.log(`Embedded ${Object.keys(manifest).length} production assets`);
