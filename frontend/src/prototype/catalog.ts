// Synthetic catalog for the prototype: the contract examples plus the same
// extra cameras the presentation preview uses.
import samples from "../../../data/api/examples.json";
import type { CameraList, ErrorBody } from "../services/contracts";
import type { SimConfig } from "./sim";

export const catalog: CameraList = {
  cameras: [
    ...(samples.catalog.cameras as CameraList["cameras"]),
    {
      id: "livingroom",
      name: "Living room",
      availability: "idle",
      streams: [
        { id: "main", name: "Main", capabilities: { video: true, audio: true } },
        {
          id: "compact",
          name: "Compact",
          capabilities: { video: true, audio: false },
        },
      ],
    },
    {
      id: "hall",
      name: "Hall",
      availability: "offline",
      streams: [
        {
          id: "default",
          name: "Default",
          capabilities: { video: true, audio: false },
        },
      ],
    },
    {
      id: "front-porch",
      name: "Front porch",
      availability: "unknown",
      streams: [
        {
          id: "default",
          name: "Default",
          capabilities: { video: true, audio: true },
        },
      ],
    },
  ],
};

// The latched fault comes from the contract example. Its message is never shown.
const hallFault = samples.offline_status.streams[0]!.error as ErrorBody;

export const simConfig: SimConfig = {
  list: catalog,
  viewers: { room1: 0, room2: 2, livingroom: 1, hall: 0, "front-porch": 0 },
  faults: { hall: hallFault },
  unobserved: ["front-porch"],
};

export interface Look {
  hue: number;
  scene: "room" | "outdoor";
}
const looks: Record<string, Look> = {
  room1: { hue: 28, scene: "room" },
  room2: { hue: 212, scene: "room" },
  livingroom: { hue: 150, scene: "room" },
  hall: { hue: 265, scene: "room" },
  "front-porch": { hue: 200, scene: "outdoor" },
};
export function lookOf(cameraId: string): Look {
  return looks[cameraId] ?? { hue: 200, scene: "room" };
}
export function sizeOf(streamId: string) {
  return streamId === "compact"
    ? { width: 640, height: 360 }
    : { width: 1280, height: 720 };
}
