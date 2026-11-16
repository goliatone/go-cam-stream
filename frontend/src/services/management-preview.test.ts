import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { BrowserPreview } from "./management-preview";
const stats=vi.fn();
class Peer {
 addTransceiver=vi.fn();getStats=stats;close=vi.fn();ontrack=null;
}
beforeEach(()=>{vi.stubGlobal("RTCPeerConnection",Peer);stats.mockResolvedValue(new Map());vi.spyOn(HTMLMediaElement.prototype,"pause").mockImplementation(()=>{});});
afterEach(()=>vi.unstubAllGlobals());
function video(){const v=document.createElement("video");Object.defineProperties(v,{paused:{value:false,configurable:true},videoWidth:{value:640},videoHeight:{value:360}});return v;}
it("requires advancing presented frames with dimensions and non-paused playback",async()=>{
 const v=video();let frame!:VideoFrameRequestCallback;v.requestVideoFrameCallback=vi.fn(callback=>{frame=callback;return 1});v.cancelVideoFrameCallback=vi.fn();
 const p=new BrowserPreview(v);expect(await p.evidence()).toBeUndefined();frame(0,{} as VideoFrameCallbackMetadata);expect(await p.evidence()).toBeUndefined();frame(1,{} as VideoFrameCallbackMetadata);
 expect(await p.evidence()).toMatchObject({method:"video_frame_callback",frames:2,paused:false});Object.defineProperty(v,"paused",{value:true});expect(await p.evidence()).toBeUndefined();p.close();expect(v.cancelVideoFrameCallback).toHaveBeenCalled();
});
it("uses this peer's inbound framesDecoded when frame callbacks are unavailable",async()=>{
 const v=video(),p=new BrowserPreview(v);stats.mockResolvedValue(new Map([["video",{type:"inbound-rtp",kind:"video",framesDecoded:3}]]));
 expect(await p.evidence()).toMatchObject({method:"frames_decoded",frames:3});p.close();
});
it("requires advancement of playback-quality counters, not old frames or timeupdate",async()=>{
 const v=video();let frames=100;v.getVideoPlaybackQuality=()=>({totalVideoFrames:frames} as VideoPlaybackQuality);
 const p=new BrowserPreview(v);expect(await p.evidence()).toBeUndefined();v.dispatchEvent(new Event("timeupdate"));expect(await p.evidence()).toBeUndefined();frames+=2;
 expect(await p.evidence()).toMatchObject({method:"playback_quality",frames:2});p.close();
});
it("fails honestly when no usable evidence API exists",async()=>{
 const v=video();Object.defineProperty(v,"getVideoPlaybackQuality",{value:undefined});const p=new BrowserPreview(v);
 await expect(p.evidence()).rejects.toMatchObject({code:"playback_evidence_unavailable"});p.close();
});
