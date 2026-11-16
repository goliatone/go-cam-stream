import type { FrameEvidence, SDPAnswer, SDPOffer } from "./contracts";
import { ManagementFailure } from "./management-api";

export interface Preview {
  offer(): Promise<SDPOffer>;
  answer(answer: SDPAnswer): Promise<void>;
  evidence(): Promise<FrameEvidence | undefined>;
  close(): void;
}
export class BrowserPreview implements Preview {
  #peer: RTCPeerConnection;
  #frames = 0;
  #callback: number | undefined;
  #closed = false;
  constructor(readonly video: HTMLVideoElement) {
    if(typeof RTCPeerConnection!=="function")throw new ManagementFailure("unsupported_browser");
    this.#peer=new RTCPeerConnection({iceServers:[]});
    this.#peer.addTransceiver("video",{direction:"recvonly"});
    this.#peer.ontrack=event=>{
      if(this.#closed)return;
      video.srcObject=event.streams[0]??new MediaStream([event.track]);video.muted=true;
      void video.play().catch(()=>{});
    };
    if(typeof video.requestVideoFrameCallback==="function") {
      const frame=()=>{if(this.#closed)return;this.#frames++;this.#callback=video.requestVideoFrameCallback(frame)};
      this.#callback=video.requestVideoFrameCallback(frame);
    }
  }
  async offer():Promise<SDPOffer> {
    const offer=await this.#peer.createOffer();await this.#peer.setLocalDescription(offer);
    if(this.#peer.iceGatheringState!=="complete")await new Promise<void>((resolve,reject)=>{
      const finish=(error?:ManagementFailure)=>{clearTimeout(timer);this.#peer.removeEventListener("icegatheringstatechange",change);error?reject(error):resolve()};
      const change=()=>{if(this.#peer.iceGatheringState==="complete")finish();else if(this.#closed)finish(new ManagementFailure("canceled"))};
      const timer=setTimeout(()=>finish(new ManagementFailure("deadline_exceeded")),10000);
      this.#peer.addEventListener("icegatheringstatechange",change);change();
    });
    if(this.#closed)throw new ManagementFailure("canceled");
    const sdp=this.#peer.localDescription?.sdp;if(!sdp)throw new ManagementFailure("signaling_failed");
    return {type:"offer",sdp};
  }
  async answer(answer:SDPAnswer) {if(this.#closed)throw new ManagementFailure("canceled");await this.#peer.setRemoteDescription(answer);}
  async evidence():Promise<FrameEvidence|undefined> {
    if(this.#closed)return;
    let frames=this.#frames,method="video_frame_callback",available=typeof this.video.requestVideoFrameCallback==="function";
    if(!available) {
      const stats=await this.#peer.getStats();
      stats.forEach(report=>{if(report.type==="inbound-rtp"&&(report.kind==="video"||report.mediaType==="video")&&typeof report.framesDecoded==="number"){frames=report.framesDecoded;method="frames_decoded";available=true}});
      if(!available&&typeof this.video.getVideoPlaybackQuality==="function"){frames=this.video.getVideoPlaybackQuality().totalVideoFrames;method="playback_quality";available=Number.isFinite(frames)}
    }
    if(!available)throw new ManagementFailure("playback_evidence_unavailable");
    if(frames<2||this.video.paused||this.video.videoWidth<1||this.video.videoHeight<1)return;
    return {method,frames,width:this.video.videoWidth,height:this.video.videoHeight,paused:false};
  }
  close() {
    this.#closed=true;
    if(this.#callback!==undefined)this.video.cancelVideoFrameCallback(this.#callback);
    this.#peer.ontrack=null;this.#peer.close();
    this.video.pause();this.video.srcObject=null;
  }
}
