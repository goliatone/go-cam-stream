import { beforeEach, expect, it, vi } from "vitest";
import type { Capabilities } from "./contracts";
import { ManagementFailure, type ManagementAPI } from "./management-api";
import { RecoveryStorage } from "./management-storage";
import { SourceRecovery } from "./source-recovery";
const caps:Capabilities={runtime_epoch:"epoch",recovery_scope:"scope",can_manage_cameras:false},ref={camera_id:"camera",stream_id:"default"};
beforeEach(()=>sessionStorage.clear());
it("lost reset response/reload resolves once without issuing another reset",async()=>{
 const reconnect=vi.fn().mockRejectedValue(new ManagementFailure());
 const api={reconnect,operation:vi.fn().mockResolvedValue({state:"succeeded",recovery:{affected:[ref],closed_sessions:[]}})} as unknown as ManagementAPI;
 const refresh=vi.fn(async()=>{}),recovered=vi.fn(async()=>{});
 const recovery=new SourceRecovery(api,new RecoveryStorage(sessionStorage),caps,vi.fn(),refresh,recovered);
 await recovery.confirm(ref);expect(reconnect).toHaveBeenCalledOnce();expect(api.operation).toHaveBeenCalledOnce();expect(recovered).toHaveBeenCalledWith(ref);expect(recovery.unresolved).toBe(false);
});
it("unknown outcome blocks a second reset and keeps its key for reload",async()=>{
 const api={reconnect:vi.fn().mockRejectedValue(new ManagementFailure()),operation:vi.fn().mockRejectedValue(new ManagementFailure())} as unknown as ManagementAPI;
 const storage=new RecoveryStorage(sessionStorage),r=new SourceRecovery(api,storage,caps,vi.fn(),async()=>{},async()=>{});
 await r.confirm(ref);const key=storage.read(caps)!.key;await r.confirm(ref);expect(api.reconnect).toHaveBeenCalledOnce();expect(r.unresolved).toBe(true);
 const loaded=new SourceRecovery(api,storage,caps,vi.fn(),async()=>{},async()=>{});await loaded.init();expect(storage.read(caps)!.key).toBe(key);expect(api.reconnect).toHaveBeenCalledOnce();
});
it("restart requires refreshed authority and a new explicit confirmation",async()=>{
 const api={operation:vi.fn().mockRejectedValue(new ManagementFailure("recovery_lost")),capabilities:vi.fn(async()=>({...caps,runtime_epoch:"new"})),reconnect:vi.fn(async()=>({affected:[ref],closed_sessions:[]}))} as unknown as ManagementAPI;
 const storage=new RecoveryStorage(sessionStorage);storage.write({scope:"scope",epoch:"epoch",key:"operation",kind:"recovery",expires:Date.now()+1000,ref});
 const refresh=vi.fn(async()=>{}),r=new SourceRecovery(api,storage,caps,vi.fn(),refresh,async()=>{});await r.init();expect(api.reconnect).not.toHaveBeenCalled();expect(refresh).toHaveBeenCalled();expect(r.unresolved).toBe(false);
 await r.confirm(ref);expect(api.reconnect).toHaveBeenCalledWith(ref,expect.objectContaining({runtime_epoch:"new"}));
});

it("replaces the old peer after confirmed reset even when catalog refresh fails",async()=>{
 const api={reconnect:vi.fn(async()=>({affected:[ref],closed_sessions:[]}))} as unknown as ManagementAPI;
 const recovered=vi.fn(async()=>{}),r=new SourceRecovery(api,new RecoveryStorage(sessionStorage),caps,vi.fn(),async()=>{throw new ManagementFailure()},recovered);
 await r.confirm(ref);expect(recovered).toHaveBeenCalledWith(ref);expect(r.unresolved).toBe(false);expect(api.reconnect).toHaveBeenCalledOnce();
});
