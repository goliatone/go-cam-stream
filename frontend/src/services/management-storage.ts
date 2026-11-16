import type { Capabilities, CatalogRevision, OperationKind, StreamRef } from "./contracts";
import { ManagementFailure } from "./management-api";

export interface RecoveryRecord {
  scope: string;
  epoch: string;
  key: string;
  kind: OperationKind;
  expires: number;
  draft?: string;
  candidate?: string;
  test?: string;
  revision?: number;
  catalog?: CatalogRevision;
  ref?: StreamRef;
}
export const RECOVERY_STORAGE_KEY = "camstream.camera-operation.v1";
const kinds = ["create","import","patch","cancel","test","complete","cancel_test","save","recovery"];
const id = (value:unknown): value is string => typeof value === "string" && /^[a-zA-Z0-9_-]{1,128}$/.test(value);
// Pick only permitted fields even if a caller passes an object with extra keys.
export function recoveryRecord(record: RecoveryRecord): RecoveryRecord {
  return {scope:record.scope,epoch:record.epoch,key:record.key,kind:record.kind,expires:record.expires,
    ...(record.draft?{draft:record.draft}:{}),...(record.candidate?{candidate:record.candidate}:{}),...(record.test?{test:record.test}:{}),...(record.revision===undefined?{}:{revision:record.revision}),
    ...(record.catalog?{catalog:{runtime_epoch:record.catalog.runtime_epoch,generation:record.catalog.generation}}:{}),
    ...(record.ref?{ref:{camera_id:record.ref.camera_id,stream_id:record.ref.stream_id}}:{})};
}
export class RecoveryStorage {
  constructor(readonly storage: Storage) {}
  check() {
    try { const key=RECOVERY_STORAGE_KEY+".check";this.storage.setItem(key,"1");if(this.storage.getItem(key)!=="1")throw Error();this.storage.removeItem(key); }
    catch { throw new ManagementFailure("recovery_storage_unavailable"); }
  }
  write(record:RecoveryRecord) {
    try { const text=JSON.stringify(recoveryRecord(record));this.storage.setItem(RECOVERY_STORAGE_KEY,text);if(this.storage.getItem(RECOVERY_STORAGE_KEY)!==text)throw Error(); }
    catch { throw new ManagementFailure("recovery_storage_unavailable"); }
  }
  clear() { try{this.storage.removeItem(RECOVERY_STORAGE_KEY)}catch{throw new ManagementFailure("recovery_storage_unavailable")} }
  read(caps:Capabilities):RecoveryRecord|undefined {
    try {
      const raw=this.storage.getItem(RECOVERY_STORAGE_KEY);if(!raw)return;
      if(raw.length>2048){this.clear();return;}
      const r:unknown=JSON.parse(raw);
      if(!r || typeof r!=="object" || Array.isArray(r)){this.clear();return;}
      const value=r as RecoveryRecord;
      if(value.scope!==caps.recovery_scope || !id(value.epoch)||!id(value.key)||!kinds.includes(value.kind)||!Number.isFinite(value.expires)) {this.clear();return;}
      for(const v of [value.draft,value.candidate,value.test])if(v!==undefined&&!id(v)){this.clear();return;}
      if(value.revision!==undefined&&(!Number.isSafeInteger(value.revision)||value.revision<1)){this.clear();return;}
      if(value.catalog&&(!id(value.catalog.runtime_epoch)||!Number.isSafeInteger(value.catalog.generation)||value.catalog.generation<1)){this.clear();return;}
      if(value.ref&&(!id(value.ref.camera_id)||!id(value.ref.stream_id))){this.clear();return;}
      // Save anchors outlive both draft and ordinary receipt retention.
      if(value.kind!=="save"&&value.kind!=="recovery"&&Date.now()>value.expires){this.clear();return;}
      return recoveryRecord(value);
    } catch(error) { if(error instanceof ManagementFailure)throw error;this.clear();return; }
  }
}
export function operationKey():string {
  const bytes=crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes,b=>b.toString(16).padStart(2,"0")).join("");
}
