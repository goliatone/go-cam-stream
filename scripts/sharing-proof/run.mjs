// Camera Sharing T11. Every listener and media hop is on IPv4 loopback.
import assert from "node:assert/strict";
import { execFileSync, spawn } from "node:child_process";
import { createHash, randomBytes, X509Certificate } from "node:crypto";
import { createSocket } from "node:dgram";
import { once } from "node:events";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer as createHTTPS } from "node:https";
import { createConnection, createServer } from "node:net";
import os from "node:os";
import { join, resolve } from "node:path";
import { setTimeout as delay } from "node:timers/promises";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import { stopProcessGroup } from "./process-group.mjs";

const root = fileURLToPath(new URL("../../", import.meta.url));
const host = "127.0.0.1";
const endpoint = (port) => host + ":" + port;
const serviceURL = (port) => "http://" + endpoint(port);
const sha = (bytes) => createHash("sha256").update(bytes).digest("hex");
const secret = () => randomBytes(24).toString("hex");
const browserFixture = readFileSync(new URL("./viewer.js", import.meta.url));
const childEnv = { PATH: "/usr/bin:/bin:/usr/sbin:/sbin", LANG: "C", PYTHONDONTWRITEBYTECODE: "1" };
const processes = [];
const servers = [];
const taps = [];
const listeners = [];
const controller = new AbortController();
for (const signal of ["SIGINT", "SIGTERM"]) {
  process.on(signal, () => controller.abort(new Error("Proof interrupted")));
}
const { values: args } = parseArgs({ options: { pack: { type: "string" }, chrome: { type: "string" }, phase: { type: "string", default: "all" } } });
assert(["playback", "all"].includes(args.phase), "phase must be playback or all");
const chrome = resolve(args.chrome ?? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
const lock = JSON.parse(readFileSync(join(root, "macos/runtime.lock.json")));
const pack = resolve(args.pack ?? join(root, ".cache/camera-runtime/packs", lock.packs["darwin-arm64"].file));
const dir = mkdtempSync(join(os.tmpdir(), "camstream-sharing-"));
const report = { phase: args.phase, cases: {}, host: { arch: os.arch(), os: os.version() } };

function child(binary, argv, stdio = ["ignore", "pipe", "pipe"]) {
  const proc = spawn(binary, argv, { cwd: dir, env: childEnv, detached: true, stdio });
  proc.once("error", () => controller.abort(new Error("Could not start an owned proof process")));
  processes.push(proc);
  return proc;
}

function exec(binary, argv, input) {
  return execFileSync(binary, argv, { cwd: dir, env: childEnv, input, timeout: 30000, stdio: ["pipe", "pipe", "pipe"] });
}

async function waitFor(label, condition, ms = 12000) {
  const deadline = performance.now() + ms;
  do {
    controller.signal.throwIfAborted();
    const result = await condition();
    if (result) return result;
    await delay(25, null, { signal: controller.signal });
  } while (performance.now() < deadline);
  throw new Error(label + " timed out");
}

async function listen(server) {
  server.listen(0, host);
  await once(server, "listening");
  return server.address().port;
}

function close(server) {
  const closed = new Promise((resolve) => server.close(resolve));
  server.closeAllConnections?.();
  return closed;
}

async function portNumbers(count) {
  const reservations = [];
  try {
    for (let index = 0; index < count; index++) {
      const server = createServer();
      reservations.push(server);
      listeners.push({ protocol: "tcp", port: await listen(server) });
    }
    return reservations.map((server) => server.address().port);
  } finally {
    await Promise.all(reservations.map(close));
  }
}

async function udpSocket() {
  controller.signal.throwIfAborted();
  const socket = createSocket("udp4");
  taps.push(socket);
  socket.bind(0, host);
  await once(socket, "listening");
  socket.on("error", () => controller.abort(new Error("Proof UDP socket failed")));
  listeners.push({ protocol: "udp", port: socket.address().port });
  return socket;
}

function localFetch(url, options) {
  return fetch(url, { redirect: "error", signal: AbortSignal.any([controller.signal, AbortSignal.timeout(6000)]), ...options });
}

function basic(user, password) {
  return "Basic " + Buffer.from(user + ":" + password).toString("base64");
}

async function setupMedia() {
  const runtime = join(dir, "runtime");
  exec("/usr/bin/env", ["python3", "-B", "-c", [
    "import sys",
    "sys.path.insert(0, sys.argv[1])",
    "from runtime_pack import load_lock, unpack_locked",
    "unpack_locked(sys.argv[2], sys.argv[3], load_lock())",
  ].join("\n"), join(root, "macos"), pack, runtime]);
  const tools = Object.fromEntries(["ffmpeg", "ffprobe", "mediamtx"].map((tool) => [tool, join(runtime, "tools", tool)]));
  report.runtime = {
    id: lock.runtime_id,
    packSHA256: lock.packs["darwin-arm64"].sha256,
    mediaMTX: lock.sources.mediamtx.version,
    ffmpeg: lock.sources.ffmpeg.version,
    tools: Object.fromEntries(Object.entries(tools).map(([tool, path]) => [tool, sha(readFileSync(path))])),
  };
  const [rtsp, whep, api] = await portNumbers(3);
  const reservation = await udpSocket();
  const udp = reservation.address().port;
  await new Promise((resolve) => reservation.close(resolve));
  taps.pop();
  const readPassword = secret();
  const guestPassword = secret();
  const apiPassword = secret();
  const config = {
    logLevel: "info", logDestinations: ["stdout"],
    ...Object.fromEntries(["metrics", "pprof", "playback", "rtmp", "hls", "srt", "moq"].map((key) => [key, false])),
    rtsp: true, rtspAddress: endpoint(rtsp), rtspTransports: ["tcp"], rtspAuthMethods: ["basic"],
    api: true, apiAddress: endpoint(api),
    webrtc: true, webrtcAddress: endpoint(whep), webrtcLocalUDPAddress: endpoint(udp),
    webrtcLocalTCPAddress: "", webrtcIPsFromInterfaces: false, webrtcAdditionalHosts: [host], webrtcICEServers2: [],
    authMethod: "internal",
    authInternalUsers: [
      { user: "any", ips: [host], permissions: ["source", "guest"].map((path) => ({ action: "publish", path })) },
      { user: "reader", pass: readPassword, ips: [host], permissions: [{ action: "read", path: "source" }] },
      { user: "gateway", pass: guestPassword, ips: [host], permissions: [{ action: "read", path: "guest" }] },
      { user: "operator", pass: apiPassword, ips: [host], permissions: [{ action: "api" }] },
    ],
    paths: { source: { source: "publisher", overridePublisher: false }, guest: { source: "publisher", overridePublisher: false } },
  };
  const configPath = join(dir, "media.json");
  writeFileSync(configPath, JSON.stringify(config), { mode: 0o600 });
  const server = child(tools.mediamtx, [configPath]);
  let ready = "";
  server.stdout.on("data", (data) => { ready = (ready + data).slice(-12000); });
  server.stderr.resume();
  await waitFor("MediaMTX listener", () => {
    assert.equal(server.exitCode, null, "MediaMTX exited during startup");
    return ready.includes("[WebRTC] listener opened") || ready.includes("[WebRTC] started with listeners");
  });
  const common = ["-nostdin", "-hide_banner", "-loglevel", "error"];
  const rtspOutput = ["-f", "rtsp", "-rtsp_transport", "tcp"];
  const rtspURL = "rtsp://" + endpoint(rtsp);
  const publish = child(tools.ffmpeg, [...common, "-re", "-f", "lavfi", "-i", "testsrc2=size=1280x720:rate=30",
    "-f", "lavfi", "-i", "sine=frequency=997:sample_rate=48000", "-map", "0:v:0", "-map", "1:a:0",
    "-c:v", "libx264", "-pix_fmt", "yuv420p", "-profile:v", "baseline", "-preset", "ultrafast", "-tune", "zerolatency",
    "-g", "30", "-bf", "0", "-b:v", "2000k", "-c:a", "libopus", ...rtspOutput, rtspURL + "/source"], ["ignore", "ignore", "ignore"]);
  await waitFor("Audio-bearing synthetic publisher", () => {
    assert.equal(publish.exitCode, null, "FFmpeg source publisher exited");
    return ready.includes("publishing to path 'source'");
  });
  const privateSource = join(dir, "source.ffconcat");
  writeFileSync(privateSource, "ffconcat version 1.0\nfile 'rtsp://reader:" + readPassword + "@" + endpoint(rtsp) + "/source'\noption rtsp_transport tcp\noption rw_timeout 5000000\n", { mode: 0o600 });
  const input = ["-protocol_whitelist", "file,tcp,rtsp,rtp,udp", "-f", "concat", "-safe", "0", "-i", privateSource];
  const filter = child(tools.ffmpeg, [...common, ...input, "-map", "0:v:0", "-c:v", "copy", "-an", ...rtspOutput, rtspURL + "/guest"], ["ignore", "ignore", "ignore"]);
  await waitFor("Video-only shared publisher", () => {
    assert.equal(filter.exitCode, null, "FFmpeg video-only remux exited");
    return ready.includes("publishing to path 'guest'");
  });
  const independent = child(tools.ffmpeg, ["-nostdin", "-hide_banner", "-loglevel", "info", "-progress", "pipe:1", ...input,
    "-map", "0:v:0", "-map", "0:a:0", "-af", "ashowinfo", "-f", "null", "-"]);
  let frame = 0;
  let audioFrames = 0;
  independent.stdout.on("data", (data) => {
    frame = Math.max(frame, ...[...String(data).matchAll(/^frame=(\d+)/gm)].map((match) => Number(match[1])));
  });
  independent.stderr.on("data", (data) => {
    audioFrames += [...String(data).matchAll(/ checksum:(?!00000000)[0-9A-F]{8}/g)].length;
  });
  return {
    whep: serviceURL(whep) + "/guest/whep", udp,
    api: serviceURL(api), apiAuth: basic("operator", apiPassword), whepAuth: basic("gateway", guestPassword),
    reader: () => {
      assert.equal(independent.exitCode, null, "Independent reader exited");
      return { frame, audioFrames };
    },
  };
}

async function tapViewer(media, answer) {
  const socket = await udpSocket();
  const port = socket.address().port;
  let clientPort;
  const observed = { port, packets: 0, lastRTP: 0 };
  socket.on("message", (bytes, peer) => {
    if (peer.address !== host) return;
    if (peer.port === media.udp) {
      if (!clientPort) return;
      // The RTP header stays visible in SRTP. Exclude the RTCP packet-type range.
      if (bytes.length >= 12 && (bytes[0] & 0xc0) === 0x80 && (bytes[1] < 192 || bytes[1] > 223)) {
        observed.packets++;
        observed.lastRTP = performance.now();
      }
      socket.send(bytes, clientPort, host);
    } else {
      if (clientPort && clientPort !== peer.port) return;
      clientPort = peer.port;
      socket.send(bytes, media.udp, host);
    }
  });
  const candidates = [...answer.matchAll(/^a=candidate:([^\r\n]*)/gm)];
  assert(candidates.length > 0, "MediaMTX answer had no ICE candidate");
  for (const [, candidate] of candidates) {
    const fields = candidate.split(/ +/);
    assert.equal(fields[2].toLowerCase(), "udp", "Unexpected media transport");
    assert.equal(fields[4], host, "Non-loopback media candidate");
    assert.equal(Number(fields[5]), media.udp, "Unexpected media listener");
  }
  observed.answer = answer.replace(/^(a=candidate:\S+ \S+ \S+ \S+ \S+ )\d+/gm, (_, prefix) => prefix + port);
  return observed;
}

async function gateway(media) {
  exec("/usr/bin/openssl", ["req", "-new", "-x509", "-nodes", "-newkey", "rsa:2048", "-days", "1", "-subj", "/CN=127.0.0.1", "-keyout", "key.pem", "-out", "cert.pem"]);
  const key = readFileSync(join(dir, "key.pem"));
  const cert = readFileSync(join(dir, "cert.pem"));
  const spki = new X509Certificate(cert).publicKey.export({ type: "spki", format: "der" });
  const pin = createHash("sha256").update(spki).digest("base64");
  const sessions = new Map();
  let lastOffer;
  let error;
  const server = createHTTPS({ key, cert, requestTimeout: 6000 }, (req, res) => {
    res.setHeader("Cache-Control", "no-store");
    route(req, res).catch((cause) => {
      error ??= cause;
      res.writeHead(502).end();
    });
  });
  servers.push(server);
  const port = await listen(server);
  listeners.push({ protocol: "tcp", port });
  const origin = "https://" + endpoint(port);

  async function route(req, res) {
    if (req.headers.host !== endpoint(port)) return res.writeHead(403).end();
    if (req.method === "GET" && req.url === "/") {
      res.setHeader("Content-Security-Policy", "default-src 'self'; media-src 'self' blob:");
      return res.writeHead(200, { "Content-Type": "text/html" }).end("<!doctype html><title>Sharing proof</title><body><script src=/viewer.js></script>");
    }
    if (req.method === "GET" && req.url === "/viewer.js") return res.writeHead(200, { "Content-Type": "text/javascript" }).end(browserFixture);
    if (req.method === "GET" && req.url === "/favicon.ico") return res.writeHead(204).end();
    if (req.method !== "POST" || req.url !== "/offer") return res.writeHead(404).end();
    const session = sessions.get(req.headers["x-proof-session"]);
    if (req.headers.origin !== origin || !session || session.revoked) return res.writeHead(403).end();
    if (session.pending || session.peer) return res.writeHead(409).end();
    if (req.headers["content-type"] !== "application/sdp") return res.writeHead(415).end();
    session.pending = true;
    try {
      let offer = "";
      for await (const data of req) {
        offer += data;
        if (offer.length > 32768) return res.writeHead(413).end();
      }
      if (controller.signal.aborted || session.revoked || res.destroyed) return res.writeHead(403).end();
      const sections = offer.split(/\r?\n(?=m=)/);
      // A video-only publisher alone can still negotiate an empty audio track.
      if (sections.length !== 2 || !sections[1].startsWith("m=video ") || !/^a=recvonly\r?$/m.test(sections[1])) {
        return res.writeHead(422).end();
      }
      lastOffer = offer;
      const result = await localFetch(media.whep, { method: "POST", body: offer, headers: { "Content-Type": "application/sdp", Authorization: media.whepAuth } });
      assert.equal(result.status, 201, "MediaMTX WHEP offer failed");
      const location = result.headers.get("location");
      assert.match(location ?? "", /^\/guest\/whep\/[a-f\d-]{36}$/i, "Expected a private WHEP resource reference");
      const id = result.headers.get("id");
      assert.match(id ?? "", /^[a-f\d-]{36}$/i, "Expected a MediaMTX peer ID");
      const resource = new URL(location, media.whep);
      const tap = await tapViewer(media, await result.text());
      session.peer = { resource, id, tap };
      res.writeHead(201, { "Content-Type": "application/sdp" }).end(tap.answer);
    } finally {
      session.pending = false;
    }
  }

  async function endPeer(session, revoke) {
    if (revoke) session.revoked = true;
    const at = performance.now();
    const peer = session.peer;
    const result = await localFetch(peer.resource, { method: "DELETE", headers: { Authorization: media.whepAuth } });
    assert(result.ok, "WHEP peer teardown failed");
    assert.equal(await status(peer), 404, "Revoked peer remains in the control API");
    session.peer = undefined;
    return at;
  }

  async function status(peer) {
    const response = await localFetch(media.api + "/v3/webrtcsessions/get/" + peer.id, { headers: { Authorization: media.apiAuth } });
    await response.body?.cancel();
    return response.status;
  }

  return {
    origin, pin, endPeer, status,
    invite() {
      const key = secret();
      const session = { key, revoked: false };
      sessions.set(key, session);
      return session;
    },
    failure: () => error,
    async checkBypass() {
      const bypass = await localFetch(media.whep, { method: "POST", body: lastOffer, headers: { "Content-Type": "application/sdp" } });
      assert.equal(bypass.status, 401, "Direct backend signaling without credentials succeeded");
      await bypass.body?.cancel();
    },
  };
}

function openBrowser(pin) {
  const proc = child(chrome, ["--headless", "--remote-debugging-pipe", "--no-first-run", "--no-default-browser-check", "--disable-background-networking",
    "--disable-sync", "--disable-component-update", "--disable-extensions", "--disable-default-apps", "--disable-breakpad", "--disable-crash-reporter",
    "--disable-background-timer-throttling", "--use-mock-keychain",
    "--autoplay-policy=no-user-gesture-required", "--no-proxy-server", "--host-resolver-rules=MAP * ~NOTFOUND, EXCLUDE 127.0.0.1",
    "--ignore-certificate-errors-spki-list=" + pin, "--user-data-dir=" + join(dir, "chrome"), "about:blank"], ["ignore", "ignore", "ignore", "pipe", "pipe"]);
  const [, , , input, output] = proc.stdio;
  let next = 0;
  let received = "";
  const pending = new Map();
  let disconnected = false;
  function disconnect() {
    disconnected = true;
    for (const finish of pending.values()) finish({ error: true });
  }
  proc.once("error", disconnect);
  proc.once("exit", disconnect);
  controller.signal.addEventListener("abort", disconnect, { once: true });
  input.on("error", () => {});
  output.setEncoding("utf8");
  output.on("data", (data) => {
    received += data;
    const messages = received.split("\0");
    received = messages.pop();
    for (const raw of messages) {
      const msg = JSON.parse(raw);
      pending.get(msg.id)?.(msg);
    }
  });
  return (method, params, sessionId) => new Promise((resolve, reject) => {
    if (disconnected) return reject(new Error("Chrome disconnected before " + method));
    const id = ++next;
    const timer = setTimeout(() => {
      pending.delete(id);
      reject(new Error("Chrome timed out in " + method));
    }, 10000);
    pending.set(id, (msg) => {
      clearTimeout(timer);
      pending.delete(id);
      if (msg.error) reject(new Error("Chrome command failed: " + method));
      else resolve(msg.result);
    });
    input.write(JSON.stringify({ id, method, params, sessionId }) + "\0");
  });
}

async function verifyMedia() {
  const media = await setupMedia();
  const proof = await gateway(media);
  const send = openBrowser(proof.pin);
  report.browser = (await send("Browser.getVersion")).product;
  const { targetId } = await send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await send("Target.attachToTarget", { targetId, flatten: true });
  async function evaluate(expression) {
    const result = await send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true }, sessionId);
    if (result.exceptionDetails) throw new Error("Proof viewer browser script failed");
    return result.result.value;
  }
  function call(method, session, ...parameters) {
    assert(["join", "sample", "disconnect"].includes(method));
    return evaluate(method + "(" + [session.key, ...parameters].map((value) => JSON.stringify(value)).join(",") + ")");
  }
  await send("Page.navigate", { url: proof.origin }, sessionId);
  await waitFor("Test TLS and browser fixture", () => evaluate("isSecureContext && typeof join === 'function'"));
  const a = proof.invite();
  const b = proof.invite();
  async function play(session, audio) {
    const before = performance.now();
    const status = await call("join", session, { audio });
    if (proof.failure()) throw proof.failure();
    assert.equal(status, 201, "Fixture join failed");
    const frames = await waitFor("Rendered H.264 frames", async () => {
      const state = await call("sample", session);
      return state.frames >= 10 ? state : null;
    });
    assert.equal(await proof.status(session.peer), 200, "MediaMTX peer missing");
    assert.equal(frames.remotePort, session.peer.tap.port, "Browser bypassed the media observer");
    assert(session.peer.tap.packets > 0, "No server-originated RTP was observed");
    assert.deepEqual([frames.width, frames.height], [1280, 720], "Unexpected video dimensions");
    assert.deepEqual(frames.tracks, ["video"], "Guest received a non-video track");
    assert.equal(frames.audioPackets, 0, "Guest received audio RTP");
    assert.equal(frames.audioAccepted, false, "Server negotiated a guest audio track");
    return { frames: frames.frames, timeToTenFramesMs: Math.round(performance.now() - before) };
  }
  report.cases.initialPlayback = await play(a, false);
  if (args.phase === "playback") return;
  assert.equal(await call("join", b, { audio: true }), 422, "Initial audio offer was not rejected");
  report.cases.audioOfferDenied = true;
  report.cases.secondViewer = await play(b, false);
  await proof.checkBypass();
  report.cases.directSignalingDenied = true;
  // Reconnect A through the same admitted viewer authority with an audio offer.
  await proof.endPeer(a, false);
  await call("disconnect", a);
  assert.equal(await call("join", a, { audio: true }), 422, "Reconnect audio offer was not rejected");
  report.cases.reconnectAudioOfferDenied = true;
  report.cases.videoOnlyReconnect = await play(a, false);
  const before = await waitFor("Independent audio-bearing RTSP reader", () => {
    const samples = media.reader();
    return samples.frame > 0 && samples.audioFrames > 0 ? samples : null;
  });
  const observerA = a.peer.tap;
  const observerB = b.peer.tap;
  const framesB = (await call("sample", b)).frames;
  const packetsB = observerB.packets;
  await waitFor("Revocation requires recent A RTP", () => performance.now() - observerA.lastRTP < 100, 2000);
  const revokedAt = await proof.endPeer(a, true);
  const peerGoneMs = Math.round(performance.now() - revokedAt);
  await delay(2200, null, { signal: controller.signal });
  const cessationMs = Math.ceil(Math.max(0, observerA.lastRTP - revokedAt));
  // Take a quiet window beyond the two-second deadline with the revoked browser still open.
  const revokedPackets = observerA.packets;
  const bAfter = await call("sample", b);
  const aAfter = await call("sample", a);
  assert.equal(await call("join", a), 403, "Revoked viewer rejoined");
  assert.equal(await proof.status(b.peer), 200, "Second MediaMTX peer disappeared");
  const readerAfter = media.reader();
  assert(peerGoneMs <= 2000 && cessationMs <= 2000, "Viewer teardown exceeded two seconds");
  assert.equal(observerA.packets, revokedPackets, "Revoked viewer RTP resumed");
  assert(aAfter.peerOpen, "Revoked browser closed its own peer");
  assert(bAfter.frames > framesB && observerB.packets > packetsB, "Second viewer stopped receiving frames");
  assert.equal(bAfter.audioPackets, 0, "Second viewer received audio RTP");
  assert(readerAfter.frame > before.frame && readerAfter.audioFrames > before.audioFrames, "Independent RTSP reader stopped");
  report.cases.revoke = {
    peerGoneMs, lastRTPAfterRevocationMs: cessationMs, revokedBrowserPeerOpen: true, replayDenied: true,
    secondViewerFrames: bAfter.frames - framesB, secondViewerRTPPackets: observerB.packets - packetsB,
    independentReaderFrames: readerAfter.frame - before.frame, independentReaderAudioFrames: readerAfter.audioFrames - before.audioFrames,
  };
}

async function cleanup() {
  controller.abort(new Error("Proof cleanup started"));
  const results = await Promise.allSettled([
    ...servers.map(close),
    ...[...processes].reverse().map((proc) => stopProcessGroup(proc.pid)),
    ...taps.map((socket) => new Promise((resolve) => socket.close(resolve))),
  ]);
  rmSync(dir, { recursive: true, force: true });
  assert(results.every((result) => result.status === "fulfilled"), "An owned resource failed to stop");
  for (const { protocol, port } of listeners) {
    if (protocol === "tcp") {
      const connection = createConnection({ port, host, timeout: 500 });
      const reachable = await new Promise((resolve) => {
        connection.once("connect", () => resolve(true));
        connection.once("timeout", () => resolve(true));
        connection.once("error", (error) => resolve(error.code !== "ECONNREFUSED"));
      });
      connection.destroy();
      assert(!reachable, "An owned TCP listener remains reachable");
    } else {
      const socket = createSocket("udp4");
      try {
        socket.bind(port, host);
        await once(socket, "listening");
      } finally {
        socket.close();
      }
    }
  }
  report.cleanup = { processGroups: processes.filter((proc) => proc.pid !== undefined).length, ports: listeners.length, scratchRemoved: true };
}

try {
  report.sources = Object.fromEntries(["run.mjs", "viewer.js", "process-group.mjs"].map((name) => [name, sha(readFileSync(new URL(name, import.meta.url)))]));
  await verifyMedia();
  report.outcome = args.phase === "all" ? "pass" : "playback-only";
} catch (error) {
  report.outcome = "fail";
  report.failure = error.message;
  process.exitCode = 1;
} finally {
  try {
    await cleanup();
  } catch (error) {
    report.outcome = "fail";
    report.cleanupFailure = error.message;
    process.exitCode = 1;
  }
  console.log(JSON.stringify(report, null, 2));
}
