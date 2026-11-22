import { expect, it, vi } from "vitest";
import { ManagementClient } from "./management-api";
import { CSRF_HEADER } from "./contracts";
const mutation={operation_key:"original",runtime_epoch:"epoch"};
const draft={id:"draft",candidate_id:"candidate",revision:1,runtime_epoch:"epoch",expires_at:"2026-11-16T10:00:00Z",name:"Camera",endpoint:"rtsp://localhost/private",authentication:"anonymous",credentials_present:false,warnings:[],state:"ready"};
const json=(value:unknown,status=200)=>new Response(JSON.stringify(value),{status});
it("keeps credentials in the POST body and attaches validated same-origin CSRF",async()=>{
 const fetcher=vi.fn().mockResolvedValueOnce(json({token:"csrf",header_name:CSRF_HEADER})).mockResolvedValueOnce(json(draft));
 const api=new ManagementClient(fetcher);await api.create({...mutation,name:"Camera",endpoint:draft.endpoint,authentication:"credentials",credentials:{username:"private-user",password:"private-password"}});
 expect(fetcher).toHaveBeenCalledTimes(2);const [url,init]=fetcher.mock.calls[1]!;
 expect(url).toBe("/api/camera-drafts");expect(init).toMatchObject({method:"POST",credentials:"same-origin",redirect:"error",cache:"no-store",headers:{[CSRF_HEADER]:"csrf"}});expect(init.body).toContain("private-password");
});
it("does not blindly retry a failed mutation and clears a rejected CSRF token",async()=>{
 const fetcher=vi.fn().mockResolvedValueOnce(json({token:"first",header_name:CSRF_HEADER})).mockResolvedValueOnce(json({error:{code:"csrf_invalid",message:"PRIVATE"}},403)).mockResolvedValueOnce(json({token:"second",header_name:CSRF_HEADER})).mockResolvedValueOnce(json(draft));
 const api=new ManagementClient(fetcher);await expect(api.cancel("draft",mutation)).rejects.toMatchObject({code:"csrf_invalid"});expect(fetcher).toHaveBeenCalledTimes(2);
 await api.cancel("draft",mutation);expect(fetcher.mock.calls[3]![1].headers[CSRF_HEADER]).toBe("second");
});
it("sends bounded import files as multipart and safely encodes owner recovery identifiers",async()=>{
 const fetcher=vi.fn().mockResolvedValueOnce(json({token:"csrf",header_name:CSRF_HEADER})).mockResolvedValueOnce(json(draft)).mockResolvedValueOnce(json({operation_key:"key",kind:"save",state:"not_found",runtime_epoch:"epoch"}));
 const api=new ManagementClient(fetcher);await api.import(mutation,new File(["yaml"],"camera.yaml"),new File(["password"],"camera.env"));
 expect(fetcher.mock.calls[1]![1].body).toBeInstanceOf(FormData);expect(fetcher.mock.calls[1]![1].headers["Content-Type"]).toBeUndefined();
 await api.operation("key/", "epoch", "candidate", "recovery");expect(fetcher.mock.calls[2]![0]).toBe("/api/camera-operations/key%2F?runtime_epoch=epoch&candidate_id=candidate&kind=recovery");
 await expect(api.import(mutation,new File(["x".repeat(32769)],"large.yaml"))).rejects.toMatchObject({code:"import_invalid"});expect(fetcher).toHaveBeenCalledTimes(3);
});
it("rejects malformed CSRF configuration and never exposes server text",async()=>{
 const fetcher=vi.fn().mockResolvedValueOnce(json({token:"csrf",header_name:"Wrong"})).mockResolvedValueOnce(json({error:{code:"__proto__",message:"PRIVATE"}},500));const api=new ManagementClient(fetcher);
 await expect(api.cancel("draft",mutation)).rejects.toMatchObject({code:"server_unavailable"});expect(fetcher).toHaveBeenCalledOnce();await expect(api.capabilities()).rejects.toMatchObject({message:"server_unavailable"});
});

it("calls the native fetch entry point without binding it to the client",async()=>{
 const fetcher=vi.fn(function(this:unknown){expect(this).not.toBeInstanceOf(ManagementClient);return Promise.resolve(json({can_manage_cameras:true,recovery_scope:"scope",runtime_epoch:"epoch"}))});
 vi.stubGlobal("fetch",fetcher);try{expect((await new ManagementClient().capabilities()).can_manage_cameras).toBe(true)}finally{vi.unstubAllGlobals()}
});
it("adds up a camera's viewers, and has no count when a stream's is unknown",async()=>{
 const fetcher=vi.fn().mockResolvedValueOnce(json({camera_id:"a/b",streams:[{stream_id:"default",state:"playing",viewers:2},{stream_id:"compact",state:"idle",viewers:1}]}))
  .mockResolvedValueOnce(json({camera_id:"hall",streams:[{stream_id:"default",state:"unknown",viewers:null}]})).mockResolvedValueOnce(json({camera_id:"empty",streams:[]}));
 const api=new ManagementClient(fetcher);expect(await api.viewers("a/b")).toBe(3);
 expect(fetcher.mock.calls[0]![0]).toBe("/api/cameras/a%2Fb/status");expect(fetcher.mock.calls[0]![1].method).toBe("GET");
 expect(await api.viewers("hall")).toBeUndefined();expect(await api.viewers("empty")).toBeUndefined();
});
it("reads settings and sends renames, removals and stop-viewers to their camera and draft routes",async()=>{
 const revision={runtime_epoch:"epoch",generation:3};
 const settings={camera_id:"a/b",name:"Porch",endpoint:"rtsp://cam.local/live",dial_address:"192.0.2.10:554",authentication:"credentials",catalog_revision:revision};
 const change={camera_id:"a/b",catalog_revision:{...revision,generation:4}};
 const fetcher=vi.fn().mockResolvedValueOnce(json(settings)).mockResolvedValueOnce(json({token:"csrf",header_name:CSRF_HEADER}))
  .mockResolvedValueOnce(json(change)).mockResolvedValueOnce(json({...change,removed:true})).mockResolvedValueOnce(json({...draft,fenced:true}))
  .mockResolvedValueOnce(json({camera_id:"a/b",name:"Porch"})).mockResolvedValueOnce(json({camera_id:"a/b"}));
 const api=new ManagementClient(fetcher);
 expect(await api.settings("a/b")).toEqual(settings);
 expect(fetcher.mock.calls[0]![0]).toBe("/api/cameras/a%2Fb/settings");expect(fetcher.mock.calls[0]![1].method).toBe("GET");
 expect(await api.rename("a/b",{...mutation,name:"Garage",catalog_revision:revision})).toEqual(change);
 expect(fetcher.mock.calls[2]![0]).toBe("/api/cameras/a%2Fb");expect(fetcher.mock.calls[2]![1]).toMatchObject({method:"PATCH",headers:{[CSRF_HEADER]:"csrf"}});
 expect(JSON.parse(fetcher.mock.calls[2]![1].body)).toEqual({...mutation,name:"Garage",catalog_revision:revision});
 expect((await api.remove("a/b",{...mutation,catalog_revision:revision,stop_viewers:true})).removed).toBe(true);
 expect(fetcher.mock.calls[3]![0]).toBe("/api/cameras/a%2Fb");expect(fetcher.mock.calls[3]![1].method).toBe("DELETE");
 expect(JSON.parse(fetcher.mock.calls[3]![1].body).stop_viewers).toBe(true);
 expect((await api.stopViewers("draft",{...mutation,expected_revision:1})).fenced).toBe(true);
 expect(fetcher.mock.calls[4]![0]).toBe("/api/camera-drafts/draft/stop-viewers");expect(fetcher.mock.calls[4]![1].method).toBe("POST");
 // Replies without the address, revision or camera are refused.
 await expect(api.settings("a/b")).rejects.toMatchObject({code:"server_unavailable"});
 await expect(api.rename("a/b",{...mutation,name:"Garage",catalog_revision:revision})).rejects.toMatchObject({code:"server_unavailable"});
});
