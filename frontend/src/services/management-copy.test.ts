import { expect, it } from "vitest";
import type { ManagementErrorCode } from "./contracts";
import { errorCopy } from "./copy";
import { managementMessage } from "./management-copy";

it("shows reviewed guidance for every accepted media error", () => {
  for (const code of Object.keys(errorCopy) as (keyof typeof errorCopy)[]) {
    expect(managementMessage(code)).not.toContain("The camera operation did not complete");
  }
});

it("does not infer authentication failure from an unavailable source", () => {
  expect(managementMessage("source_unavailable")).toContain("username and password");
  expect(managementMessage("source_unavailable")).not.toContain("rejected");
  expect(managementMessage("media_connection_failed")).toContain("WebRTC media address and ports");
});

it("keeps unknown and inherited keys behind the safe fallback", () => {
  for (const code of ["constructor", "__proto__", "raw backend secret"])
    expect(managementMessage(code as ManagementErrorCode)).toBe("Something went wrong. Check the camera and try again.");
});
