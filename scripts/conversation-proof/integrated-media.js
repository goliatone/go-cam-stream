// This script runs only in the loopback proof.
(() => {
  const peers = [];
  const tracks = [];
  let denyMicrophone = false;
  let lastSample;
  const RealPeer = window.RTCPeerConnection;
  window.RTCPeerConnection = class extends RealPeer {
    constructor(...args) { super(...args); peers.push(this); }
  };
  navigator.mediaDevices.getUserMedia = async ({ audio, video }) => {
    if (audio && denyMicrophone) {
      denyMicrophone = false;
      throw new DOMException("Proof microphone denial", "NotAllowedError");
    }
    const result = new MediaStream();
    if (video) {
      const canvas = document.createElement("canvas");
      canvas.width = 320;
      canvas.height = 180;
      const context = canvas.getContext("2d");
      const paint = () => {
        context.fillStyle = "#143552";
        context.fillRect(0, 0, 320, 180);
        context.fillStyle = "white";
        context.fillRect(performance.now() % 250, 60, 28, 60);
      };
      paint();
      const track = canvas.captureStream(12).getVideoTracks()[0];
      const timer = setInterval(() => {
        if (track.readyState === "ended") clearInterval(timer);
        else paint();
      }, 80);
      result.addTrack(track);
    }
    if (audio) {
      const context = new AudioContext();
      const destination = context.createMediaStreamDestination();
      const oscillator = context.createOscillator();
      const gain = context.createGain();
      gain.gain.value = 0.015;
      oscillator.connect(gain).connect(destination);
      oscillator.start();
      void context.resume();
      const track = destination.stream.getAudioTracks()[0];
      const timer = setInterval(() => {
        if (track.readyState === "ended") {
          clearInterval(timer);
          oscillator.stop();
          void context.close();
        }
      }, 100);
      result.addTrack(track);
    }
    tracks.push(...result.getTracks());
    return result;
  };
  // Busy controls stay focusable and report it through aria-disabled.
  const ready = (control) => control && !control.disabled && control.getAttribute("aria-disabled") !== "true";
  window.integratedProof = {
    async start(invitedInBrowser = false, denyMic = false) {
      if (tracks.length || peers.length || location.hash) throw new Error("prejoin was not idle");
      denyMicrophone = denyMic;
      // Join with both inputs. A browser invitation turns both on already;
      // a Mac or host page starts with them off. While the page is still
      // opening, the caller asks again.
      const toggles = () => [...document.querySelectorAll("#prejoin-camera, #prejoin-mic")];
      if (toggles().length !== 2) throw new Error("prejoin input toggles are missing");
      if (![...toggles(), document.querySelector("#join-call")].every(ready)) return false;
      for (const toggle of toggles()) {
        if (toggle.getAttribute("aria-pressed") === "true") continue;
        if (invitedInBrowser) throw new Error("browser invitation did not turn on both inputs");
        toggle.click();
      }
      await new Promise((resolve) => setTimeout(resolve, 0));
      if (tracks.length) throw new Error("choosing inputs captured before Join");
      if (toggles().some((toggle) => toggle.getAttribute("aria-pressed") !== "true")) {
        throw new Error("prejoin did not keep both inputs on");
      }
      document.querySelector("#join-call").click();
      return true;
    },
    // Leave or, for a host, confirm End the way a person would.
    async leave() {
      document.querySelector("#leave-call")?.click();
      await new Promise((resolve) => setTimeout(resolve, 0));
      document.querySelector("#end-conversation")?.click();
    },
    async step(action) {
      const [input, target] = action.split(":");
      const deadline = performance.now() + 7500;
      const waitFor = async (condition) => {
        while (!condition()) {
          if (performance.now() > deadline) throw new Error("Timed out: " + action);
          await new Promise((resolve) => setTimeout(resolve, 50));
        }
      };
      // Camera and Mic controls report on or off through aria-pressed.
      const pressed = (button) => button?.getAttribute("aria-pressed");
      if (input === "audio") {
        await waitFor(() => target === "active" ? document.querySelector('.audio-activity[data-active="true"]') :
          !document.querySelector(".audio-activity"));
        return;
      }
      // Controls fade while the call is idle. Focus must bring them back.
      const reach = async (control, problem) => {
        control.focus();
        await waitFor(() => !control.closest("[data-idle]"));
        if (document.activeElement !== control || control.getBoundingClientRect().bottom > innerHeight) {
          throw new Error(problem);
        }
      };
      if (input === "devices") {
        const enable = document.querySelector("#call-enable-inputs");
        await waitFor(() => ready(enable));
        await reach(enable, "Combined call action is not visible and focusable");
        enable.click();
        await waitFor(() => pressed(document.querySelector("#call-camera")) === "true" &&
          pressed(document.querySelector("#call-mic")) === "true");
        return;
      }
      const camera = input === "camera";
      const button = document.querySelector(camera ? "#call-camera" : "#call-mic");
      const enabling = target === "on";
      await waitFor(() => ready(button) && pressed(button) === String(!enabling));
      await reach(button, "Call action is not visible and focusable: " + action);
      button.click();
      const kind = camera ? "video" : "audio";
      await waitFor(() => ready(button) && pressed(button) === String(enabling) &&
        tracks.some((track) => track.kind === kind && track.readyState === "live") === enabling);
    },
    async sample() {
      const last = peers.at(-1);
      const video = document.querySelector("#remote-video");
      if (video) video.muted = true;
      const stats = { audio: 0, video: 0, peers: peers.length };
      const raw = [];
      if (last && last.connectionState !== "closed") {
        for (const value of (await last.getStats()).values()) {
          if (value.type === "inbound-rtp") raw.push([value.kind, value.packetsReceived, value.framesDecoded]);
          if (value.type === "inbound-rtp" && value.kind === "audio") stats.audio += value.packetsReceived || 0;
          if (value.type === "inbound-rtp" && value.kind === "video" && video?.videoWidth > 0) {
            stats.video += value.packetsReceived || 0;
          }
        }
      }
      lastSample = { ...stats, raw, width: video?.videoWidth, paused: video?.paused,
        audioActivity: document.querySelector(".audio-activity")?.dataset.active ?? "none",
        tracks: video?.srcObject?.getTracks().map(({kind, muted}) => [kind, muted]) };
      return stats;
    },
    status() {
      const title = document.querySelector("h1")?.textContent;
      const problem = document.querySelector("[role=alert]")?.textContent;
      return { title, problem, sample: lastSample, local: tracks.map(({kind, readyState}) => [kind, readyState]),
        transport: peers.map(({connectionState, localDescription, remoteDescription}) =>
          [connectionState, localDescription?.type, remoteDescription?.type]) };
    },
    cameraIdle() { return tracks.length === 0; },
    closed() {
      return tracks.every(track => track.readyState === "ended") &&
        peers.length > 0 && peers.every(peer => peer.connectionState === "closed");
    },
  };
})();
