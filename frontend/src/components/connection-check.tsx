import type { Ref } from "preact";
import type { ErrorCode, TestResult } from "../services/contracts";
import { managementTestMessage } from "../services/management-copy";
import { ErrorDetails } from "./status";

const ROWS = ["Reaching the camera", "Receiving video", "Playing in this browser"];
// Failures that stop the check before the camera is reached, and those in the
// browser's own playback. Any other failure means no video arrived.
const REACH: readonly string[] = ["authentication_failed", "camera_unreachable", "destination_denied", "address_unavailable", "identity_unverified", "duplicate_camera", "capacity_exceeded", "camera_busy", "signaling_failed"];
const PLAY: readonly string[] = ["media_connection_failed", "playback_evidence_unavailable", "playback_evidence_insufficient"];

/**
 * A connection check: a mark for each stage above the muted preview, and what
 * to fix when one fails. A row is done only once the server reports camera
 * video, or the check passed; the row a failure points at is failed, and the
 * rest stay waiting.
 */
export function ConnectionCheck({ test, active, video, hidden }: {
  test: TestResult | undefined;
  /** A check is running or still closing. */
  active: boolean;
  video: Ref<HTMLVideoElement>;
  /** Kept out of view, with its preview, until a check starts. */
  hidden?: boolean;
}) {
  const code = test?.error?.code ?? "";
  const media = test?.source_media_observed;
  const failed = !code ? -1 : media || PLAY.includes(code) ? 2 : REACH.includes(code) ? 0 : 1;
  const marks = ROWS.map((_, i) => test?.stage === "passed" || media && i < 2 ? "done" : i === failed ? "failed"
    : failed < 0 && active && i === (media ? 2 : 0) ? "checking" : "waiting");
  return <section class="management-preview" aria-label="Connection check" hidden={hidden}>
    <ol class="checklist">{ROWS.map((row, i) => <li key={row} data-mark={marks[i]}>{row}<span class="sr-only">: {marks[i]}</span></li>)}</ol>
    <video ref={video} muted playsInline aria-label="Camera preview" />
    {test?.error && <div class="management-error">
      <p role="alert">{managementTestMessage(test)}</p>
      <details class="disclosure"><summary>Details</summary><ErrorDetails code={test.error.code as ErrorCode} where={[ROWS[failed] ?? ""]} /></details>
    </div>}
    {test?.stage === "canceled" && !test.error && <p>The check stopped before it finished.</p>}
  </section>;
}
