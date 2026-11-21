import { beforeEach, expect, it, vi } from "vitest";
import type { Capabilities, DraftReview, Mutation, OperationKind, OperationResult, TestResult } from "./contracts";
import { ManagementFailure, type ManagementAPI } from "./management-api";
import { ManagementController } from "./management-controller";
import { RECOVERY_STORAGE_KEY, RecoveryStorage } from "./management-storage";

const caps:Capabilities={can_manage_cameras:true,recovery_scope:"scope",runtime_epoch:"epoch"};
const input={name:"PRIVATE-NAME",endpoint:"rtsp://localhost:18556/PRIVATE-PATH",authentication:"credentials" as const,credentials:{username:"PRIVATE-USER",password:"PRIVATE-PASSWORD"}};
const revision={runtime_epoch:"epoch",generation:1};
beforeEach(()=>sessionStorage.clear());
function fixture(){
 let draft:DraftReview={id:"draft",candidate_id:"candidate",revision:1,runtime_epoch:"epoch",expires_at:new Date(Date.now()+900000).toISOString(),name:input.name,endpoint:input.endpoint,authentication:"credentials",credentials_present:true,warnings:[],state:"ready"};
 let currentCaps=caps;
 const operations=new Map<string,OperationResult>(),drop=new Set<OperationKind>();
 const preview={offer:vi.fn(async()=>({type:"offer" as const,sdp:"v=0\r\n"})),answer:vi.fn(async()=>{}),evidence:vi.fn(async()=>({method:"frames_decoded",frames:3,width:640,height:360,paused:false})),close:vi.fn()};
 const test=():TestResult=>({id:"test",attempt_key:"attempt",draft_revision:draft.revision,expires_at:new Date(Date.now()+20000).toISOString(),stage:"browser_playback",answer:{type:"answer",sdp:"v=0\r\n"},source_media_observed:true,browser_frames_reported:false,cleanup:"pending"});
 function done<T>(kind:OperationKind,m:Mutation,result:T){
  operations.set(m.operation_key,{operation_key:m.operation_key,kind,state:"succeeded",runtime_epoch:"epoch",draft_id:draft.id,candidate_id:draft.candidate_id,...(kind==="save"?{saved:{camera_id:"candidate",catalog_revision:{...revision,generation:2}}}:{})});
  if(drop.has(kind)){drop.delete(kind);throw new ManagementFailure()}
  return result;
 }
 const api:ManagementAPI={
  capabilities:vi.fn(async()=>currentCaps),draft:vi.fn(async()=>structuredClone(draft)),
  operation:vi.fn(async (key:string):Promise<OperationResult>=>operations.get(key)??{operation_key:key,kind:"create",state:"not_found",runtime_epoch:"epoch"}),
  create:vi.fn(async request=>done("create",request,structuredClone(draft))),
  credentialDraft:vi.fn(async request=>{
    draft={...draft,candidate_id:request.camera_id,credential_update:true,state:"ready",warnings:[]};
    delete draft.test;
    return done("create",request,structuredClone(draft));
  }),
  import:vi.fn(async request=>done("import",request,structuredClone(draft))),
  patch:vi.fn(async(_id,request)=>{draft={...draft,revision:draft.revision+1,state:"ready"};delete draft.test;return done("patch",request,structuredClone(draft))}),
  cancel:vi.fn(async(_id,request)=>{draft={...draft,state:"canceled"};return done("cancel",request,structuredClone(draft))}),
  test:vi.fn(async(_id,request)=>{draft={...draft,state:"testing",test:test()};return done("test",request,draft.test!)}),
  testStatus:vi.fn(async()=>draft.test!),
  complete:vi.fn(async(_id,_test,request)=>{draft={...draft,state:"tested",test:{...draft.test!,stage:"passed",cleanup:"confirmed",browser_frames_reported:true}};return done("complete",request,draft.test!)}),
  cancelTest:vi.fn(async(_id,_test,request)=>{draft={...draft,state:"ready",test:{...draft.test!,stage:"canceled",cleanup:"confirmed"}};return done("cancel_test",request,draft.test!)}),
  save:vi.fn(async request=>done("save",request,{camera_id:"candidate",catalog_revision:{...revision,generation:2}})),
  reconnect:vi.fn(async()=>({affected:[],closed_sessions:[]})),
 };
 const refresh=vi.fn(async()=>revision);
 const storage=new RecoveryStorage(sessionStorage);
 const build=()=>new ManagementController(api,storage,vi.fn(),refresh,()=>preview);
 return {api,drop,operations,preview,refresh,storage,build,setCaps:(c:Capabilities)=>{currentCaps=c}};
}
it.each(["create","import","patch","complete","cancel","save"] as const)("resolves a lost %s response through its original operation without replay",async kind=>{
 const f=fixture(),c=f.build();await c.init();
 if(kind!=="create"&&kind!=="import")await c.submit(input);
 if(kind==="complete"||kind==="save"){
  if(kind==="complete")f.drop.add(kind);
  await c.test(document.createElement("video"));
 }
 f.drop.add(kind);
 if(kind==="create"||kind==="patch")await c.submit(input);
 if(kind==="import")await c.importFiles(new File(["yaml"],"camera.yaml"));
 if(kind==="cancel")await c.cancel();
 if(kind==="save")await c.save();
 expect(f.api.operation).toHaveBeenCalled();expect(c.view.unresolved).toBe(false);
 const method=kind==="complete"?f.api.complete:kind==="import"?f.api.import:f.api[kind];expect(method).toHaveBeenCalledTimes(1);
 const persisted=sessionStorage.getItem(RECOVERY_STORAGE_KEY)??"";
 for(const secret of [input.name,input.endpoint,input.credentials.username,input.credentials.password])expect(persisted).not.toContain(secret);
 if(kind==="save")expect(c.view.saved?.camera_id).toBe("candidate");
});
it("recovers a lost preview negotiation and cancels it without opening a second peer",async()=>{
 const f=fixture(),c=f.build();await c.init();await c.submit(input);f.drop.add("test");await c.test(document.createElement("video"));
 expect(f.api.test).toHaveBeenCalledOnce();expect(c.view.phase).toBe("testing");
 f.drop.add("cancel_test");await c.cancelTest();expect(f.api.cancelTest).toHaveBeenCalledOnce();expect(c.view.draft?.state).toBe("ready");expect(c.view.unresolved).toBe(false);
});
it("opens the authorized duplicate camera and recovers a lost credential draft without replaying the password", async () => {
 const f=fixture(),c=f.build();await c.init();await c.submit(input);
 vi.mocked(f.api.test).mockImplementationOnce(async (_id, request) => {
  f.operations.set(request.operation_key,{operation_key:request.operation_key,kind:"test",state:"not_committed",runtime_epoch:"epoch",
    error:{code:"duplicate_camera",message:"Address in use",retryable:false,camera_id:"existing"}});
  throw new ManagementFailure();
 });
 await c.test(document.createElement("video"));
 expect(c.view.duplicateCamera).toBe("existing");
 await c.openCredentials({id:c.view.duplicateCamera!});
 expect(f.api.cancel).toHaveBeenCalledOnce();
 expect(c.view.draft).toBeUndefined();
 f.drop.add("create");
 await c.submitCredentials({username:"",password:"replacement-secret"});
 expect(f.api.credentialDraft).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({
  camera_id:"existing",credentials:{username:"",password:"replacement-secret"},
 }));
 expect(c.view.draft?.credential_update).toBe(true);
 expect(c.view.draft?.candidate_id).toBe("existing");
 expect(c.view.unresolved).toBe(false);
 expect(sessionStorage.getItem(RECOVERY_STORAGE_KEY)).not.toContain("replacement-secret");
});
it("recovers a durable Save after reload even with a different epoch and no management permission",async()=>{
 const f=fixture(),c=f.build();await c.init();await c.submit(input);await c.test(document.createElement("video"));await c.save();
 const record=f.storage.read(caps)!;expect(record.kind).toBe("save");
 f.setCaps({...caps,runtime_epoch:"restarted",can_manage_cameras:false});
 const reloaded=f.build();await reloaded.init();expect(reloaded.view.saved?.camera_id).toBe("candidate");expect(f.api.save).toHaveBeenCalledOnce();expect(reloaded.capabilities?.runtime_epoch).toBe("restarted");
});
it("never mints another Save while the original outcome is unknown",async()=>{
 const f=fixture(),c=f.build();await c.init();await c.submit(input);await c.test(document.createElement("video"));
 vi.mocked(f.api.save).mockRejectedValue(new ManagementFailure("save_outcome_unknown"));
 vi.mocked(f.api.operation).mockRejectedValue(new ManagementFailure("storage_unavailable"));
 await c.save();const record=sessionStorage.getItem(RECOVERY_STORAGE_KEY);expect(c.view.unresolved).toBe(true);
 await c.save();await c.cancel();expect(f.api.save).toHaveBeenCalledOnce();expect(sessionStorage.getItem(RECOVERY_STORAGE_KEY)).toBe(record);expect(c.view.canAbandon).not.toBe(true);
});
it("blocks mutations before sending anything when browser storage is unavailable",async()=>{
 const f=fixture();const storage=new RecoveryStorage({setItem(){throw Error()},getItem(){return null},removeItem(){}} as unknown as Storage);
 const c=new ManagementController(f.api,storage,vi.fn(),f.refresh);await c.init();await c.submit(input);
 expect(c.view.storageReady).toBe(false);expect(c.view.error).toBe("recovery_storage_unavailable");expect(f.api.create).not.toHaveBeenCalled();
});
it("visibility cancellation does not invalidate an already cleaned successful test",async()=>{
 const f=fixture(),c=f.build();await c.init();await c.submit(input);await c.test(document.createElement("video"));await c.cancelTest();
 expect(c.view.draft?.state).toBe("tested");expect(f.api.cancelTest).not.toHaveBeenCalled();
});
it("reload after a lost unsaved draft adopts the new epoch and requires explicit re-entry",async()=>{
 const f=fixture(),c=f.build();await c.init();await c.submit(input);
 f.setCaps({...caps,runtime_epoch:"restarted"});vi.mocked(f.api.operation).mockRejectedValue(new ManagementFailure("draft_lost"));
 const reloaded=f.build();await reloaded.init();expect(reloaded.view.draft).toBeUndefined();expect(reloaded.view.error).toBe("draft_lost");expect(reloaded.capabilities?.runtime_epoch).toBe("restarted");expect(f.api.create).toHaveBeenCalledOnce();
});
it("a missing same-runtime Save may only explicitly retry its exact original inputs/key",async()=>{
 const f=fixture(),c=f.build();await c.init();await c.submit(input);await c.test(document.createElement("video"));
 vi.mocked(f.api.save).mockRejectedValueOnce(new ManagementFailure());
 await c.save();expect(c.view.canRetrySave).toBe(true);expect(c.view.unresolved).toBe(true);
 const original=vi.mocked(f.api.save).mock.calls[0]![0];await c.save();expect(f.api.save).toHaveBeenCalledOnce();
 await c.retrySave();expect(f.api.save).toHaveBeenCalledTimes(2);expect(vi.mocked(f.api.save).mock.calls[1]![0]).toEqual(original);expect(c.view.saved?.camera_id).toBe("candidate");
});
it("confirmed CSRF rejection and absent operation restore the prior recoverable draft",async()=>{
 const f=fixture(),c=f.build();await c.init();await c.submit(input);await c.test(document.createElement("video"));
 vi.mocked(f.api.save).mockRejectedValueOnce(new ManagementFailure("csrf_invalid"));await c.save();
 expect(c.view.unresolved).toBe(false);expect(c.view.draft?.state).toBe("tested");expect(f.storage.read(caps)?.kind).toBe("complete");
 await c.save();expect(c.view.saved?.camera_id).toBe("candidate");
});

it("serializes Save rebase and ignores concurrent submissions without losing its key",async()=>{
 const f=fixture(),c=f.build();await c.init();await c.submit(input);await c.test(document.createElement("video"));
 let finish!:(value:typeof revision)=>void;
 f.refresh.mockImplementationOnce(()=>new Promise(resolve=>{finish=resolve}));
 const saving=c.save();await c.save();await c.submit(input);await c.importFiles(new File(["yaml"],"camera.yaml"));await c.cancel();
 expect(f.api.patch).not.toHaveBeenCalled();expect(f.api.import).not.toHaveBeenCalled();expect(f.api.cancel).not.toHaveBeenCalled();
 finish(revision);await saving;expect(f.api.save).toHaveBeenCalledOnce();expect(c.view.saved?.camera_id).toBe("candidate");
});
it("does not settle an in-flight create when a second submit arrives",async()=>{
 const f=fixture(),c=f.build();await c.init();
 const create=vi.mocked(f.api.create).getMockImplementation()!;
 let finish!:()=>void;
 vi.mocked(f.api.create).mockImplementationOnce(async request=>{await new Promise<void>(resolve=>{finish=resolve});return create(request)});
 const pending=c.submit(input);await c.submit(input);expect(c.view.unresolved).toBe(true);expect(f.api.operation).not.toHaveBeenCalled();
 finish();await pending;expect(f.api.create).toHaveBeenCalledOnce();expect(c.view.unresolved).toBe(false);
});

it("late status from a canceled preview cannot close or update its replacement",async()=>{
 const f=fixture();let release!:(value:TestResult)=>void;
 let firstStatus:TestResult|undefined;
 const status=vi.mocked(f.api.testStatus).getMockImplementation()!;
 vi.mocked(f.api.testStatus).mockImplementationOnce(async()=>{firstStatus=await status("draft","test");return new Promise(resolve=>{release=resolve})});
 let secondOffer!:(offer:{type:"offer";sdp:string})=>void;
 const first={...f.preview,close:vi.fn()},second={...f.preview,offer:vi.fn(()=>new Promise<{type:"offer";sdp:string}>(resolve=>{secondOffer=resolve})),close:vi.fn()};let previews=0;
 const c=new ManagementController(f.api,f.storage,vi.fn(),f.refresh,()=>++previews===1?first:second);
 await c.init();await c.submit(input);const old=c.test(document.createElement("video"));await vi.waitFor(()=>expect(release).toBeDefined());
 await c.cancelTest();const next=c.test(document.createElement("video"));release(firstStatus!);await old;
 expect(second.close).not.toHaveBeenCalled();secondOffer({type:"offer",sdp:"v=0\r\n"});await next;
 expect(c.view.draft?.state).toBe("tested");expect(second.answer).toHaveBeenCalled();expect(f.api.complete).toHaveBeenCalledOnce();
});
