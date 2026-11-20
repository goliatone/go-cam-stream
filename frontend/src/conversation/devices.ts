import type { InputKind } from "../services/conversation-media";
import type { MenuEntry } from "./overlays";

export type InputChoice = { device: string; selected: boolean };
export type Choices = Record<InputKind, InputChoice>;
/** Why an input is off although it was asked for. */
export type InputFailure = "denied" | "unavailable" | "busy" | "capture_busy" | "capture_unavailable" | "lost" | "other";
export type InputErrors = Partial<Record<InputKind, InputFailure>>;
export type Pending = Partial<Record<InputKind, boolean>>;

export const inputKinds = ["video", "audio"] as const;
/** The order of the dock, which leads with the microphone. */
export const dockKinds = ["audio", "video"] as const;
export const inputNames: Record<InputKind, string> = { video: "camera", audio: "microphone" };
export const capitalized = (text: string) => text.charAt(0).toUpperCase() + text.slice(1);

export function failureOf(error: unknown): InputFailure {
  const detail = error as { name?: string; message?: string } | null;
  switch (detail?.name) {
    case "NotAllowedError":
    case "SecurityError": return "denied";
    case "NotFoundError":
    case "OverconstrainedError": return "unavailable";
    case "NotReadableError": return "busy";
    default: return detail?.message === "capture_busy" ? "capture_busy" :
      detail?.message === "capture_unavailable" ? "capture_unavailable" : "other";
  }
}

/** What the failed control's popover says. The Mac app points to macOS settings. */
export function failureCopy(kind: InputKind, failure: InputFailure, native: boolean) {
  const name = inputNames[kind];
  const title = capitalized(name);
  switch (failure) {
    case "denied": return { title: `${title} blocked`, text: native
      ? `Allow Camstream to use the ${name} in System Settings › Privacy & Security, then try again.`
      : `This browser blocked the ${name}. Allow it in the site settings, then try again.` };
    case "unavailable": return { title: `${title} unavailable`, text: `The selected ${name} isn’t available. Choose another ${name} or try again.` };
    case "busy": return { title: `${title} is busy`, text: `Another app is using it. Close that app or choose another ${name}.` };
    case "capture_busy": return { title: `${title} unavailable`, text: "Stop the Camstream camera stream to use call devices." };
    case "capture_unavailable": return { title: `${title} unavailable`, text: "Camstream couldn’t hand over this input. Check the Camera tab, then try again." };
    case "lost": return { title: `${title} stopped`, text: `The ${name} disconnected or another app took it. Try again or choose another ${name}.` };
    default: return { title: `${title} didn’t start`, text: `Could not open the ${name}. Try again or choose another ${name}.` };
  }
}

export type DeviceKind = MediaDeviceKind;
const kindNames: Record<DeviceKind, string> = { videoinput: "Camera", audioinput: "Microphone", audiooutput: "Speaker" };

/** Devices of a kind that can be chosen by ID; browser aliases stay behind System default. */
function selectable(devices: MediaDeviceInfo[], kind: DeviceKind) {
  return devices.filter((device) => device.kind === kind && device.deviceId &&
    device.deviceId !== "default" && device.deviceId !== "communications");
}

export function deviceName(devices: MediaDeviceInfo[], kind: DeviceKind, chosen: string) {
  if (chosen === "default" || chosen === "") return "System default";
  const list = selectable(devices, kind);
  const index = list.findIndex((device) => device.deviceId === chosen);
  if (index < 0) return "Selected device unavailable";
  return list[index]!.label || `${kindNames[kind]} ${index + 1}`;
}

export function deviceEntries(devices: MediaDeviceInfo[], kind: DeviceKind, chosen: string, choose: (id: string) => void): MenuEntry[] {
  const list = selectable(devices, kind);
  const entries: MenuEntry[] = [{ kind: "heading", text: kindNames[kind] },
    { kind: "radio", label: "System default", checked: chosen === "default" || chosen === "", select: () => choose("default") }];
  if (chosen && chosen !== "default" && !list.some((device) => device.deviceId === chosen)) {
    entries.push({ kind: "radio", label: "Selected device unavailable", checked: true, select: () => {} });
  }
  list.forEach((device, index) => entries.push({ kind: "radio", label: device.label || `${kindNames[kind]} ${index + 1}`,
    checked: device.deviceId === chosen, select: () => choose(device.deviceId) }));
  return entries;
}

type Routable = HTMLMediaElement & { setSinkId?(id: string): Promise<void> };

/** Browsers that can route playback to a chosen speaker. The Mac app follows the system output. */
export function speakerRouting(native: boolean) {
  return !native && typeof HTMLMediaElement !== "undefined" && "setSinkId" in HTMLMediaElement.prototype;
}

export async function routeSpeaker(element: HTMLMediaElement | null, id: string) {
  const media = element as Routable | null;
  if (!media?.setSinkId) return true;
  try { await media.setSinkId(id === "default" ? "" : id); return true; }
  catch { return false; }
}
