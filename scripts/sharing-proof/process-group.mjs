import { setTimeout as delay } from "node:timers/promises";

function signalGroup(pid, signal) {
  try {
    process.kill(-pid, signal);
    return true;
  } catch (error) {
    if (error.code === "ESRCH") return false;
    throw error;
  }
}

async function waitForGroupExit(pid) {
  const deadline = performance.now() + 2000;
  while (signalGroup(pid, 0)) {
    if (performance.now() >= deadline) return false;
    await delay(25);
  }
  return true;
}

export async function stopProcessGroup(pid) {
  if (pid === undefined) return; // spawn failed before allocating a process group
  if (!Number.isSafeInteger(pid) || pid <= 1) throw new Error("Invalid proof process group");

  // Each detached child leads its own group. The group can outlive the child.
  if (!signalGroup(pid, "SIGTERM") || await waitForGroupExit(pid)) return;
  if (!signalGroup(pid, "SIGKILL") || await waitForGroupExit(pid)) return;
  throw new Error("An owned proof process group is still running");
}
