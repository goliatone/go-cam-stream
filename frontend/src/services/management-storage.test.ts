import { beforeEach, expect, it } from "vitest";
import { CREDENTIAL_RECOVERY_KEY, RecoveryStorage, RECOVERY_STORAGE_KEY } from "./management-storage";
const caps={can_manage_cameras:true,recovery_scope:"scope",runtime_epoch:"epoch"};
beforeEach(()=>sessionStorage.clear());
it("whitelists nested recovery fields and discards other principals' records",()=>{
 const storage=new RecoveryStorage(sessionStorage);
 storage.write({scope:"scope",epoch:"epoch",key:"operation",kind:"save",expires:0,catalog:{runtime_epoch:"epoch",generation:1,password:"PRIVATE"},ref:{camera_id:"camera",stream_id:"default",endpoint:"PRIVATE"},name:"PRIVATE"} as never);
 expect(sessionStorage.getItem(RECOVERY_STORAGE_KEY)).not.toContain("PRIVATE");expect(storage.read(caps)?.key).toBe("operation");
 expect(storage.read({...caps,recovery_scope:"other"})).toBeUndefined();expect(sessionStorage.getItem(RECOVERY_STORAGE_KEY)).toBeNull();
});
it.each(['{',JSON.stringify({scope:"scope",key:"bad/path"}), 'x'.repeat(2049)])("clears malformed records without replaying anything",raw=>{
 sessionStorage.setItem(RECOVERY_STORAGE_KEY,raw);expect(new RecoveryStorage(sessionStorage).read(caps)).toBeUndefined();expect(sessionStorage.getItem(RECOVERY_STORAGE_KEY)).toBeNull();
});
it("keeps credential and reconnect records separate and reads past a corrupt legacy record",()=>{
 const storage=new RecoveryStorage(sessionStorage);
 const reconnect=new RecoveryStorage(sessionStorage,RECOVERY_STORAGE_KEY+".reset");
 const record={scope:"scope",epoch:"epoch",key:"rotation",kind:"save",expires:0,credentialUpdate:true} as const;
 storage.write(record);
 reconnect.write({...record,key:"reset",kind:"recovery",ref:{camera_id:"camera",stream_id:"default"}});
 sessionStorage.setItem(RECOVERY_STORAGE_KEY,"{");
 expect(reconnect.read(caps)?.key).toBe("reset");
 expect(storage.read(caps)).toEqual(record);
 expect(sessionStorage.getItem(RECOVERY_STORAGE_KEY)).toBeNull();
 reconnect.clear();
 expect(sessionStorage.getItem(CREDENTIAL_RECOVERY_KEY)).not.toBeNull();
});
