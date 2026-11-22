import { readFile, readdir } from 'node:fs/promises';
import { gzipSync } from 'node:zlib';
const dist = new URL('../dist/', import.meta.url);
const manifest = JSON.parse(await readFile(new URL('.vite/manifest.json', dist), 'utf8'));
const closure = (keys, result = new Set()) => {
  for (const key of keys) {
    const chunk = manifest[key];
    if (!chunk) throw Error(`Missing build manifest entry: ${key}`);
    if (result.has(chunk.file)) continue;
    result.add(chunk.file);
    closure(chunk.imports ?? [], result);
  }
  return result;
};
const initial = closure(Object.keys(manifest).filter(key => manifest[key].isEntry));
const management = closure(['src/hooks/use-management.tsx', 'src/components/camera-management.tsx']);
for (const file of initial) management.delete(file);
const assets = (await readdir(new URL('assets/', dist))).map(name => `assets/${name}`);
async function measure(files) {
  let bytes = 0;
  for (const file of files) bytes += gzipSync(await readFile(new URL(file, dist))).length;
  return bytes;
}
const sizes = {
  initial_js: await measure([...initial].filter(file => file.endsWith('.js'))),
  lazy_management_js: await measure(management),
  all_js: await measure(assets.filter(file => file.endsWith('.js'))),
  all_css: await measure(assets.filter(file => file.endsWith('.css'))),
  all_svg: await measure(assets.filter(file => file.endsWith('.svg'))),
};
const limits = { initial_js: 21400, lazy_management_js: 15150, all_css: 7000 };
console.log(JSON.stringify({ gzip_bytes: sizes, limits }, null, 2));
if (Object.entries(limits).some(([key, limit]) => sizes[key] > limit)) throw Error('Viewer payload budget exceeded');
