import { useEffect, useRef, useState } from "preact/hooks";
import type { AppProps } from "../app";
import type { CatalogRevision, StreamRef } from "../services/contracts";
import { ManagementClient, ManagementFailure } from "../services/management-api";
import { ManagementController } from "../services/management-controller";
import { RecoveryStorage, RECOVERY_STORAGE_KEY } from "../services/management-storage";
import { SourceRecovery } from "../services/source-recovery";
import { CameraRecovery } from "../components/camera-recovery";
import type { ManagementMode } from "../components/camera-management";

export interface ManagementHostProps {
  viewer: AppProps;
  refresh(minimum?:CatalogRevision):Promise<CatalogRevision|undefined>;
  recovered():Promise<void>;
  onChange(props:Partial<AppProps>):void;
}
/** How often the settings drawer asks how many people watch its camera. */
const VIEWERS_MS = 4000;
// This complete integration is a separately loaded module. The existing viewer
// keeps rendering while management capability and recovery status are fetched.
export default function ManagementHost({viewer,refresh,recovered,onChange}:ManagementHostProps) {
  const [version,update]=useState(0),[open,setOpen]=useState(false),[mode,setMode]=useState<ManagementMode>("manual"),[watchSelection,setWatchSelection]=useState<StreamRef>();
  // The stream whose Reconnect confirmation is open, and the drawer camera's viewers.
  const [recovering,setRecovering]=useState<StreamRef>(),[viewers,setViewers]=useState<{id:string;count?:number|undefined}>(),[ask,askAgain]=useState(0);
  const latest=useRef({viewer,refresh,recovered});latest.current={viewer,refresh,recovered};
  const recovery=useRef<SourceRecovery | undefined>(undefined);
  const [client]=useState(()=>new ManagementClient());
  const [manager]=useState(()=>{
    let storage:Storage;
    try{storage=sessionStorage}catch{storage={getItem(){throw Error()},setItem(){throw Error()},removeItem(){throw Error()}} as unknown as Storage}
    return new ManagementController(client,new RecoveryStorage(storage),()=>update(n=>n+1),async minimum=>{
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
        const resetStorage=new RecoveryStorage(manager.storage.storage,RECOVERY_STORAGE_KEY+".reset");
        recovery.current=new SourceRecovery(manager.api,resetStorage,caps,()=>{if(active)update(n=>n+1)},async()=>{await latest.current.refresh()},async ref=>{
          const selected=latest.current.viewer.viewer?.selection;
          if(selected?.camera_id===ref.camera_id&&selected.stream_id===ref.stream_id)await latest.current.recovered();
        });
        await recovery.current.init();
        if(active)update(n=>n+1);
      }
      // A change restored after a reload reopens where it was made, to say how it ended.
      if(active&&(manager.view.draft||manager.view.saved||manager.view.change||manager.view.unresolved||manager.view.error==="receipt_expired"))setOpen(true);
    });
    const hidden=()=>{if(document.visibilityState==="hidden")void manager.cancelTest()};
    const pagehide=()=>{void manager.cancelTest()};
    document.addEventListener("visibilitychange",hidden);window.addEventListener("pagehide",pagehide);
    return()=>{active=false;document.removeEventListener("visibilitychange",hidden);window.removeEventListener("pagehide",pagehide);manager.dispose()};
  },[manager]);
  const reset=recovery.current;
  const {view}=manager,cameras=viewer.catalog.cameras,current=viewer.viewer;
  // The saved camera the settings drawer shows.
  const settingsID=open?view.credentialTarget?.id??(view.draft?.credential_update?view.draft.candidate_id:undefined)??(view.saved?.credentials_updated?view.saved.camera_id:undefined):undefined;
  const camera=cameras.find(item=>item.id===settingsID);
  // Only the manager who added a camera reads and changes its settings.
  const editable=Boolean(camera?.can_update_credentials);
  // The camera on screen reads this page's state, as its rail row does; any
  // other reads the server's.
  const shown=Boolean(current&&settingsID&&current.selection.camera_id===settingsID&&current.presentation.state!=="unknown");
  const watching=shown&&["connecting","playing","reconnecting"].includes(current?.presentation.state??"");
  // The viewer's actions are new on every render, so only whether Stop exists
  // is a dependency; the latest one runs.
  const canStop=Boolean(current?.presentation.actions?.onStop);
  // The first of the camera's streams whose source a reset would restore.
  const needs=camera?.streams.find(item=>item.can_reconnect&&(item.recovery_required||reset?.unresolved&&reset.ref?.camera_id===camera.id&&reset.ref.stream_id===item.id));
  useEffect(()=>{if(settingsID&&editable)void manager.loadSettings(settingsID)},[settingsID,editable]);
  // The server refuses a new address or sign-in, and a removal, while anyone
  // watches, so the drawer keeps the count current. Hidden pages skip the request.
  useEffect(()=>{
    if(!settingsID||view.saved||!editable)return;
    let active=true,timer:ReturnType<typeof setTimeout>|undefined;
    const poll=async()=>{
      if(document.visibilityState!=="hidden"){
        let count:number|undefined;
        try{count=await client.viewers(settingsID)}catch{}
        if(!active)return;
        setViewers(last=>last?.id===settingsID&&last.count===count?last:{id:settingsID,count});
      }
      timer=setTimeout(poll,VIEWERS_MS);
    };
    void poll();
    return()=>{active=false;clearTimeout(timer)};
  },[settingsID,Boolean(view.saved),editable,Boolean(view.draft?.fenced),ask]);
  const openSettings=(id:string)=>{
    const name=latest.current.viewer.catalog.cameras.find(camera=>camera.id===id)?.name;
    setOpen(true);
    void manager.openCredentials({id,name});
  };
  // A Reconnect confirmation closes once the reset is known to have happened.
  const settle=(work:Promise<void>)=>{void work.then(()=>{if(reset&&!reset.unresolved&&!reset.error)setRecovering(undefined)})};
  useEffect(()=>{onChange({watchSelection,
    onAddCamera:manager.capabilities?.can_manage_cameras?next=>{manager.reset();setMode(next??"manual");setOpen(true)}:undefined,
    onCameraSettings:manager.capabilities?.can_manage_cameras?openSettings:undefined,
    onReconnect:reset?setRecovering:undefined,
    recoveryPending:reset?.unresolved?reset.ref:undefined,
    management:open?{view,actions:{
      submit:input=>{void manager.submit(input)},importFiles:(yaml,env)=>{void manager.importFiles(yaml,env)},test:video=>{void manager.test(video)},
      submitCredentials:credentials=>{void manager.submitCredentials(credentials)},checkAndSave:(video,credentials,endpoint,stop)=>{void manager.checkAndSave(video,credentials,endpoint,stop)},openSettings,
      rename:name=>{void manager.rename(name)},remove:stop=>{void manager.remove(stop)},
      retrySave:()=>{void manager.retrySave()},abandon:()=>manager.abandon(),reset:()=>manager.reset(),cancelTest:()=>{void manager.cancelTest()},save:()=>{void manager.save()},recover:()=>{void manager.recover()},
      cancel:()=>{void manager.cancel().then(()=>{if(!manager.view.unresolved)setOpen(false)})},
      close:()=>{setOpen(false);void manager.close().catch(()=>{})},
      watch:id=>{void manager.watch().then(()=>{
        const current=latest.current.viewer.viewer?.selection;
        if(manager.view.saved?.credentials_updated&&current?.camera_id===id&&current.stream_id==="default") {
          void latest.current.recovered().catch(()=>{});
        } else setWatchSelection({camera_id:id,stream_id:"default"});
        setOpen(false);void manager.close().catch(()=>{});
      }).catch(()=>{})},
    },mode,settings:{camera,viewers:viewers?.id===settingsID?viewers?.count:undefined,watching,state:shown?current?.presentation.state:camera?.availability,
      stopWatching:watching&&canStop?()=>{void Promise.resolve(latest.current.viewer.viewer?.presentation.actions?.onStop?.()).catch(()=>{}).finally(()=>askAgain(n=>n+1))}:undefined,
      reconnect:reset&&camera&&needs?()=>setRecovering({camera_id:camera.id,stream_id:needs.id}):undefined}}:undefined,
  });},[version,open,mode,watchSelection,viewers,camera,watching,shown&&current?.presentation.state,canStop,needs?.id]);
  const target=recovering&&cameras.find(item=>item.id===recovering.camera_id);
  return recovering&&reset?<CameraRecovery name={target?.name??"this camera"} busy={reset.busy} unresolved={reset.unresolved} error={reset.error} disabled={!view.storageReady}
    onConfirm={()=>settle(reset.confirm(recovering))} onCheck={()=>settle(reset.check())} onClose={()=>setRecovering(undefined)}/>:null;
}
