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
 await api.operation("key/", "epoch", "candidate", true);expect(fetcher.mock.calls[2]![0]).toBe("/api/camera-operations/key%2F?runtime_epoch=epoch&candidate_id=candidate&kind=recovery");
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
