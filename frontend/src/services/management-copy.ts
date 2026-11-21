import type { ManagementErrorCode, TestResult } from "./contracts";
import { errorCopy } from "./copy";
export const managementCopy: Partial<Record<ManagementErrorCode, string>> = {
  authentication_failed: "The camera rejected its credentials. Check the username and password.",
  source_unavailable: "The server could not receive camera video. Check that capture is running, the RTSP address is reachable from the server, and the camera credentials are provided and correct.",
  camera_unreachable: "The server could not reach the camera. Check that capture is running and the RTSP address and port are reachable from the server.",
  stream_unavailable: "The camera stream is unavailable. Check the RTSP path and that the camera is publishing video.",
  media_connection_failed: "The browser could not receive preview video. Check connectivity to the server’s WebRTC media address and ports, then test again.",
  signaling_failed: "The preview connection could not be negotiated. Check the camera address, credentials and supported video codec, then test again.",
  deadline_exceeded: "The connection test timed out. Check capture, camera credentials and connectivity, then test again.",
  access_denied: "You no longer have permission to manage this camera.",
  recovery_storage_unavailable: "Enable site storage in this browser, then reopen Add Camera. Changes are disabled until recovery can survive a refresh.",
  draft_lost: "The server restarted and this unsaved draft was lost. Start again and re-enter or import the camera.",
  draft_expired: "This draft expired. Start again and re-enter or import the camera.",
  test_expired: "Test this camera again before saving.",
  draft_revision_conflict: "The draft changed. Check its latest details before continuing.",
  catalog_revision_conflict: "The camera list changed. Refresh it and explicitly try Save again after checking this draft.",
  duplicate_camera: "This address overlaps a configured camera or another active test.",
  credentials_required: "Enter both the camera username and password.",
  credentials_changed: "The saved credentials changed. Close this draft and start a new update.",
  import_invalid: "Choose one current Mac camera YAML export and, optionally, its matching credentials file.",
  destination_denied: "This camera address is not allowed by the server.",
  address_unavailable: "The camera address could not be resolved. Check it and try again.",
  identity_unverified: "An existing camera address could not be verified. Try again when its name resolves.",
  cleanup_incomplete: "The preview has not finished closing. Check its status before trying again or saving.",
  save_outcome_unknown: "Save may have completed. Check the original operation before making another change.",
  storage_unavailable: "Private camera storage is unavailable. Check the operation before trying again.",
  playback_evidence_unavailable: "This browser cannot verify decoded frames. Try a supported browser; Save remains disabled.",
  playback_evidence_insufficient: "The preview has not shown advancing video frames yet.",
  recovery_lost: "The server restarted or the reconnect record expired. Refresh source status before explicitly reconnecting again.",
  operation_in_progress: "The current change is still being processed. Check its status.",
  receipt_expired: "Resolve the original saved camera before starting another Save.",
};
export function managementMessage(code: ManagementErrorCode) {
  return (Object.prototype.hasOwnProperty.call(managementCopy,code)?managementCopy[code]:undefined)
    ?? (Object.prototype.hasOwnProperty.call(errorCopy,code)?errorCopy[code as keyof typeof errorCopy].detail:undefined)
    ?? "The camera operation did not complete. Check the camera and the operation status before trying again.";
}

export function managementTestMessage(test: TestResult) {
  if (test.error?.code === "test_expired" && !test.browser_frames_reported) {
    return test.source_media_observed
      ? "Camera video reached the server, but browser playback was not verified before the test timed out. Check the server’s WebRTC media address and ports. Firefox may reject localhost media candidates. Start the Mac development server with --media-address and this Mac’s private IPv4, then test again."
      : "The test timed out before camera video was verified. Check capture, the camera address and credentials, then test again.";
  }
  return test.error ? managementMessage(test.error.code) : "";
}

