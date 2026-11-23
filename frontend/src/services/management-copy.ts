import type { ManagementErrorCode, TestResult } from "./contracts";
import { errorCopy } from "./copy";
// Plain copy about the camera. Its keys are also the management codes the API
// accepts from the server, so every code keeps an entry.
export const managementCopy: Partial<Record<ManagementErrorCode, string>> = {
  authentication_failed: "The camera rejected the username or password. Check them and try again.",
  source_unavailable: "The server couldn’t receive video from the camera. Check that the camera is running, that the server can reach its address, and that the username and password are right.",
  camera_unreachable: "The server couldn’t reach the camera. Check that the camera is running and that the server can reach its address and port.",
  stream_unavailable: "The camera isn’t sending video. Check the path in the address and that the camera is streaming.",
  media_connection_failed: "This browser couldn’t receive the preview. Check that it can reach the server’s WebRTC media address and ports, then try again.",
  signaling_failed: "The preview couldn’t start. Check the address, the sign-in and the camera’s video format, then try again.",
  deadline_exceeded: "The check took too long. Check that the camera is running, its sign-in and the network, then try again.",
  access_denied: "You no longer have permission to manage this camera.",
  recovery_storage_unavailable: "Allow site storage for this page, then open Add Camera or the camera’s Settings again.",
  draft_lost: "The server restarted. Enter or import the camera again.",
  draft_expired: "This setup expired. Enter or import the camera again.",
  test_expired: "Check this camera again before saving.",
  draft_revision_conflict: "These details changed in another tab. Review them before continuing.",
  catalog_revision_conflict: "The camera list changed. Review the details and save again.",
  duplicate_camera: "Another camera, or a check in progress, already uses this address.",
  credentials_required: "Enter both the username and password.",
  credentials_changed: "The saved camera address or sign-in changed. Close Settings and open it again to start from the current settings.",
  import_invalid: "Choose the .yaml file the Camstream Mac app exports and, optionally, its .env file.",
  destination_denied: "The server doesn’t allow this camera address.",
  address_unavailable: "The camera address couldn’t be found. Check it and try again.",
  identity_unverified: "A saved camera’s address couldn’t be confirmed. Try again once its name resolves.",
  cleanup_incomplete: "The preview is still closing. Wait a moment, then try again.",
  save_outcome_unknown: "Your camera may already be saved. Check again before making changes.",
  storage_unavailable: "The server couldn’t write to camera storage. Try again in a moment.",
  playback_evidence_unavailable: "This browser can’t confirm that the preview plays. Try another browser; saving stays off until then.",
  playback_evidence_insufficient: "The preview hasn’t shown moving video yet.",
  recovery_lost: "The server restarted or the reconnect record expired. Refresh source status before explicitly reconnecting again.",
  operation_in_progress: "A change is still in progress. Try again in a moment.",
  receipt_expired: "The earlier change can’t be confirmed anymore. Check the camera list before trying again.",
};
/** Notes on a draft's address and files. */
export const draftWarnings = {
  server_relative_localhost: "Localhost is the server’s own machine. A remote or container server needs your Mac’s network address and Private network capture.",
  imported_grants_ignored: "Sharing settings in the file are not imported.",
  initial_address_only: "Use a camera you trust: the server checks only the address it first connects to and follows the camera’s redirects.",
  creator_only_access: "",
  fixed_address: "Reconnect does not follow the camera to a new address. Change the address in the camera’s Settings instead.",
};
export function managementMessage(code: ManagementErrorCode) {
  return (Object.prototype.hasOwnProperty.call(managementCopy,code)?managementCopy[code]:undefined)
    ?? (Object.prototype.hasOwnProperty.call(errorCopy,code)?errorCopy[code as keyof typeof errorCopy].detail:undefined)
    ?? "Something went wrong. Check the camera and try again.";
}

export function managementTestMessage(test: TestResult) {
  if (test.error?.code === "test_expired" && !test.browser_frames_reported) {
    return test.source_media_observed
      ? "The camera’s video reached the server, but this browser didn’t play it before the check timed out. Check the server’s WebRTC media address and ports. Firefox may reject localhost media candidates: start the Mac development server with --media-address and this Mac’s private IPv4, then check again."
      : "The check timed out before the camera sent video. Check that the camera is running, its address and its sign-in, then check again.";
  }
  return test.error ? managementMessage(test.error.code) : "";
}
