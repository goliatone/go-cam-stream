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
  credentialUpdate?: true;
}
export const RECOVERY_STORAGE_KEY = "camstream.camera-operation.v1";
export const CREDENTIAL_RECOVERY_KEY = RECOVERY_STORAGE_KEY+".credentials";
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
  readonly #keys: [string] | [string, string];
  constructor(readonly storage: Storage, key = RECOVERY_STORAGE_KEY) {
    this.#keys=key===RECOVERY_STORAGE_KEY?[key,CREDENTIAL_RECOVERY_KEY]:[key];
  }
  check() {
    try { const key=this.#keys[0]+".check";this.storage.setItem(key,"1");if(this.storage.getItem(key)!=="1")throw Error();this.storage.removeItem(key); }
    catch { throw new ManagementFailure("recovery_storage_unavailable"); }
  }
  write(record:RecoveryRecord) {
    const [normal,credential]=this.#keys;
    const key=record.credentialUpdate&&credential?credential:normal;
    try {
      const text=JSON.stringify(recoveryRecord(record));
      this.storage.setItem(key,text);
      if(this.storage.getItem(key)!==text)throw Error();
      if(credential)this.storage.removeItem(key===credential?normal:credential);
    }
    catch { throw new ManagementFailure("recovery_storage_unavailable"); }
  }
  clear() {
    for(const key of this.#keys)this.#remove(key);
  }
  #remove(key:string) {
    try{this.storage.removeItem(key)}catch{throw new ManagementFailure("recovery_storage_unavailable")}
  }
  read(caps:Capabilities):RecoveryRecord|undefined {
    for(const key of this.#keys) {
      const record=this.#read(caps,key);
      if(record)return record;
    }
  }
  #read(caps:Capabilities,key:string):RecoveryRecord|undefined {
    try {
      const raw=this.storage.getItem(key);if(!raw)return;
      if(raw.length>2048)throw Error();
      const value=JSON.parse(raw) as RecoveryRecord;
      if(value.scope!==caps.recovery_scope || !id(value.epoch)||!id(value.key)||!kinds.includes(value.kind)||!Number.isFinite(value.expires))throw Error();
      for(const v of [value.draft,value.candidate,value.test])if(v!==undefined&&!id(v))throw Error();
      if(value.revision!==undefined&&(!Number.isSafeInteger(value.revision)||value.revision<1))throw Error();
      if(value.catalog&&(!id(value.catalog.runtime_epoch)||!Number.isSafeInteger(value.catalog.generation)||value.catalog.generation<1))throw Error();
      if(value.ref&&(!id(value.ref.camera_id)||!id(value.ref.stream_id)))throw Error();
      // Save anchors outlive both draft and ordinary receipt retention.
      if(value.kind!=="save"&&value.kind!=="recovery"&&Date.now()>value.expires)throw Error();
      const record=recoveryRecord(value);
      if(key===CREDENTIAL_RECOVERY_KEY)record.credentialUpdate=true;
      return record;
    } catch { this.#remove(key); }
  }
}
export function operationKey():string {
  const bytes=crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes,b=>b.toString(16).padStart(2,"0")).join("");
}
