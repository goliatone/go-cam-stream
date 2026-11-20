// Loaded only by the loopback T11 fixture.
globalThis.viewers = new Map();

globalThis.join = async (session, { audio = false } = {}) => {
  const peer = new RTCPeerConnection({ iceServers: [], bundlePolicy: "max-bundle" });
  const video = document.createElement("video");
  video.autoplay = true;
  video.muted = true;
  video.playsInline = true;
  document.body.append(video);
  const state = { peer, video, frames: 0, tracks: [] };
  peer.ontrack = ({ track, streams }) => {
    state.tracks.push(track.kind);
    if (track.kind === "video") video.srcObject = streams[0] ?? new MediaStream([track]);
  };
  const countFrame = () => {
    state.frames++;
    video.requestVideoFrameCallback(countFrame);
  };
  video.requestVideoFrameCallback(countFrame);
  peer.addTransceiver("video", { direction: "recvonly" });
  if (audio) peer.addTransceiver("audio", { direction: "recvonly" });
  try {
    await peer.setLocalDescription(await peer.createOffer());
    // Initiate ICE from the browser over the instrumented loopback path.
    const offer = peer.localDescription.sdp.replace(/^a=candidate:.*\r\n/gm, "");
    const response = await fetch("/offer", {
      method: "POST",
      headers: { "Content-Type": "application/sdp", "X-Proof-Session": session },
      body: offer,
    });
    if (!response.ok) {
      peer.close();
      video.remove();
      return response.status;
    }
    const sdp = await response.text();
    state.audioAccepted = sdp.split(/\r?\n/).some((line) => /^m=audio [1-9]/.test(line));
    await peer.setRemoteDescription({ type: "answer", sdp });
    viewers.set(session, state);
    return response.status;
  } catch (error) {
    peer.close();
    video.remove();
    throw error;
  }
};

globalThis.disconnect = (session) => {
  const state = viewers.get(session);
  state.peer.close();
  state.video.remove();
  viewers.delete(session);
};

globalThis.sample = async (session) => {
  const state = viewers.get(session);
  const stats = await state.peer.getStats();
  const transport = [...stats.values()].find((stat) => stat.type === "transport");
  const pair = stats.get(transport?.selectedCandidatePairId);
  const remote = stats.get(pair?.remoteCandidateId);
  const audio = [...stats.values()].filter((stat) => stat.type === "inbound-rtp" && stat.kind === "audio");
  return {
    frames: state.frames,
    width: state.video.videoWidth,
    height: state.video.videoHeight,
    tracks: state.tracks,
    audioAccepted: state.audioAccepted,
    audioPackets: audio.reduce((sum, stat) => sum + stat.packetsReceived, 0),
    remotePort: remote?.port,
    peerOpen: state.peer.signalingState !== "closed",
  };
};
