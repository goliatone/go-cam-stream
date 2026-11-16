// Preview snapshots: captures every state of the dev-only preview (`preview.html`)
// at a git ref and in the working tree, or at a second ref, and reports the views
// that differ. The ref is read with `git archive`, so no baseline images are kept
// and the checkout, index and branches are left alone. The project's own Vite
// serves each version, and a local Chrome or Chromium captures it over the
// DevTools protocol, so the script needs no dependency of its own.
import { execFileSync, spawn } from "node:child_process";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readdirSync,
  readFileSync,
  realpathSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parseArgs } from "node:util";
import { createServer } from "vite";
import { compareImages, decodePng, encodePng } from "./snapshot-png.mjs";

const USAGE = `Usage: npm run snapshots -- [options]

Captures the preview's states at a git ref and in the working tree, and reports
the views that differ. Exits 0 when no view differs, 1 when one does and 2 when
the run fails.

  --base <ref>          The version to compare with (default HEAD)
  --head <ref>          A second ref, instead of the working tree
  --scheme <scheme>     Only light or only dark (default both)
  --only <text>         Only views whose name contains the text, such as "phone"
  --tolerance <levels>  A view differs when a pixel differs by more than this many
                        levels in a colour channel (0-254, default 3)
  --area <pixels>       or when more than this many pixels differ at all
                        (default 1000)
  --out <dir>           Where the captures and the report go, relative to
                        frontend/ (default .snapshots). It is emptied on each run,
                        and refused if it holds files this script did not make.
  --chrome <path>       The Chrome or Chromium program (default CHROME_PATH, then
                        the usual places)
  -h, --help            Shows this text`;

const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

/** The sizes every state is captured at. A phone's whole page is captured. */
const SIZES = [
  { name: "desktop", width: 1440, height: 900, touch: false, fullPage: false },
  { name: "phone", width: 375, height: 812, touch: true, fullPage: true },
  // A phone held sideways, where the viewer fills the screen with the video.
  { name: "landscape", width: 667, height: 330, touch: true, fullPage: false },
];

/** Views reached with a click after their state is shown: a selector or a button's text. */
const EXTRAS = [
  { size: "desktop", scenario: "playing", name: "dialog", click: ".shortcuts-button" },
  { size: "desktop", scenario: "playing", name: "rail-collapsed", click: ".rail-toggle" },
  { size: "desktop", scenario: "busy", name: "sheet", click: "Choose another camera" },
  { size: "phone", scenario: "busy", name: "sheet", click: "Choose another camera" },
  { size: "landscape", scenario: "playing", name: "sheet", click: ".overlay-cameras" },
];

/** What the preview needs from outside its folder: the API examples it imports. */
const OUTSIDE = ["data/api"];

/** Results that make a view differ. */
const DIFFERS = new Set(["changed", "added", "removed", "error"]);

const CHROMES = {
  darwin: [
    "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    "/Applications/Chromium.app/Contents/MacOS/Chromium",
  ],
  linux: ["/usr/bin/google-chrome", "/usr/bin/google-chrome-stable", "/usr/bin/chromium", "/usr/bin/chromium-browser"],
};

const CHROME_FLAGS = [
  "--headless",
  // The DevTools protocol on a pipe, so the browser closes when this script ends.
  "--remote-debugging-pipe",
  "--no-first-run",
  "--no-default-browser-check",
  "--disable-extensions",
  "--disable-sync",
  "--disable-background-networking",
  "--disable-component-update",
  "--disable-default-apps",
  "--disable-background-timer-throttling",
  "--disable-renderer-backgrounding",
  "--disable-backgrounding-occluded-windows",
  // Keeps the throwaway profile out of the system's keychain and password store.
  "--use-mock-keychain",
  "--password-store=basic",
  // The same colours and rendering on every run and display.
  "--force-color-profile=srgb",
  "--hide-scrollbars",
  "--mute-audio",
  "--lang=en-US",
];

/**
 * Runs before the preview's own scripts. The clock stands still at 12:00:00 UTC on
 * 14 November 2026, and timers of a second or more never fire, so countdowns,
 * fading controls and the preview's own timed changes hold each state as first
 * drawn.
 */
const FREEZE = `(() => {
  const at = Date.UTC(2026, 10, 14, 12, 0, 0);
  const RealDate = Date;
  globalThis.Date = new Proxy(RealDate, {
    construct: (target, args, newTarget) =>
      Reflect.construct(target, args.length ? args : [at], newTarget),
    apply: () => new RealDate(at).toString(),
    get: (target, key) => (key === "now" ? () => at : Reflect.get(target, key)),
  });
  Performance.prototype.now = () => 100000;
  const timeout = setTimeout;
  const interval = setInterval;
  const never = () => timeout(() => {}, 0);
  globalThis.setTimeout = (run, delay = 0, ...rest) =>
    Number(delay) >= 1000 ? never() : timeout(run, delay, ...rest);
  globalThis.setInterval = (run, delay = 0, ...rest) =>
    Number(delay) >= 1000 ? never() : interval(run, delay, ...rest);
})();`;

/** Waits for the preview to render, then hides its own bar and stops animations. */
const READY = `(async () => {
  for (let i = 0; i < 500 && !document.getElementById("scenario"); i++)
    await new Promise((done) => setTimeout(done, 10));
  if (!document.getElementById("scenario")) return false;
  const style = document.createElement("style");
  style.textContent =
    ".preview-bar { display: none !important; } " +
    "*, *::before, *::after { animation: none !important; transition: none !important; caret-color: transparent !important; }";
  document.head.append(style);
  return true;
})()`;

/**
 * Waits until two frames in a row, each with the tasks queued in it, pass without
 * a change to the document, for up to 60 frames; resolves to whether they did.
 * Effects run after the page is drawn and can change it again, and a state chosen
 * before they have run can be overturned by them.
 */
const SETTLE = `(async () => {
  let changed = false;
  const observer = new MutationObserver(() => (changed = true));
  observer.observe(document, { subtree: true, childList: true, attributes: true, characterData: true });
  try {
    for (let frames = 0, quiet = 0; frames < 60; frames++) {
      changed = false;
      await new Promise((done) => requestAnimationFrame(() => setTimeout(done, 0)));
      quiet = changed ? 0 : quiet + 1;
      if (quiet === 2) return true;
    }
    return false;
  } finally {
    observer.disconnect();
  }
})()`;

const SCENARIOS = `[...document.getElementById("scenario").options].map((option) => option.value)`;

/** Chooses a state in the preview's select; false when the preview has no such state. */
const choose = (scenario) => `(() => {
  const select = document.getElementById("scenario");
  const scenario = ${JSON.stringify(scenario)};
  if (![...select.options].some((option) => option.value === scenario)) return false;
  select.value = scenario;
  select.dispatchEvent(new Event("change", { bubbles: true }));
  return true;
})()`;

/** Clicks an element by selector, or a button by its text; false when there is none. */
const click = (target) => `(() => {
  const target = ${JSON.stringify(target)};
  const element = target.startsWith(".")
    ? document.querySelector(target)
    : [...document.querySelectorAll("button")].find((button) => button.textContent.trim() === target);
  if (!element) return false;
  element.click();
  return true;
})()`;

const FINISH = `(async () => {
  await document.fonts.ready;
  await new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done)));
})()`;

class Failure extends Error {}

function parse(config) {
  try {
    return parseArgs(config);
  } catch (error) {
    throw new Failure(`${error.message}\n\n${USAGE}`);
  }
}

const sleep = (ms) => new Promise((done) => setTimeout(done, ms));

/** Rejects with a Failure when `promise` takes longer than `ms`. */
function within(promise, what, ms = 20000) {
  let timer;
  const late = new Promise((_, fail) => {
    timer = setTimeout(() => fail(new Failure(`${what} took longer than ${ms / 1000} s`)), ms);
  });
  return Promise.race([promise, late]).finally(() => clearTimeout(timer));
}

const count = (n) => n.toLocaleString("en-US");
const plural = (n, word) => `${count(n)} ${word}${n === 1 ? "" : "s"}`;
const exception = (details) => details.exception?.description?.split("\n")[0] ?? details.text;

function git(args, options = {}) {
  return execFileSync("git", args, { cwd: frontend, maxBuffer: 1 << 28, ...options });
}

function commit(ref) {
  try {
    return git(["rev-parse", "--verify", "--quiet", `${ref}^{commit}`], {
      stdio: ["ignore", "pipe", "ignore"],
    })
      .toString()
      .trim();
  } catch {
    throw new Failure(`${ref} is not a commit in this repository`);
  }
}

function exists(sha, file) {
  try {
    git(["cat-file", "-e", `${sha}:${file}`], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}

/** The preview at a commit, extracted into `dir`, with the working tree's dependencies. */
function extract(root, folder, sha, dir) {
  mkdirSync(dir, { recursive: true });
  const paths = [folder, ...OUTSIDE.filter((file) => exists(sha, file))];
  const archive = git(["archive", "--format=tar", sha, "--", ...paths], { cwd: root });
  execFileSync("tar", ["-x", "-f", "-", "-C", dir], { input: archive, maxBuffer: 1 << 28 });
  const copy = path.join(dir, folder);
  symlinkSync(path.join(frontend, "node_modules"), path.join(copy, "node_modules"), "dir");
  const lock = (from) => {
    const file = path.join(from, "package-lock.json");
    return existsSync(file) ? readFileSync(file, "utf8") : "";
  };
  if (lock(copy) !== lock(frontend))
    console.warn(
      `Note: ${folder}/package-lock.json differs at ${sha.slice(0, 7)}; it is served with the working tree's dependencies.`,
    );
  return { root: copy, allow: [dir] };
}

function findChrome(choice) {
  const isFile = (file) => {
    try {
      return statSync(file).isFile();
    } catch {
      return false;
    }
  };
  for (const [file, source] of [
    [choice, "--chrome"],
    [process.env.CHROME_PATH, "CHROME_PATH"],
  ])
    if (file) {
      if (!existsSync(file)) throw new Failure(`${source} names ${file}, which does not exist`);
      if (!isFile(file)) throw new Failure(`${source} names ${file}, which is a folder, not the browser's program`);
      return file;
    }
  const found = (CHROMES[process.platform] ?? []).find(isFile);
  if (!found) throw new Failure("No Chrome found. Pass --chrome <path> or set CHROME_PATH.");
  return found;
}

/**
 * Starts Chrome with the DevTools protocol on a pipe. Returns the protocol's
 * commands and events, and `stop`, which closes the browser and resolves once it
 * has exited.
 */
function startChrome(binary, profile) {
  const chrome = spawn(binary, [...CHROME_FLAGS, `--user-data-dir=${profile}`, "about:blank"], {
    stdio: ["ignore", "ignore", "pipe", "pipe", "pipe"],
  });
  const pending = new Map();
  const listeners = new Set();
  let next = 0;
  let broken;
  let output = "";
  const exited = new Promise((done) =>
    chrome.once("exit", (code, signal) => done(signal ? `was stopped by ${signal}` : `exited with code ${code}`)),
  );
  const fail = (reason) => {
    broken ??= new Failure(reason);
    for (const call of pending.values()) call.reject(broken);
    pending.clear();
  };
  chrome.once("error", (error) => fail(`Could not start ${binary}: ${error.message}`));
  chrome.stderr.setEncoding("utf8");
  chrome.stderr.on("data", (chunk) => (output = (output + chunk).slice(-4000)));
  const [, , , commands, events] = chrome.stdio;
  // Writing to a browser that has gone fails here; its reason comes with `events`.
  commands.on("error", () => {});
  events.setEncoding("utf8");
  let parts = [];
  events.on("data", (chunk) => {
    let start = 0;
    for (let end = chunk.indexOf("\0"); end !== -1; end = chunk.indexOf("\0", start)) {
      parts.push(chunk.slice(start, end));
      start = end + 1;
      const message = JSON.parse(parts.join(""));
      parts = [];
      const call = pending.get(message.id);
      if (!call) for (const listener of listeners) listener(message);
      else {
        pending.delete(message.id);
        if (message.error) call.reject(new Failure(`${call.method}: ${message.error.message}`));
        else call.resolve(message.result);
      }
    }
    if (start < chunk.length) parts.push(chunk.slice(start));
  });
  events.on("close", async () => {
    const how = await Promise.race([exited, sleep(1000).then(() => "closed its connection")]);
    const last = output.trim().split("\n").slice(-2).join(" / ");
    fail(`Chrome ${how}${last ? `. Its last output: ${last}` : ""}`);
  });
  return {
    send(method, params = {}, sessionId) {
      if (broken) return Promise.reject(broken);
      return new Promise((resolve, reject) => {
        const id = ++next;
        pending.set(id, { resolve, reject, method });
        commands.write(`${JSON.stringify({ id, method, params, ...(sessionId ? { sessionId } : {}) })}\0`);
      });
    },
    listen: (listener) => listeners.add(listener),
    async stop() {
      if (chrome.pid === undefined || chrome.exitCode !== null || chrome.signalCode !== null) return;
      chrome.kill();
      // A page stuck in a loop can keep the browser from closing.
      if (!(await Promise.race([exited.then(() => true), sleep(5000)]))) {
        chrome.kill("SIGKILL");
        await Promise.race([exited, sleep(2000)]);
      }
    },
  };
}

/** Opens a tab whose clock stands still, and returns what the captures need of it. */
async function openPage(browser) {
  const { targetId } = await browser.send("Target.createTarget", { url: "about:blank" });
  const { sessionId } = await browser.send("Target.attachToTarget", { targetId, flatten: true });
  const send = (method, params) => browser.send(method, params, sessionId);
  const loads = new Set();
  const requests = new Set();
  const idlers = new Set();
  const errors = [];
  browser.listen(({ method, params, sessionId: from }) => {
    if (from !== sessionId) return;
    if (method === "Runtime.exceptionThrown") errors.push(exception(params.exceptionDetails));
    else if (method === "Page.loadEventFired") for (const done of [...loads]) done();
    else if (method === "Network.requestWillBeSent") requests.add(params.requestId);
    else if (method === "Network.loadingFinished" || method === "Network.loadingFailed") {
      requests.delete(params.requestId);
      if (!requests.size) for (const done of [...idlers]) done(true);
    }
  });
  for (const method of ["Page.enable", "Runtime.enable", "Network.enable"]) await send(method);
  await send("Page.addScriptToEvaluateOnNewDocument", { source: FREEZE });
  await send("Emulation.setTimezoneOverride", { timezoneId: "UTC" });
  await send("Emulation.setLocaleOverride", { locale: "en-US" });

  const evaluate = async (expression) => {
    const { result, exceptionDetails } = await within(
      send("Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true }),
      "The preview",
    );
    if (exceptionDetails) throw new Failure(`Could not drive the preview: ${exception(exceptionDetails)}`);
    return result.value;
  };
  /** Resolves to true once nothing is loading, or to false after `ms`. */
  const idle = (ms) =>
    new Promise((resolve) => {
      if (!requests.size) return resolve(true);
      let timer;
      const done = (value) => {
        clearTimeout(timer);
        idlers.delete(done);
        resolve(value);
      };
      timer = setTimeout(() => done(false), ms);
      idlers.add(done);
    });
  /**
   * Waits until nothing is loading and the document stays the same for two
   * frames; resolves to whether it did. A lazily loaded part, such as the
   * shortcuts dialog, can take longer to arrive than two frames.
   */
  async function settle() {
    for (let round = 0; round < 5; round++) {
      const quiet = await evaluate(SETTLE);
      if (!requests.size) return quiet;
      if (!(await idle(10000))) return false;
    }
    return false;
  }

  return {
    errors,
    evaluate,
    settle,
    /** Loads the preview afresh and lets it settle; resolves to whether it did. */
    async load(url) {
      requests.clear();
      let done;
      const loaded = new Promise((resolve) => loads.add((done = resolve)));
      try {
        const { errorText } = await send("Page.navigate", { url });
        if (errorText) throw new Failure(`Could not load ${url}: ${errorText}`);
        await within(loaded, `Loading ${url}`);
      } finally {
        loads.delete(done);
      }
      if (!(await evaluate(READY))) throw new Failure(`The preview at ${url} did not render`);
      return settle();
    },
    async size({ width, height, touch }, scheme) {
      await send("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: touch });
      await send("Emulation.setTouchEmulationEnabled", { enabled: touch, ...(touch ? { maxTouchPoints: 5 } : {}) });
      await send("Emulation.setEmulatedMedia", {
        features: [
          { name: "prefers-color-scheme", value: scheme },
          { name: "prefers-reduced-motion", value: "reduce" },
        ],
      });
    },
    async capture({ width }, fullPage) {
      const params = { format: "png" };
      if (fullPage) {
        const { cssContentSize } = await send("Page.getLayoutMetrics");
        params.captureBeyondViewport = true;
        params.clip = { x: 0, y: 0, width, height: Math.ceil(cssContentSize.height), scale: 1 };
      }
      const { data } = await within(send("Page.captureScreenshot", params), "The screenshot");
      return Buffer.from(data, "base64");
    },
  };
}

/** Serves a version's preview with the working tree's Vite, without watching files or HMR. */
async function serve({ root, allow }, cacheDir) {
  const server = await createServer({
    root,
    // Loads the version's own config without writing a bundled copy beside it.
    configLoader: "runner",
    cacheDir,
    clearScreen: false,
    logLevel: "error",
    optimizeDeps: { noDiscovery: true, include: [] },
    plugins: [
      {
        name: "preview-snapshots",
        // Merging the inline config drops null values, so the watcher is turned
        // off here.
        config(config) {
          config.server = { ...config.server, watch: null };
        },
      },
    ],
    server: {
      host: "127.0.0.1",
      port: 0,
      strictPort: true,
      hmr: false,
      ws: false,
      fs: { allow: [...allow, realpathSync(path.join(frontend, "node_modules")), cacheDir] },
    },
  });
  await server.listen();
  const { port } = server.httpServer.address();
  return { url: `http://127.0.0.1:${port}/preview.html`, close: () => server.close() };
}

/**
 * Captures one version's views into `dir`. Returns the files by scheme and name,
 * the views it could not show, those whose page threw and those still changing
 * when captured.
 */
async function captureVersion(page, version, cacheDir, dir, { schemes, only }) {
  const server = await serve(version, cacheDir);
  try {
    await page.size(SIZES[0], schemes[0]);
    await page.load(server.url);
    // The states come from the preview, so a new state is captured once it exists.
    const scenarios = await page.evaluate(SCENARIOS);
    const views = [
      ...SIZES.flatMap((size) =>
        scenarios.map((scenario) => ({ size, scenario, name: `${size.name}-${scenario}` })),
      ),
      ...EXTRAS.map((extra) => ({
        ...extra,
        size: SIZES.find((size) => size.name === extra.size),
        name: `${extra.size}-${extra.scenario}-${extra.name}`,
      })),
    ].filter((view) => !only || view.name.includes(only));
    const captured = { files: new Map(), missing: new Set(), failed: new Map(), unsettled: [] };
    for (const scheme of schemes) {
      mkdirSync(path.join(dir, scheme), { recursive: true });
      for (const size of SIZES) {
        await page.size(size, scheme);
        for (const view of views.filter((candidate) => candidate.size === size)) {
          const name = `${scheme}/${view.name}`;
          try {
            page.errors.length = 0;
            let settled = await page.load(server.url);
            if (!(await page.evaluate(choose(view.scenario)))) {
              captured.missing.add(name);
              continue;
            }
            settled = (await page.settle()) && settled;
            if (view.click) {
              if (!(await page.evaluate(click(view.click)))) {
                captured.missing.add(name);
                continue;
              }
              settled = (await page.settle()) && settled;
            }
            await page.evaluate(FINISH);
            if (page.errors.length) {
              captured.failed.set(name, page.errors.join("; "));
              continue;
            }
            if (!settled) captured.unsettled.push(name);
            const file = path.join(dir, scheme, `${view.name}.png`);
            writeFileSync(file, await page.capture(size, size.fullPage && !view.click));
            captured.files.set(name, file);
          } catch (error) {
            if (error instanceof Failure) error.message = `${name}: ${error.message}`;
            throw error;
          }
        }
      }
    }
    return captured;
  } finally {
    await server.close();
  }
}

const escape = (text) =>
  String(text).replace(/[&<>"]/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]);

function describe(result, tolerance) {
  switch (result.status) {
    case "added":
      return "only in the head";
    case "removed":
      return "only in the base";
    case "missing":
      return "could not be shown in either version";
    case "error":
      return Object.entries(result.errors)
        .map(([side, error]) => `the ${side} threw ${error}`)
        .join("; ");
  }
  const { differing, over, total, maxDelta, box } = result;
  if (!differing) return "identical";
  const share = differing / total < 0.0001 ? "under 0.01%" : `${((differing / total) * 100).toFixed(2)}%`;
  return `${plural(differing, "pixel")} (${share}) differ by up to ${plural(maxDelta, "level")}, ${count(over)} by more than ${tolerance}, within ${box.left},${box.top} to ${box.right},${box.bottom}`;
}

function report(out, labels, { tolerance, area }, results) {
  const differing = results.filter((result) => DIFFERS.has(result.status));
  const names = (status) => results.filter((result) => result.status === status).map((result) => result.view);
  const figure = (src, caption) =>
    src
      ? `<figure><a href="${escape(src)}"><img src="${escape(src)}" alt="${escape(caption)}"></a><figcaption>${escape(caption)}</figcaption></figure>`
      : "";
  const sections = differing
    .map(
      (result) => `<section>
<h2>${escape(result.view)}: ${escape(describe(result, tolerance))}</h2>
${figure(result.base, `Base, ${labels.base}`)}${figure(result.head, `Head, ${labels.head}`)}${figure(result.diff, `Red: over ${tolerance} levels. Amber: within them.`)}
</section>`,
    )
    .join("\n");
  const missing = names("missing");
  writeFileSync(
    path.join(out, "index.html"),
    `<!doctype html>
<html lang="en">
<meta charset="utf-8">
<title>Preview snapshots</title>
<style>
body { font: 14px/1.4 system-ui, sans-serif; margin: 24px; color: #182d35; }
h2 { font-size: 15px; margin: 24px 0 8px; }
figure { display: inline-block; margin: 0 12px 12px 0; vertical-align: top; }
img { display: block; max-width: 440px; border: 1px solid #c8d2d4; }
figcaption { font-size: 12px; color: #596b70; }
</style>
<h1>Preview snapshots</h1>
<p>Base: ${escape(labels.base)}. Head: ${escape(labels.head)}. A view differs when a pixel differs by more than ${tolerance} levels in a colour channel, or when more than ${count(area)} pixels differ at all. ${differing.length} of ${plural(results.length, "view")} ${differing.length === 1 ? "differs" : "differ"}.</p>
${sections}
${missing.length ? `<h2>Not shown in either version</h2>\n<p>${missing.map(escape).join(", ")}</p>\n` : ""}<h2>Unchanged</h2>
<p>${names("same").map(escape).join(", ") || "None"}</p>
</html>
`,
  );
  writeFileSync(
    path.join(out, "report.json"),
    `${JSON.stringify({ base: labels.base, head: labels.head, tolerance, area, views: results }, null, 2)}\n`,
  );
}

/** Empties the output folder, refusing a folder this script did not make. */
function prepareOut(out) {
  if (existsSync(out) && !statSync(out).isDirectory()) throw new Failure(`${out} is not a folder`);
  const marker = path.join(out, ".preview-snapshots");
  if (existsSync(out) && readdirSync(out).length && !existsSync(marker))
    throw new Failure(`${out} is not empty and was not made by this script`);
  rmSync(out, { recursive: true, force: true });
  mkdirSync(out, { recursive: true });
  writeFileSync(marker, "Made by frontend/scripts/snapshots.mjs; emptied on each run.\n");
}

/** What a run has to clear away, however it ends. */
const run = { scratch: undefined, browser: undefined };

async function main() {
  const { values } = parse({
    options: {
      base: { type: "string", default: "HEAD" },
      head: { type: "string" },
      scheme: { type: "string" },
      only: { type: "string" },
      // From run to run, Chrome draws a large blurred shadow or a gradient a level
      // differently in up to about 750 pixels of a view, and an anti-aliased
      // corner up to three levels differently. The eye can see neither.
      tolerance: { type: "string", default: "3" },
      area: { type: "string", default: "1000" },
      out: { type: "string", default: ".snapshots" },
      chrome: { type: "string" },
      help: { type: "boolean", short: "h" },
    },
  });
  if (values.help) {
    console.log(USAGE);
    return 0;
  }
  const whole = (text) => (/^\d+$/.test(text) ? Number(text) : NaN);
  const tolerance = whole(values.tolerance);
  if (!(tolerance <= 254)) throw new Failure("--tolerance takes a whole number of levels from 0 to 254");
  const area = whole(values.area);
  if (!(area >= 0)) throw new Failure("--area takes a whole number of pixels");
  if (values.scheme && !["light", "dark"].includes(values.scheme))
    throw new Failure("--scheme takes light or dark");
  const schemes = values.scheme ? [values.scheme] : ["light", "dark"];

  const root = git(["rev-parse", "--show-toplevel"]).toString().trim();
  const folder = path.relative(root, frontend);
  const base = commit(values.base);
  const head = values.head ? commit(values.head) : undefined;
  for (const sha of [base, head])
    if (sha && !exists(sha, `${folder}/preview.html`))
      throw new Failure(`${sha.slice(0, 7)} has no ${folder}/preview.html`);
  const label = (ref, sha) => (sha.startsWith(ref) ? sha.slice(0, 7) : `${ref} (${sha.slice(0, 7)})`);
  const labels = {
    base: label(values.base, base),
    head: head ? label(values.head, head) : "the working tree",
  };
  const binary = findChrome(values.chrome);
  const out = path.resolve(frontend, values.out);
  prepareOut(out);

  // Vite compares real paths, and macOS's temporary folder is reached by a link.
  const scratch = realpathSync(mkdtempSync(path.join(tmpdir(), "preview-snapshots-")));
  run.scratch = scratch;
  const versions = {
    base: extract(root, folder, base, path.join(scratch, "base")),
    head: head
      ? extract(root, folder, head, path.join(scratch, "head"))
      : {
          root: frontend,
          allow: [frontend, ...OUTSIDE.map((file) => path.join(root, file)).filter((file) => existsSync(file))],
        },
  };
  console.log(`Preview snapshots: ${labels.head} against ${labels.base}`);
  const captured = {};
  for (const side of ["base", "head"]) {
    const started = Date.now();
    // Each version gets a browser of its own: one that has already drawn a
    // version can draw the next a level differently in a few pixels.
    const browser = startChrome(binary, path.join(scratch, `profile-${side}`));
    run.browser = browser;
    try {
      await within(browser.send("Browser.getVersion"), "Starting Chrome");
      captured[side] = await captureVersion(
        await openPage(browser),
        versions[side],
        path.join(scratch, `vite-${side}`),
        path.join(out, side),
        { schemes, only: values.only },
      );
    } catch (error) {
      if (error instanceof Failure) error.message = `Capturing ${labels[side]}: ${error.message}`;
      throw error;
    } finally {
      await browser.stop();
      run.browser = undefined;
    }
    const { files, unsettled } = captured[side];
    console.log(
      `Captured ${plural(files.size, "view")} of ${labels[side]} in ${((Date.now() - started) / 1000).toFixed(0)} s`,
    );
    if (unsettled.length)
      console.log(`  Still changing when captured, so these may differ by chance: ${unsettled.join(", ")}`);
  }

  const names = new Set();
  for (const { files, missing, failed } of Object.values(captured))
    for (const name of [...files.keys(), ...missing, ...failed.keys()]) names.add(name);
  if (!names.size)
    throw new Failure(values.only ? `No view's name contains "${values.only}"` : "The preview has no states");
  const results = [];
  for (const view of [...names].sort()) {
    const file = (side) => captured[side].files.get(view);
    const result = {
      view,
      base: file("base") && path.relative(out, file("base")),
      head: file("head") && path.relative(out, file("head")),
    };
    const errors = Object.fromEntries(
      ["base", "head"].filter((side) => captured[side].failed.has(view)).map((side) => [side, captured[side].failed.get(view)]),
    );
    if (Object.keys(errors).length) Object.assign(result, { status: "error", errors });
    else if (!result.base && !result.head) result.status = "missing";
    else if (!result.base) result.status = "added";
    else if (!result.head) result.status = "removed";
    else {
      const compared = compareImages(
        decodePng(readFileSync(file("base"))),
        decodePng(readFileSync(file("head"))),
        tolerance,
      );
      Object.assign(result, {
        status: compared.over || compared.differing > area ? "changed" : "same",
        differing: compared.differing,
        over: compared.over,
        total: compared.width * compared.height,
        maxDelta: compared.maxDelta,
        box: compared.box,
      });
      if (result.status === "changed") {
        result.diff = path.join("diff", `${view}.png`);
        mkdirSync(path.dirname(path.join(out, result.diff)), { recursive: true });
        writeFileSync(path.join(out, result.diff), encodePng(compared.image));
      }
    }
    results.push(result);
  }
  report(out, labels, { tolerance, area }, results);
  const differing = results.filter((result) => DIFFERS.has(result.status));
  for (const result of differing) console.log(`  ${result.view}: ${describe(result, tolerance)}`);
  const missing = results.filter((result) => result.status === "missing").map((result) => result.view);
  if (missing.length) console.log(`Not shown in either version: ${missing.join(", ")}`);
  const index = path.join(out, "index.html");
  const shown = path.relative(process.cwd(), index);
  console.log(
    `${differing.length} of ${plural(results.length, "view")} ${differing.length === 1 ? "differs" : "differ"}. Report: ${shown.startsWith("..") ? index : shown}`,
  );
  return differing.length ? 1 : 0;
}

let ending = false;

/** Closes the browser and removes the temporary folder, then exits with `code`. */
async function end(code, error) {
  if (ending) return;
  ending = true;
  if (error) console.error(error instanceof Failure ? error.message : error);
  await run.browser?.stop();
  if (run.scratch) rmSync(run.scratch, { recursive: true, force: true, maxRetries: 5 });
  process.exit(code);
}

// A closed pipe, as with `| head`, must not stop the run halfway.
process.stdout.on("error", () => {});
for (const [signal, code] of [
  ["SIGINT", 130],
  ["SIGTERM", 143],
])
  process.once(signal, () => end(code));
process.on("uncaughtException", (error) => end(2, error));
process.on("unhandledRejection", (error) => end(2, error));
main().then(
  (code) => end(code),
  (error) => end(2, error),
);
