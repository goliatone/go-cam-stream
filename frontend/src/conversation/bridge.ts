import type { RoomAdmission, RoomRole } from "../services/contracts";

export type CallEnd = "closed" | "ended" | "expired" | "failed";
/** Why the Mac app refused call devices: a live camera stream, or ownership it could not confirm. */
export type CaptureRefusal = "busy" | "unavailable";
export type CallPhase = "prejoin" | "waiting" | "connecting" | "connected" | "reconnecting";
/**
 * Local call activity for the native status display. It names the bound room
 * and role but never carries the admission token or invitation.
 */
export type CallState = {
  version: 1; room: string; role: RoomRole; phase: CallPhase;
  camera: boolean; microphone: boolean; camera_label?: string; microphone_label?: string;
};
type NativeMessage = "ready" | "capture" | "copy_invitation" | { closed: CallEnd } | { state: CallState };
type NativePort = { postMessage(message: NativeMessage): Promise<unknown> };
export type NativeCall = {
  admission(): Promise<RoomAdmission & { start_muted?: boolean }>;
  /** Resolves true when granted, otherwise the refusal reason. */
  capture(): Promise<true | CaptureRefusal>;
  closed(reason: CallEnd): void;
  state(value: CallState): void;
  /**
   * Asks the Mac app to copy the host's invitation. The link stays in the app;
   * the page learns only whether it was copied.
   */
  copyInvitation?(): Promise<boolean>;
};

export function nativeCall(): NativeCall | undefined {
  const native = (window as Window & { webkit?: { messageHandlers?: { conversation?: NativePort } } })
    .webkit?.messageHandlers?.conversation;
  if (!native) return undefined;
  return {
    admission: async () => await native.postMessage("ready") as Awaited<ReturnType<NativeCall["admission"]>>,
    // Older Mac builds answer false for any refusal, which meant a live stream.
    capture: async () => {
      const value = await native.postMessage("capture");
      return value === true ? true : value === "unavailable" ? "unavailable" : "busy";
    },
    closed: (reason) => { void native.postMessage({ closed: reason }).catch(() => {}); },
    // Older Mac builds reject state reports; activity then stays unknown there.
    state: (value) => { void native.postMessage({ state: value }).catch(() => {}); },
    copyInvitation: async () => await native.postMessage("copy_invitation").then((value) => value === true, () => false),
  };
}

const labelBytes = 128;

/** A device name the Mac app accepts, or nothing when it cannot be shown safely. */
export function deviceLabel(track: MediaStreamTrack | undefined): string | undefined {
  const label = track?.label.trim();
  if (!label || /[\u0000-\u001f\u007f-\u009f]/.test(label) || new TextEncoder().encode(label).length > labelBytes) return undefined;
  return label;
}
