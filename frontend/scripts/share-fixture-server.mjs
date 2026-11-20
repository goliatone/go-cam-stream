// Local fixture for the standalone guest page. It serves dist-share/ on the
// guest listener's routes with its headers (internal/transport/http/sharing.go)
// and answers the guest API with the published shapes
// (data/api/sharing_examples.json). The first character of the share ID picks
// a scenario; open / for links. It carries no camera media: an accepted offer
// gets a send-only answer whose connection cannot complete, which exercises
// Connecting, Reconnecting and Retry without live playback. Live evidence
// belongs to the combined smoke. Usage: node scripts/share-fixture-server.mjs [--port 4174]
import { randomBytes } from "node:crypto";
import { createServer } from "node:http";
import { readFile } from "node:fs/promises";
import { extname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { ASSET_NAME } from "./share-artifact.mjs";

const root = fileURLToPath(new URL("../dist-share/", import.meta.url));
const examples = JSON.parse(await readFile(new URL("../../data/api/sharing_examples.json", import.meta.url), "utf8"));
const port = Number(process.argv[process.argv.indexOf("--port") + 1]) || 4174;
const HANDLE = /^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/;
const API = /^\/share-api\/v1\/shares\/([^/]+)\/(redeem|viewers)(?:\/([^/]+)(\/offer)?)?$/;
const HEADERS = {
  "Cache-Control": "no-store",
  "Referrer-Policy": "no-referrer",
  "X-Content-Type-Options": "nosniff",
  "Permissions-Policy": "camera=(), microphone=()",
  "Content-Security-Policy":
    "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; media-src 'self' blob:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'none'",
};
const TYPES = {
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
  ".woff2": "font/woff2",
  ".png": "image/png",
  ".webp": "image/webp",
};
const SCENARIOS = {
  A: ["watch", "Admitted; the connection never completes"],
  B: ["full", "Another viewer holds the place"],
  C: ["expired", "The link has expired"],
  D: ["revoked", "Sharing stops while the first offer is answered"],
  E: ["offline", "The camera owner is unreachable (502)"],
  F: ["denied", "The invitation is not valid"],
  G: ["unsupported", "The offer is refused"],
  H: ["expiring", "The share ends 20 seconds after the link opens"],
  R: ["limited", "The first exchange is rate limited"],
};
const INVITATION = examples.redeem.invitation;
const shares = new Map();
const log = [];
const token = () => randomBytes(32).toString("base64url");
const failure = (code, message, retryable) => ({ error: { code, message, retryable } });
const invalid = failure("invalid_request", "The request is not valid.", false);

/** Checks a receive-only offer with one video section and no audio. */
function inspect(sdp) {
  const sections = sdp.split(/\r\n(?=m=)/).slice(1);
  const kinds = sections.map((section) => /^m=(\w+)/.exec(section)?.[1]);
  const video = sections[kinds.indexOf("video")] ?? "";
  return { kinds, recvonly: video.split("\r\n").includes("a=recvonly") };
}

function answer(offer) {
  const lines = offer.split("\r\n");
  const mid = lines.find((line) => line.startsWith("a=mid:"))?.slice(6) ?? "0";
  const codecs = lines.map((line) => /^a=rtpmap:(\d+) (VP8|H264)\/90000$/.exec(line)).filter(Boolean);
  const codec = codecs.find((match) => match[2] === "VP8") ?? codecs[0];
  if (!codec) return undefined;
  const fingerprint = [...randomBytes(32)].map((byte) => byte.toString(16).padStart(2, "0")).join(":").toUpperCase();
  return [
    "v=0",
    "o=- 1 1 IN IP4 127.0.0.1",
    "s=-",
    "t=0 0",
    `a=group:BUNDLE ${mid}`,
    "a=msid-semantic: WMS fixture",
    `m=video 9 UDP/TLS/RTP/SAVPF ${codec[1]}`,
    "c=IN IP4 0.0.0.0",
    "a=rtcp:9 IN IP4 0.0.0.0",
    "a=ice-ufrag:fxtr",
    "a=ice-pwd:fixturefixturefixturefix",
    `a=fingerprint:sha-256 ${fingerprint}`,
    "a=setup:active",
    `a=mid:${mid}`,
    "a=sendonly",
    "a=rtcp-mux",
    `a=rtpmap:${codec[1]} ${codec[2]}/90000`,
    ...lines.filter((line) => line.startsWith(`a=fmtp:${codec[1]} `)),
    "a=ssrc:1001 cname:fixture",
    "a=msid:fixture video",
    "a=candidate:1 1 udp 2130706431 127.0.0.1 9 typ host",
    "a=end-of-candidates",
    "",
  ].join("\r\n");
}

async function body(req) {
  const chunks = [];
  let size = 0;
  for await (const chunk of req) {
    size += chunk.length;
    if (size > 64 * 1024) return undefined;
    chunks.push(chunk);
  }
  try {
    const value = JSON.parse(Buffer.concat(chunks).toString("utf8") || "null");
    return value && typeof value === "object" && !Array.isArray(value) ? value : undefined;
  } catch {
    return undefined;
  }
}
const only = (value, ...keys) => value !== undefined && Object.keys(value).every((key) => keys.includes(key));

async function guestAPI(req, shareID, kind, viewerID, offer, entry) {
  const [scenario] = SCENARIOS[shareID[0]] ?? SCENARIOS.A;
  const share = shares.get(shareID) ?? { redeems: 0, csrf: "", expiresAt: 0 };
  shares.set(shareID, share);
  entry.scenario = scenario;
  entry.cookie = /(?:^|;\s*)__Host-camstream-share=/.test(req.headers.cookie ?? "");
  if (req.headers["content-type"]?.split(";")[0].trim() !== "application/json") return [400, invalid];
  const value = await body(req);
  const expires = () => new Date(share.expiresAt).toISOString();
  if (kind === "redeem") {
    if (req.method !== "POST" || viewerID || !only(value, "invitation") || typeof value.invitation !== "string")
      return [400, invalid];
    entry.invitation = value.invitation === INVITATION ? "fixture" : "other";
    share.redeems += 1;
    if (scenario === "expired") return [410, examples.expired];
    if (scenario === "denied") return [403, failure("invalid_invitation", "This invitation is not valid.", false)];
    if (scenario === "limited" && share.redeems === 1)
      return [429, failure("rate_limited", "Try again shortly.", true), { "Retry-After": "1" }];
    share.csrf = randomBytes(18).toString("base64url");
    share.expiresAt = Date.now() + (scenario === "expiring" ? 20_000 : 15 * 60_000);
    const cookie = `__Host-camstream-share=${token()}; Path=/; Secure; HttpOnly; SameSite=Strict`;
    return [200, { expires_at: expires(), csrf_token: share.csrf }, { "Set-Cookie": cookie }];
  }
  entry.csrf = req.headers["x-csrf-token"] === share.csrf && share.csrf !== "";
  if (!entry.csrf) return [403, failure("permission_denied", "Open the invitation again.", false)];
  if (Date.now() >= share.expiresAt) return [410, examples.expired];
  if (req.method === "POST" && !viewerID) {
    if (!only(value, "viewer_id")) return [400, invalid];
    entry.resume = typeof value.viewer_id === "string";
    if (scenario === "full") return [409, examples.at_capacity];
    if (scenario === "offline") return [502, "Bad gateway", { "Content-Type": "text/plain; charset=utf-8" }];
    return [201, { viewer_id: value.viewer_id ?? token(), attempt_id: token(), expires_at: expires() }];
  }
  if (req.method === "POST" && offer) {
    if (!only(value, "attempt_id", "offer") || !only(value.offer, "type", "sdp")) return [400, invalid];
    const sdp = value.offer.sdp;
    if (value.offer.type !== "offer" || typeof sdp !== "string" || !sdp.startsWith("v=0\r\n") || sdp.length > 32 * 1024)
      return [400, invalid];
    entry.offer = inspect(sdp);
    if (scenario === "revoked") return [410, examples.revoked];
    const reply = answer(sdp);
    if (scenario === "unsupported" || !reply || entry.offer.kinds.join() !== "video" || !entry.offer.recvonly)
      return [422, failure("unsupported_offer", "This browser offer is not supported.", false)];
    return [200, { answer: { type: "answer", sdp: reply } }];
  }
  if (req.method === "DELETE" && viewerID && !offer) {
    if (!only(value, "attempt_id")) return [400, invalid];
    return [204, undefined];
  }
  return [405, invalid];
}

function index() {
  const links = Object.entries(SCENARIOS)
    .map(([letter, [name, about]]) => {
      const id = `${letter.repeat(42)}A`;
      return `<li><a href="/s/${id}#${INVITATION}">${name}</a>: ${about}</li>`;
    })
    .join("");
  return `<!doctype html><meta charset="utf-8"><title>Guest viewer fixtures</title><h1>Guest viewer fixtures</h1><ul>${links}<li><a href="/s/${"A".repeat(43)}">reopen</a>: No invitation in the link</li></ul>`;
}

async function serve(req, res) {
  const url = new URL(req.url ?? "/", "http://fixture");
  const send = (status, payload, headers = {}) => {
    const raw = typeof payload === "string" || payload === undefined || Buffer.isBuffer(payload);
    res.writeHead(status, { ...HEADERS, ...(raw ? {} : { "Content-Type": "application/json" }), ...headers });
    res.end(raw ? payload : JSON.stringify(payload));
  };
  try {
    if (req.method === "GET" && url.pathname === "/") return send(200, index(), { "Content-Type": "text/html; charset=utf-8" });
    if (req.method === "GET" && url.pathname === "/fixture/log") return send(200, log);
    const page = /^\/s\/([^/]+)$/.exec(url.pathname);
    if (req.method === "GET" && page) {
      if (!HANDLE.test(page[1])) return send(404, "404 page not found\n", { "Content-Type": "text/plain; charset=utf-8" });
      return send(200, await readFile(join(root, "index.html")), { "Content-Type": "text/html; charset=utf-8" });
    }
    const asset = /^\/share-assets\/([^/]+)$/.exec(url.pathname);
    if (req.method === "GET" && asset && ASSET_NAME.test(asset[1])) {
      const bytes = await readFile(join(root, "assets", asset[1])).catch(() => undefined);
      if (bytes) return send(200, bytes, { "Content-Type": TYPES[extname(asset[1])] });
    }
    const route = API.exec(url.pathname);
    if (route && HANDLE.test(route[1]) && (!route[3] || HANDLE.test(route[3]))) {
      const entry = { at: new Date().toISOString(), method: req.method, route: `${route[2]}${route[3] ? "/{viewer}" : ""}${route[4] ?? ""}` };
      const [status, payload, headers] = await guestAPI(req, route[1], route[2], route[3], Boolean(route[4]), entry);
      log.push({ ...entry, status });
      return send(status, payload, headers);
    }
    send(404, "404 page not found\n", { "Content-Type": "text/plain; charset=utf-8" });
  } catch {
    send(500, "fixture error\n", { "Content-Type": "text/plain; charset=utf-8" });
  }
}

try {
  await readFile(join(root, "index.html"));
} catch {
  console.error("dist-share/index.html is missing. Run `npm run build:share` first.");
  process.exit(1);
}
for (const host of ["127.0.0.1", "::1"])
  createServer(serve)
    .on("error", (error) => {
      if (host === "127.0.0.1") throw error;
    })
    .listen(port, host);
console.log(`Guest viewer fixtures on http://localhost:${port}/`);
