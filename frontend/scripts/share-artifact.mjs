// Validates the standalone Camera Sharing guest artifact against the contract
// the guest listener enforces (internal/transport/http/shareguest/handler.go): an
// index.html of at most 256 KiB and a flat assets/ directory of allowlisted
// names of at most 4 MiB each, served under /share-assets/. The page must also
// fit the guest CSP: same-origin scripts and styles only, no inline code or
// styles and no data: URIs. Usage: node scripts/share-artifact.mjs [dir]
import { readFile, readdir, stat } from "node:fs/promises";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";

export const ASSET_NAME = /^[A-Za-z0-9_-]+\.(?:js|css|svg|woff2|png|webp)$/;
export const LIMITS = { index: 256 * 1024, asset: 4 * 1024 * 1024, js_gzip: 16000, css_gzip: 4000 };
// The listener's header policy, less frame-ancestors, which a meta tag cannot carry.
export const PAGE_CSP =
  "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; media-src 'self' blob:; connect-src 'self'; font-src 'self'; base-uri 'none'";
const URL_ATTRIBUTES = new Set(["src", "href", "srcset", "poster", "action", "formaction", "data", "background", "ping"]);

/** A tag's attributes, whatever their quoting; names are lower case. */
function attributes(source) {
  const found = new Map();
  for (const [, name, double, single, bare] of source.matchAll(
    /([^\s"'<>\/=]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s"'=<>`]+)))?/g,
  ))
    found.set(name.toLowerCase(), double ?? single ?? bare ?? "");
  return found;
}
// Browser storage, capture and cookie access have no place on the guest page.
const FORBIDDEN_JS = [
  "localStorage",
  "sessionStorage",
  "indexedDB",
  "getUserMedia",
  "getDisplayMedia",
  "document.cookie",
  "sourceMappingURL",
];

export async function validateShareArtifact(dir) {
  const problems = [];
  const sizes = { index: 0, js_gzip: 0, css_gzip: 0 };
  const entries = await readdir(dir).catch(() => []);
  for (const name of entries)
    if (name !== "index.html" && name !== "assets") problems.push(`unexpected top-level entry ${name}`);
  let html = "";
  try {
    const info = await stat(join(dir, "index.html"));
    sizes.index = info.size;
    if (!info.isFile()) problems.push("index.html is not a file");
    if (info.size === 0 || info.size > LIMITS.index) problems.push(`index.html is ${info.size} bytes (1–${LIMITS.index})`);
    html = await readFile(join(dir, "index.html"), "utf8");
  } catch {
    problems.push("index.html is missing");
  }
  const assets = new Map();
  for (const name of await readdir(join(dir, "assets")).catch(() => (problems.push("assets/ is missing"), []))) {
    const path = join(dir, "assets", name);
    const info = await stat(path);
    if (!info.isFile()) problems.push(`assets/${name} is not a file`);
    else if (!ASSET_NAME.test(name)) problems.push(`assets/${name} does not match the served asset names`);
    else if (info.size > LIMITS.asset) problems.push(`assets/${name} exceeds ${LIMITS.asset} bytes`);
    else assets.set(name, await readFile(path));
  }

  const referenced = new Set();
  const local = (url, where) => {
    const name = /^\/share-assets\/([^/?#]+)$/.exec(url)?.[1];
    if (!name || !assets.has(name)) problems.push(`${where} references ${url}, not a served guest asset`);
    else referenced.add(name);
  };
  if (/<style[\s>]/i.test(html)) problems.push("index.html has an inline <style> block");
  if (/data:/i.test(html)) problems.push("index.html uses a data: URI");
  for (const [, body] of html.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/gi))
    if (body.trim()) problems.push("index.html has an inline script");
  let scripts = 0;
  let stylesheets = 0;
  let policy;
  for (const [, tag, rest = ""] of html.matchAll(/<([a-zA-Z][\w-]*)(\s[^>]*)?>/g)) {
    const name = tag.toLowerCase();
    const found = attributes(rest);
    for (const [attribute, value] of found) {
      if (attribute === "style") problems.push("index.html has an inline style attribute");
      else if (attribute.startsWith("on")) problems.push("index.html has an inline event handler");
      else if (URL_ATTRIBUTES.has(attribute)) local(value, "index.html");
    }
    if (name === "script") {
      if (found.get("type") !== "module" || !found.has("src")) problems.push("index.html has a script that is not an external module");
      else scripts += 1;
    }
    if (name === "link" && found.get("rel")?.toLowerCase() === "stylesheet") stylesheets += 1;
    if (name === "meta" && found.get("http-equiv")?.toLowerCase() === "content-security-policy")
      policy = found.get("content")?.replace(/\s+/g, " ").trim();
  }
  if (scripts !== 1) problems.push(`index.html loads ${scripts} module scripts (expected 1)`);
  if (stylesheets !== 1) problems.push(`index.html loads ${stylesheets} stylesheets (expected 1)`);
  if (policy !== PAGE_CSP) problems.push("index.html does not repeat the guest content security policy");

  for (const [name, bytes] of assets) {
    const source = bytes.toString("utf8");
    if (name.endsWith(".css")) {
      sizes.css_gzip += gzipSync(bytes).length;
      if (/data:/i.test(source)) problems.push(`assets/${name} uses a data: URI`);
      if (/@import/i.test(source)) problems.push(`assets/${name} imports another stylesheet`);
      for (const [, raw] of source.matchAll(/url\(\s*(['"]?)([^'")]+)\1\s*\)/gi)) local(raw, `assets/${name}`);
    }
    if (name.endsWith(".js")) {
      sizes.js_gzip += gzipSync(bytes).length;
      for (const word of FORBIDDEN_JS) if (source.includes(word)) problems.push(`assets/${name} uses ${word}`);
      if (/data:/i.test(source)) problems.push(`assets/${name} uses a data: URI`);
      if (/\bimport\s*\(\s*["'`](?:https?:)?\/\//.test(source)) problems.push(`assets/${name} imports a remote module`);
      if (/\bnew\s+Function\s*\(|\beval\s*\(/.test(source)) problems.push(`assets/${name} evaluates strings as code`);
    }
  }
  // Scripts and stylesheets the page loads may reference further assets.
  for (let grew = true; grew; ) {
    grew = false;
    for (const from of [...referenced])
      if (/\.(?:js|css)$/.test(from))
        for (const name of assets.keys())
          if (!referenced.has(name) && assets.get(from).toString("utf8").includes(name)) {
            referenced.add(name);
            grew = true;
          }
  }
  // The listener serves every file in assets/, so an unused one is a mistake.
  for (const name of assets.keys())
    if (!referenced.has(name)) problems.push(`assets/${name} is never referenced`);
  if (sizes.js_gzip > LIMITS.js_gzip) problems.push(`guest JavaScript is ${sizes.js_gzip} gzip bytes (limit ${LIMITS.js_gzip})`);
  if (sizes.css_gzip > LIMITS.css_gzip) problems.push(`guest CSS is ${sizes.css_gzip} gzip bytes (limit ${LIMITS.css_gzip})`);
  return { problems, sizes, assets: [...assets.keys()].sort() };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const dir = resolve(process.argv[2] ?? process.env.CAMSTREAM_SHARE_OUT_DIR ??
    fileURLToPath(new URL("../dist-share/", import.meta.url)));
  const result = await validateShareArtifact(dir);
  console.log(JSON.stringify({ dir, ...result, limits: LIMITS }, null, 2));
  if (result.problems.length) process.exit(1);
}
