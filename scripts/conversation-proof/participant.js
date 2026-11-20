const ticket = location.hash.slice(1);
history.replaceState(null, "", location.pathname);
const join = document.querySelector("#join");
const leave = document.querySelector("#leave");
const status = document.querySelector("#status");
const remote = document.querySelector("#remote");
let running;

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function signal(method, value, abort) {
  const response = await fetch("/signal", {
    method, signal: abort, cache: "no-store",
    headers: { Authorization: `Bearer ${ticket}`, "Content-Type": "application/json" },
    body: value === undefined ? undefined : JSON.stringify(value),
  });
  if (response.status === 204) return null;
  if (!response.ok) throw new Error("Signaling rejected");
  return response.json();
}

function syntheticMedia() {
  const canvas = document.createElement("canvas");
  canvas.width = 320;
  canvas.height = 180;
  const drawing = canvas.getContext("2d");
  const draw = () => {
    drawing.fillStyle = "navy";
    drawing.fillRect(0, 0, 320, 180);
    drawing.fillStyle = "white";
    drawing.fillRect((performance.now() / 8) % 300, 60, 20, 60);
  };
  draw();
  const frames = setInterval(draw, 80);
  const video = canvas.captureStream(12).getVideoTracks()[0];
  const audio = new AudioContext();
  const destination = audio.createMediaStreamDestination();
  const oscillator = audio.createOscillator();
  const gain = audio.createGain();
  gain.gain.value = 0.03;
  oscillator.connect(gain).connect(destination);
  oscillator.start();
  const ready = audio.resume();
  const stream = new MediaStream([video, ...destination.stream.getAudioTracks()]);
  return { ready, stream, stop: async () => {
    clearInterval(frames);
    stream.getTracks().forEach((track) => track.stop());
    oscillator.stop();
    await audio.close();
  } };
}

async function leaveCall() {
  const call = running;
  if (!call) return;
  running = undefined;
  call.abort.abort();
  call.peer.close();
  remote.srcObject = null;
  await call.media.stop();
  leave.disabled = true;
  status.textContent = "Left";
  window.proofClosed = call.peer.connectionState === "closed" &&
    call.media.stream.getTracks().every((track) => track.readyState === "ended");
}

async function waitForDescription(side, abort) {
  while (!abort.aborted) {
    const description = await signal("GET", undefined, abort);
    if (description?.type === side) return description;
    await delay(100);
  }
}

async function joinCall() {
  if (running) return;
  const abort = new AbortController();
  const peer = new RTCPeerConnection();
  const media = syntheticMedia();
  running = { abort, peer, media };
  join.disabled = true;
  leave.disabled = false;
  status.textContent = "Negotiating";
  window.proofProgress = () => ({
    audio: media.stream.getAudioTracks()[0]?.readyState,
    gathering: peer.iceGatheringState,
    ice: peer.iceConnectionState,
    connection: peer.connectionState,
    description: peer.remoteDescription?.type,
    size: remote.videoWidth,
    status: status.textContent,
  });
  const incoming = new MediaStream();
  peer.ontrack = ({track}) => {
    incoming.addTrack(track);
    remote.srcObject = incoming;
    void remote.play().catch(() => {});
  };
  try {
    await media.ready;
    for (const track of media.stream.getTracks()) peer.addTrack(track, media.stream);
    if (location.pathname === "/answer") {
      await peer.setRemoteDescription(await waitForDescription("offer", abort.signal));
    }
    const local = location.pathname === "/offer" ? await peer.createOffer() : await peer.createAnswer();
    await peer.setLocalDescription(local);
    while (peer.iceGatheringState !== "complete" && !abort.signal.aborted) await delay(100);
    await signal("PUT", peer.localDescription, abort.signal);
    if (location.pathname === "/offer") {
      await peer.setRemoteDescription(await waitForDescription("answer", abort.signal));
    }
    while (!abort.signal.aborted) {
      const report = await peer.getStats();
      const received = {};
      for (const item of report.values()) {
        if (item.type === "inbound-rtp") received[item.kind ?? item.mediaType] = item.packetsReceived;
      }
      if (received.audio > 10 && received.video > 10 && remote.videoWidth > 0) {
        window.proofReceived = received;
        status.textContent = "Receiving audio and video";
        return;
      }
      await delay(100);
    }
  } catch (error) {
    if (!abort.signal.aborted) {
      await leaveCall();
      status.textContent = error.name;
    }
  }
}

join.addEventListener("click", () => void joinCall());
leave.addEventListener("click", () => void leaveCall());
window.addEventListener("pagehide", () => void leaveCall());
