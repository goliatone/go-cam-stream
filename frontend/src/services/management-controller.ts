import type { ManagementView } from "../components/camera-management";
import type { Capabilities, CatalogRevision, Credentials, DraftInput, DraftReview, Mutation, OperationKind, OperationResult, SaveCameraResponse } from "./contracts";
import { duplicateCameraID, ManagementFailure, managementCode, type ManagementAPI } from "./management-api";
import { BrowserPreview, type Preview } from "./management-preview";
import { operationKey, RecoveryStorage, type RecoveryRecord } from "./management-storage";

const delay = (ms:number) => new Promise<void>(resolve => setTimeout(resolve,ms));
export class ManagementController {
  view: ManagementView = {phase:"editing",storageReady:false};
  capabilities: Capabilities | undefined;
  #record: RecoveryRecord | undefined;
  #previous: RecoveryRecord | undefined;
  #rejected: boolean = false;
  #preview: Preview | undefined;
  #generation = 0;
  #disposed = false;
  #mutating = false;
  constructor(readonly api:ManagementAPI, readonly storage:RecoveryStorage,
    readonly changed:()=>void, readonly refresh:(minimum?:CatalogRevision)=>Promise<CatalogRevision>,
    readonly previewFactory:(video:HTMLVideoElement)=>Preview=video=>new BrowserPreview(video)) {}
  #show(change:Partial<ManagementView>) { this.view={...this.view,...change};if(!this.#disposed)this.changed(); }
  async init() {
    try {
      this.capabilities=await this.api.capabilities();this.storage.check();
      this.#show({storageReady:true});this.#record=this.storage.read(this.capabilities);
      if(this.#record)await this.recover();
    } catch(error){this.#error(error)}
    this.changed();
  }
  #error(error:unknown) {
    const code=managementCode(error);
    if(code==="access_denied") {
      try{this.storage.clear()}catch{}
      this.#record=undefined;
      if(this.capabilities && !this.view.credentialTarget && !this.view.draft?.credential_update) {
        this.capabilities={...this.capabilities,can_manage_cameras:false};
      }
    }
    this.#show({error:code,duplicateCamera: error instanceof ManagementFailure ? duplicateCameraID(code,error.cameraID) : undefined,
      busy:false,...(code==="recovery_storage_unavailable"?{storageReady:false}:{}),...(code==="access_denied"?{unresolved:false}:{})});
  }
  #begin(kind:OperationKind,extra:Partial<RecoveryRecord>={}):Mutation {
    if(!this.capabilities?.can_manage_cameras)throw new ManagementFailure("access_denied");
    if(!this.view.storageReady)throw new ManagementFailure("recovery_storage_unavailable");
    if(this.view.unresolved||this.#mutating)throw new ManagementFailure("operation_in_progress");
    const d=this.view.draft;
    const record:RecoveryRecord={scope:this.capabilities.recovery_scope,epoch:this.capabilities.runtime_epoch,key:operationKey(),kind,expires:Date.now()+20*60*1000,
      ...(d?{draft:d.id,candidate:d.candidate_id,revision:d.revision}:{}),...extra};
    this.storage.write(record);this.#previous=this.#record;this.#rejected=false;this.#record=record;this.#mutating=true;
    this.#show({busy:true,unresolved:true,error:undefined,duplicateCamera:undefined,canAbandon:false,canRetrySave:false});
    return {operation_key:record.key,runtime_epoch:record.epoch};
  }
  #rememberDraft(draft:DraftReview) {
    if(this.#record) {this.#record={...this.#record,draft:draft.id,candidate:draft.candidate_id,revision:draft.revision,expires:Date.parse(draft.expires_at)+5*60*1000};this.storage.write(this.#record)}
    this.#show({draft,test:draft.test,phase:draft.state==="canceled"?"canceled":"review",busy:false,unresolved:false,error:undefined,duplicateCamera:undefined});
  }
  async #failed(error:unknown) {
    this.#mutating=false;this.#rejected=["csrf_invalid","invalid_argument","import_invalid"].includes(managementCode(error));this.#error(error);
    // Every lost/error reply resolves by reading the original key, never POSTing again.
    if(this.#record&&this.view.storageReady&&managementCode(error)!=="access_denied")await this.recover();
  }
  async submit(input:DraftInput) {
    if(this.view.busy||this.view.unresolved||this.#mutating)return;
    try {
      const d=this.view.draft,m=this.#begin(d?"patch":"create");
      const result=d?await this.api.patch(d.id,{...m,...input,expected_revision:d.revision}):await this.api.create({...m,...input});
      this.#rememberDraft(result);
    } catch(error){await this.#failed(error)} finally{this.#mutating=false}
  }
  async openCredentials(target: NonNullable<ManagementView["credentialTarget"]>) {
    if (this.view.busy || this.view.unresolved || this.#mutating) return;
    if (this.view.draft && !this.view.saved && this.view.phase !== "canceled") {
      if (!await this.cancel()) return;
    }
    this.reset();
    this.#show({ phase: "editing", credentialTarget: target, error: undefined, duplicateCamera: undefined });
  }
  async submitCredentials(credentials: Credentials) {
    if (this.view.busy || this.view.unresolved || this.#mutating) return;
    const draft = this.view.draft?.credential_update ? this.view.draft : undefined;
    const cameraID = draft?.candidate_id ?? this.view.credentialTarget?.id;
    if (!cameraID) return;
    try {
      const mutation = this.#begin(draft ? "patch" : "create");
      const result = draft
        ? await this.api.patch(draft.id, { ...mutation, expected_revision: draft.revision, credentials })
        : await this.api.credentialDraft({ ...mutation, camera_id: cameraID, credentials });
      this.#rememberDraft(result);
    } catch (error) {
      await this.#failed(error);
    } finally {
      this.#mutating = false;
    }
  }
  async importFiles(yaml:File,env?:File) {
    if(this.view.busy||this.view.unresolved||this.#mutating)return;
    try{const m=this.#begin("import");this.#show({phase:"importing"});this.#rememberDraft(await this.api.import(m,yaml,env))}
    catch(error){await this.#failed(error)}finally{this.#mutating=false}
  }
  async save() {
    if(this.view.unresolved||this.view.busy||this.#mutating)return;
    const d=this.view.draft;if(!d)return;
    // Reserve the UI before the asynchronous rebase, without replacing its key.
    this.#show({busy:true,phase:"saving"});
    let catalog:CatalogRevision;
    try{catalog=await this.refresh()}catch(error){this.#error(error);this.#show({phase:"review"});return;}
    try {
      const m=this.#begin("save",{catalog});this.#show({phase:"saving"});
      const saved=await this.api.save({...m,draft_id:d.id,expected_revision:d.revision,catalog_revision:catalog});
      await this.#saved(saved);
    }catch(error){await this.#failed(error)}finally{this.#mutating=false}
  }
  async retrySave() {
    const r=this.#record;
    if(!this.view.canRetrySave||this.#mutating||!r?.draft||!r.catalog||!r.revision)return;
    this.#mutating=true;this.#show({busy:true,phase:"saving",canRetrySave:false});
    try{await this.#saved(await this.api.save({operation_key:r.key,runtime_epoch:r.epoch,draft_id:r.draft,expected_revision:r.revision,catalog_revision:r.catalog}))}
    catch(error){await this.#failed(error)}finally{this.#mutating=false}
  }
  async #saved(saved:SaveCameraResponse) {
    this.#show({saved,phase:"saved",busy:false,unresolved:false,error:undefined,canRetrySave:false,canAbandon:false});
    // A list failure cannot undo a confirmed durable Save. Watch waits for its row.
    try{await this.refresh(saved.catalog_revision)}catch{this.#show({error:"server_unavailable"})}
  }
  async #lostDraft() {
    this.capabilities=await this.api.capabilities();this.storage.clear();this.#record=undefined;this.#show({draft:undefined,test:undefined,unresolved:false,phase:"editing"});
  }
  async recover() {
    if(this.#mutating)return;
    const r=this.#record;if(!r)return;
    this.#show({phase:"recovering",busy:true,unresolved:true});
    try {
      const until=Date.now()+20000;
      let op:OperationResult;
      for(;;){
        op=await this.api.operation(r.key,r.epoch,r.candidate);
        if(op.state!=="processing"&&op.state!=="outcome_unknown")break;
        if(Date.now()>=until){this.#show({busy:false,error:op.error?.code??"operation_in_progress"});return;}
        await delay(500);if(this.#disposed)return;
      }
      if(op.state==="succeeded"&&op.saved){await this.#saved(op.saved);return;}
      if(op.state==="succeeded"&&op.draft_id) {
        const d=await this.api.draft(op.draft_id);this.#rememberDraft(d);
        if(d.state==="testing"||d.state==="cleaning"||d.state==="cleanup_failed")this.#show({phase:"testing"});
        return;
      }
      if(op.state==="not_committed") {
        const failure={error:op.error?.code,duplicateCamera:duplicateCameraID(op.error?.code,op.error?.camera_id)};
        this.#show({unresolved:false,busy:false,...failure,phase:"review"});
        if(r.draft)try{this.#rememberDraft(await this.api.draft(r.draft));this.#show(failure)}catch(error){if(["draft_lost","draft_expired"].includes(managementCode(error)))await this.#lostDraft();this.#error(error)}
        return;
      }
      if(op.state==="not_found"&&this.#rejected) {
        const previous=this.#previous;
        if(previous){this.storage.write(previous);this.#record=previous;if(previous.draft)this.#rememberDraft(await this.api.draft(previous.draft));}
        else{this.storage.clear();this.#record=undefined;}
        this.#show({busy:false,unresolved:false,phase:this.view.draft?"review":"editing"});return;
      }
      // Unknown/missing records never manufacture a new save or recreate secret input.
      this.#show({busy:false,error:op.state==="expired"?"receipt_expired":"operation_in_progress",canAbandon:r.kind!=="save",canRetrySave:op.state==="not_found"&&r.kind==="save"&&!!r.draft&&!!r.catalog&&!!r.revision&&r.epoch===this.capabilities?.runtime_epoch&&Date.now()<r.expires});
    }catch(error){
      const code=managementCode(error);
      if(code==="draft_lost"||code==="draft_expired") {
        try{await this.#lostDraft()}catch(storageError){this.#error(storageError);return;}
      }
      this.#error(error);
    }
  }
  async test(video:HTMLVideoElement) {
    if(this.view.busy||this.view.unresolved)return;
    const d=this.view.draft;if(!d)return;
    const generation=++this.#generation;
    let preview:Preview|undefined, operation:string|undefined;
    try {
      this.#preview?.close();preview=this.previewFactory(video);this.#preview=preview;
      this.#show({phase:"testing",busy:true,error:undefined});
      const offer=await preview.offer();if(generation!==this.#generation)return;
      const m=this.#begin("test");operation=m.operation_key;
      const result=await this.api.test(d.id,{...m,expected_revision:d.revision,offer});
      this.#mutating=false;
      if(this.#record){this.#record={...this.#record,test:result.id};this.storage.write(this.#record)}
      this.#show({test:result,unresolved:false,busy:false});
      if(generation!==this.#generation){await this.cancelTest();return;}
      if(result.answer)await preview.answer(result.answer);
      const until=Math.min(Date.parse(result.expires_at),Date.now()+21000);
      while(generation===this.#generation&&!this.#disposed) {
        const status=await this.api.testStatus(d.id,result.id);if(generation!==this.#generation)return;this.#show({test:status});
        if(["failed","canceled","passed"].includes(status.stage)&&status.cleanup==="confirmed") {this.#rememberDraft(await this.api.draft(d.id));return;}
        if(Date.now()>=until) {await this.cancelTest();return;}
        const evidence=await preview.evidence();
        if(generation!==this.#generation)return;
        if(evidence&&status.source_media_observed) {
          const complete=this.#begin("complete",{test:result.id});operation=complete.operation_key;
          const passed=await this.api.complete(d.id,result.id,{...complete,expected_revision:d.revision,evidence});
          this.#show({test:passed});this.#rememberDraft(await this.api.draft(d.id));
          this.#mutating=false;if(generation!==this.#generation)await this.cancelTest();return;
        }
        await delay(250);
      }
    }catch(error){if(operation?this.#record?.key===operation:generation===this.#generation)await this.#failed(error)}finally{
      if(operation&&this.#record?.key===operation)this.#mutating=false;
      preview?.close();if(this.#preview===preview){this.#preview=undefined;this.#show({busy:false})}
    }
  }
  async cancelTest() {
    if(!this.#preview&&this.view.phase!=="testing"&&!["testing","cleaning","cleanup_failed"].includes(this.view.draft?.state??""))return;
    ++this.#generation;this.#preview?.close();
    // Negotiation is settled by its original operation before a cancel can be keyed.
    if(this.#mutating){this.#show({unresolved:true});return;}
    const d=this.view.draft,t=this.view.test??d?.test;if(!d||!t){this.#show({phase:"review",busy:false});return;}
    try{const m=this.#begin("cancel_test",{test:t.id});this.#show({test:await this.api.cancelTest(d.id,t.id,m)});this.#rememberDraft(await this.api.draft(d.id))}
    catch(error){await this.#failed(error)}finally{this.#mutating=false}
  }
  async cancel() {
    if(this.view.unresolved||this.#mutating||(this.view.busy&&this.view.phase==="saving"))return false;
    ++this.#generation;this.#preview?.close();
    const d=this.view.draft;
    if(!d){try{this.storage.clear();this.#record=undefined;this.#show({phase:"canceled"})}catch(error){this.#error(error)}return this.view.phase==="canceled";}
    try{const m=this.#begin("cancel");this.#rememberDraft(await this.api.cancel(d.id,m));this.storage.clear();this.#record=undefined}
    catch(error){await this.#failed(error)}finally{this.#mutating=false}
    return this.view.phase==="canceled";
  }
  async close() {
    if(this.view.saved){try{this.storage.clear();this.#record=undefined}catch(error){this.#error(error)}return;}
    if(this.view.phase==="testing")await this.cancelTest();
    if(!this.view.unresolved&&!this.#mutating)await this.cancel();
  }
  abandon() {
    if(!this.view.canAbandon||this.#record?.kind==="save")return;
    try{this.storage.clear();this.#record=undefined;this.#show({phase:"editing",draft:undefined,test:undefined,saved:undefined,error:undefined,unresolved:false,canAbandon:false})}catch(error){this.#error(error)}
  }
  reset() {
    if(!this.view.unresolved&&!this.#mutating&&(this.view.saved||this.view.phase==="canceled")) {
      try{this.storage.clear();this.#record=undefined;this.#show({phase:"editing",draft:undefined,test:undefined,saved:undefined,credentialTarget:undefined,error:undefined,duplicateCamera:undefined,canAbandon:false})}catch(error){this.#error(error)}
    }
  }
  async watch() {if(this.view.saved)await this.refresh(this.view.saved.catalog_revision);}
  dispose() {this.#disposed=true;++this.#generation;this.#preview?.close();}
}
