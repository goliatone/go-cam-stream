import { useEffect, useRef, useState } from "preact/hooks";
import type { AppProps } from "../app";
import type { CatalogRevision, StreamRef } from "../services/contracts";
import { ManagementClient, ManagementFailure } from "../services/management-api";
import { ManagementController } from "../services/management-controller";
import { RecoveryStorage, RECOVERY_STORAGE_KEY } from "../services/management-storage";
import { SourceRecovery } from "../services/source-recovery";
import { CameraRecovery } from "../components/camera-recovery";

export interface ManagementHostProps {
  viewer: AppProps;
  refresh(minimum?:CatalogRevision):Promise<CatalogRevision|undefined>;
  recovered():Promise<void>;
  onChange(props:Partial<AppProps>):void;
}
// This complete integration is a separately loaded module. The existing viewer
// keeps rendering while management capability and recovery status are fetched.
export default function ManagementHost({viewer,refresh,recovered,onChange}:ManagementHostProps) {
  const [version,update]=useState(0),[open,setOpen]=useState(false),[watchSelection,setWatchSelection]=useState<StreamRef>();
  const latest=useRef({viewer,refresh,recovered});latest.current={viewer,refresh,recovered};
  const recovery=useRef<SourceRecovery | undefined>(undefined);
  const [manager]=useState(()=>{
    const api=new ManagementClient();
    let storage:Storage;
    try{storage=sessionStorage}catch{storage={getItem(){throw Error()},setItem(){throw Error()},removeItem(){throw Error()}} as unknown as Storage}
    return new ManagementController(api,new RecoveryStorage(storage),()=>update(n=>n+1),async minimum=>{
      const revision=await latest.current.refresh(minimum);if(!revision)throw new ManagementFailure();return revision;
    });
  });
  useEffect(()=>{
    let active=true;
    void manager.init().then(async()=>{
      if(!active)return;
      const caps=manager.capabilities;
      if(caps&&manager.view.storageReady){
        // Reset recovery has its own slot, so it never overwrites an unsaved draft.
        const storage=manager.storage.storage;
        const resetStorage=new RecoveryStorage({
          getItem:key=>storage.getItem(key.replace(RECOVERY_STORAGE_KEY,RECOVERY_STORAGE_KEY+".reset")),
          setItem:(key,value)=>storage.setItem(key.replace(RECOVERY_STORAGE_KEY,RECOVERY_STORAGE_KEY+".reset"),value),
          removeItem:key=>storage.removeItem(key.replace(RECOVERY_STORAGE_KEY,RECOVERY_STORAGE_KEY+".reset")),
        } as Storage);
        recovery.current=new SourceRecovery(manager.api,resetStorage,caps,()=>{if(active)update(n=>n+1)},async()=>{await latest.current.refresh()},async ref=>{
          const selected=latest.current.viewer.viewer?.selection;
          if(selected?.camera_id===ref.camera_id&&selected.stream_id===ref.stream_id)await latest.current.recovered();
        });
        await recovery.current.init();
        if(active)update(n=>n+1);
      }
      if(active&&(manager.view.draft||manager.view.saved||manager.view.unresolved))setOpen(true);
    });
    const hidden=()=>{if(document.visibilityState==="hidden")void manager.cancelTest()};
    const pagehide=()=>{void manager.cancelTest()};
    document.addEventListener("visibilitychange",hidden);window.addEventListener("pagehide",pagehide);
    return()=>{active=false;document.removeEventListener("visibilitychange",hidden);window.removeEventListener("pagehide",pagehide);manager.dispose()};
  },[manager]);
  const selected=viewer.viewer?.selection;
  const stream=viewer.catalog.cameras.find(c=>c.id===selected?.camera_id)?.streams.find(s=>s.id===selected?.stream_id);
  const reset=recovery.current;
  useEffect(()=>{onChange({watchSelection,
    onAddCamera:manager.capabilities?.can_manage_cameras?()=>{manager.reset();setOpen(true)}:undefined,
    management:open?{view:manager.view,actions:{
      submit:input=>{void manager.submit(input)},importFiles:(yaml,env)=>{void manager.importFiles(yaml,env)},test:video=>{void manager.test(video)},
      retrySave:()=>{void manager.retrySave()},abandon:()=>manager.abandon(),cancelTest:()=>{void manager.cancelTest()},save:()=>{void manager.save()},recover:()=>{void manager.recover()},
      cancel:()=>{void manager.cancel().then(()=>{if(!manager.view.unresolved)setOpen(false)})},
      close:()=>{setOpen(false);void manager.close().catch(()=>{})},
      watch:id=>{void manager.watch().then(()=>{setWatchSelection({camera_id:id,stream_id:"default"});setOpen(false);void manager.close().catch(()=>{})}).catch(()=>{})},
    }}:undefined,
    recoveryControl:selected&&stream?.can_reconnect&&(stream.recovery_required||reset?.unresolved)?<CameraRecovery busy={reset?.busy??false} unresolved={reset?.unresolved??false} error={reset?.error} disabled={!manager.view.storageReady} onConfirm={()=>{if(reset)void reset.confirm(selected)}} onCheck={()=>{if(reset)void reset.check()}}/>:undefined,
  });},[version,open,watchSelection,stream?.can_reconnect,stream?.recovery_required,selected?.camera_id,selected?.stream_id]);
  return null;
}
