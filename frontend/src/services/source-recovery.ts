import type { Capabilities, ManagementErrorCode, StreamRef } from "./contracts";
import { ManagementFailure, managementCode, type ManagementAPI } from "./management-api";
import { operationKey, RecoveryStorage, type RecoveryRecord } from "./management-storage";

export class SourceRecovery {
  busy=false; unresolved=false; error:ManagementErrorCode|undefined;
  #record:RecoveryRecord|undefined;
  constructor(readonly api:ManagementAPI, readonly storage:RecoveryStorage, public caps:Capabilities, readonly changed:()=>void, readonly refreshed:()=>Promise<void>, readonly recovered:(ref:StreamRef)=>Promise<void>) {}
  async init() { try{this.#record=this.storage.read(this.caps);if(this.#record){this.unresolved=true;await this.check()}}catch(error){this.error=managementCode(error);this.changed()} }
  async confirm(ref:StreamRef) {
    if(this.busy||this.unresolved)return;
    try {
      this.storage.check();
      this.#record={scope:this.caps.recovery_scope,epoch:this.caps.runtime_epoch,key:operationKey(),kind:"recovery",expires:Date.now()+600000,ref};
      this.storage.write(this.#record);this.busy=true;this.unresolved=true;this.error=undefined;this.changed();
      await this.api.reconnect(ref,{operation_key:this.#record.key,runtime_epoch:this.#record.epoch});
      await this.#success();
    }catch(error){this.error=managementCode(error);this.busy=false;if(this.#record)await this.check()}
    finally{this.busy=false;this.changed()}
  }
  async #success() {
    const ref=this.#record?.ref;
    this.storage.clear();this.#record=undefined;this.unresolved=false;this.error=undefined;
    try{await this.refreshed()}finally{if(ref)await this.recovered(ref)}
  }
  async check() {
    if(!this.#record||this.busy)return;
    this.busy=true;this.changed();const r=this.#record;
    try {
      const until=Date.now()+20000;
      for(;;) {
        const op=await this.api.operation(r.key,r.epoch,undefined,true);
        if(op.state==="succeeded"){await this.#success();return;}
        if(op.state==="not_committed"){this.unresolved=false;this.error=op.error?.code;this.storage.clear();this.#record=undefined;await this.refreshed();return;}
        if(op.state==="not_found"||op.state==="expired")throw new ManagementFailure("recovery_lost");
        if(Date.now()>=until){this.error="operation_in_progress";return;}
        await new Promise(resolve=>setTimeout(resolve,500));
      }
    }catch(error){
      this.error=managementCode(error);
      if(this.error==="recovery_lost"||this.error==="access_denied"){
        try{await this.refreshed();this.caps=await this.api.capabilities();this.storage.clear();this.#record=undefined;this.unresolved=false;}catch(refreshError){this.error=managementCode(refreshError)}
      }
    }finally{this.busy=false;this.changed()}
  }
}
