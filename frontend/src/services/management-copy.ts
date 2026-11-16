import type { ManagementErrorCode } from "./contracts";
export const managementCopy: Partial<Record<ManagementErrorCode, string>> = {
  access_denied: "You no longer have permission to manage this camera.",
  recovery_storage_unavailable: "Enable site storage in this browser, then reopen Add Camera. Changes are disabled until recovery can survive a refresh.",
  draft_lost: "The server restarted and this unsaved draft was lost. Start again and re-enter or import the camera.",
  draft_expired: "This draft expired. Start again and re-enter or import the camera.",
  test_expired: "Test this camera again before saving.",
  draft_revision_conflict: "The draft changed. Check its latest details before continuing.",
  catalog_revision_conflict: "The camera list changed. Refresh it and explicitly try Save again after checking this draft.",
  duplicate_camera: "This address overlaps a configured camera or another active test. Use the existing camera or finish the other test.",
  credentials_required: "Enter both the camera username and password.",
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
  return managementCopy[code] ?? "The camera operation did not complete. Check the camera and the operation status before trying again.";
}

