import { expect, test } from "vitest";
import examples from "../../../data/api/conversation_examples.json";
import type { RoomFault, RoomView } from "./contracts";

const view = (room: typeof examples.waiting): RoomView => ({
  ...room,
  state: room.state as RoomView["state"],
  participants: room.participants.map((participant) => ({
    ...participant,
    role: participant.role as RoomView["participants"][number]["role"],
  })),
});

test("conversation fixtures have typed room and failure projections", () => {
  const waiting = view(examples.waiting);
  const connected = view(examples.connected);
  const reconnecting = view(examples.reconnecting);
  const ended = view(examples.ended);
  const errors: RoomFault[] = [examples.full.error, examples.denied.error, examples.incompatible.error];
  expect([waiting.state, connected.state, reconnecting.state, ended.state]).toEqual([
    "waiting", "connected", "reconnecting", "ended",
  ]);
  expect(connected.participants.map(({ role }) => role)).toEqual(["host", "guest"]);
  expect(errors.map(({ code }) => code)).toEqual(["room_full", "permission_denied", "incompatible_client"]);
});
