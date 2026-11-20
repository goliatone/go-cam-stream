import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import test from "node:test";
import { stopProcessGroup } from "./process-group.mjs";

test("reaps a descendant after its process group leader exits", { timeout: 10000 }, async () => {
  const leader = spawn("/bin/sh", ["-c", `
    /bin/sh -c 'trap "" TERM; echo "$$"; exec /bin/sleep 10' &
    exit 1
  `], { detached: true, stdio: ["ignore", "pipe", "ignore"] });
  const exited = once(leader, "exit");
  try {
    const [data] = await once(leader.stdout, "data");
    const descendant = Number(String(data).trim());
    assert(descendant > 1);
    assert.deepEqual(await exited, [1, null]);
    assert(process.kill(descendant, 0), "controlled descendant should still be running");

    await stopProcessGroup(leader.pid);

    assert.throws(() => process.kill(-leader.pid, 0), { code: "ESRCH" });
    assert.throws(() => process.kill(descendant, 0), { code: "ESRCH" });
  } finally {
    if (leader.pid) {
      try {
        process.kill(-leader.pid, "SIGKILL");
      } catch (error) {
        if (error.code !== "ESRCH") throw error;
      }
    }
  }
});
