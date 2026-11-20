// @vitest-environment node
import { mkdtemp, mkdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { PAGE_CSP, validateShareArtifact } from "./share-artifact.mjs";

const page = (head = "") => `<!doctype html>
<html lang="en">
  <head>
    <meta charset="UTF-8" />
    <meta http-equiv="Content-Security-Policy" content="${PAGE_CSP}" />
    <link rel="icon" href="/share-assets/guest-c.svg" />
    <script type="module" crossorigin src="/share-assets/guest-a.js"></script>
    <link rel="stylesheet" crossorigin href="/share-assets/guest-b.css">
    ${head}
  </head>
  <body><div id="guest"></div></body>
</html>`;
const files = () => ({
  "index.html": page(),
  "assets/guest-a.js": 'document.querySelector("#guest").textContent = "ok";',
  "assets/guest-b.css": ".guest { color: red; }",
  "assets/guest-c.svg": '<svg xmlns="http://www.w3.org/2000/svg"></svg>',
});
const dirs = [];
async function check(changes) {
  const dir = await mkdtemp(join(tmpdir(), "share-artifact-"));
  dirs.push(dir);
  await mkdir(join(dir, "assets"));
  for (const [name, text] of Object.entries({ ...files(), ...changes }))
    if (text !== undefined) await writeFile(join(dir, name), text);
  return (await validateShareArtifact(dir)).problems;
}
afterEach(async () => {
  await Promise.all(dirs.splice(0).map((dir) => rm(dir, { recursive: true, force: true })));
});

describe("guest artifact validation", () => {
  it("accepts the listener's layout and policy", async () => {
    expect(await check({})).toEqual([]);
  });

  it.each([
    ["a data: URI in a script", { "assets/guest-a.js": 'new Image().src = "data:image/png;base64,AA";' }, /data: URI/],
    ["a page without its module script", { "index.html": page().replace(/<script[^>]*><\/script>/, "") }, /0 module scripts/],
    ["a single-quoted external stylesheet", { "index.html": page("<link rel='stylesheet' href='https://cdn.example/x.css'>") }, /not a served guest asset/],
    ["an unquoted external image", { "index.html": page("<img src=https://cdn.example/x.png>") }, /not a served guest asset/],
    ["a weakened page policy", { "index.html": page().replace("style-src 'self'", "style-src 'self' 'unsafe-inline'") }, /content security policy/],
    ["an unreferenced script", { "assets/guest-d.js": "console.log(1);" }, /guest-d\.js is never referenced/],
    ["an inline style attribute", { "index.html": page('<div style="color:red"></div>') }, /inline style attribute/],
    ["an inline event handler", { "index.html": page("<img src=/share-assets/guest-c.svg onerror=alert(1)>") }, /inline event handler/],
    ["an inline script", { "index.html": page("<script>alert(1)</script>") }, /inline script/],
    ["browser storage", { "assets/guest-a.js": 'localStorage.setItem("k", "v");' }, /uses localStorage/],
    ["an unexpected top-level file", { "notes.txt": "x" }, /unexpected top-level entry/],
    ["an asset name the listener refuses", { "assets/guest.a.js": "1" }, /does not match the served asset names/],
  ])("rejects %s", async (_name, changes, problem) => {
    expect((await check(changes)).join("\n")).toMatch(problem);
  });

  it("follows assets referenced from the page's scripts", async () => {
    expect(
      await check({
        "assets/guest-a.js": 'import("/share-assets/guest-d.js");',
        "assets/guest-d.js": "export const ok = 1;",
      }),
    ).toEqual([]);
  });
});
