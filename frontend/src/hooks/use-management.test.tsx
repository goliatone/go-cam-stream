import {render,screen,fireEvent,waitFor,cleanup} from '@testing-library/preact';
import {afterEach,expect,it,vi} from 'vitest';
import {useState} from 'preact/hooks';
import type {AppProps} from '../app';
import type {State} from '../services/contracts';
import CameraManagement from '../components/camera-management';
import ManagementHost from './use-management';
const api=vi.hoisted(()=>({capabilities:vi.fn(async()=>({can_manage_cameras:false,recovery_scope:'scope',runtime_epoch:'epoch'})),reconnect:vi.fn(async()=>({affected:[],closed_sessions:[]})),viewers:vi.fn(async(_id:string):Promise<number|undefined>=>undefined)}));
vi.mock('../services/management-api',async original=>({...await original<object>(),ManagementClient:class{capabilities=api.capabilities;reconnect=api.reconnect;viewers=api.viewers}}));
afterEach(()=>{cleanup();sessionStorage.clear();vi.clearAllMocks()});
const ref={camera_id:'saved',stream_id:'default'};
const refresh=vi.fn(async()=>({runtime_epoch:'epoch',generation:1}));
it('binds reconnect on an initially faulted stream when management is disabled',async()=>{
 const recovered=vi.fn(async()=>{});
 const viewer={catalog:{cameras:[{id:'saved',name:'Saved',availability:'failed',streams:[{id:'default',name:'Default',capabilities:{video:true,audio:false},can_reconnect:true,recovery_required:true}]}]},viewer:{selection:ref,presentation:{state:"failed"}}} as AppProps;
 // The page asks; the host shows its own confirmation.
 function Bound(){const[props,set]=useState<Partial<AppProps>>({});return <><ManagementHost viewer={viewer} refresh={refresh} recovered={recovered} onChange={set}/>{props.onReconnect&&<button onClick={()=>props.onReconnect?.(ref)}>Reconnect</button>}</>}
 render(<Bound/>);await waitFor(()=>expect(api.capabilities).toHaveBeenCalled());
 fireEvent.click(await screen.findByRole('button',{name:'Reconnect'}));
 expect(await screen.findByRole('dialog',{name:'Reconnect Saved?'})).toBeTruthy();expect(api.reconnect).not.toHaveBeenCalled();
 fireEvent.click(screen.getByRole('button',{name:'Reconnect camera'}));
 await waitFor(()=>expect(api.reconnect).toHaveBeenCalledOnce());expect(api.reconnect).toHaveBeenCalledWith(ref,expect.objectContaining({runtime_epoch:'epoch'}));expect(recovered).toHaveBeenCalledOnce();
 // Once the reset is known to have happened, the confirmation closes.
 await waitFor(()=>expect(screen.queryByRole('dialog')).toBeNull());
});
it('counts who watches the settings camera, and asks again once this page stops',async()=>{
 api.capabilities.mockResolvedValueOnce({can_manage_cameras:true,recovery_scope:'scope',runtime_epoch:'epoch'});
 api.viewers.mockResolvedValueOnce(2).mockResolvedValue(0);
 const stop=vi.fn(),catalog={cameras:[{id:'saved',name:'Saved',availability:'playing' as const,can_update_credentials:true,streams:[{id:'default',name:'Default',capabilities:{video:true,audio:false}}]}]};
 function Bound(){
  const[props,set]=useState<Partial<AppProps>>({}),[state,setState]=useState<State>('playing');
  const viewer={catalog,viewer:{selection:ref,presentation:{state,actions:{onStop:()=>{stop();setState('closed')}}}}} as AppProps;
  return <><ManagementHost viewer={viewer} refresh={refresh} recovered={vi.fn(async()=>{})} onChange={set}/>
   {props.onCameraSettings&&<button onClick={()=>props.onCameraSettings?.('saved')}>Settings</button>}
   {props.management&&<CameraManagement {...props.management}/>}</>;
 }
 render(<Bound/>);fireEvent.click(await screen.findByRole('button',{name:'Settings'}));
 await screen.findByRole('dialog',{name:'Settings Saved'});
 await waitFor(()=>expect(document.getElementById('settings-watchers')!.textContent).toBe('2 people are watching this camera, including you. Everyone must stop watching before its sign-in can change.'));
 expect(api.viewers).toHaveBeenCalledWith('saved');expect((screen.getByRole('button',{name:'Check and save'}) as HTMLButtonElement).disabled).toBe(true);
 fireEvent.click(screen.getByRole('button',{name:'Stop watching here'}));expect(stop).toHaveBeenCalledOnce();
 // The count is asked for again at once rather than at the next poll.
 await waitFor(()=>expect(document.getElementById('settings-watchers')!.textContent).toBe('No one is watching this camera, so its sign-in can change now.'));
 expect(api.viewers).toHaveBeenCalledTimes(2);expect((screen.getByRole('button',{name:'Check and save'}) as HTMLButtonElement).disabled).toBe(false);
 expect(document.querySelector('.drawer-facts dd')!.textContent).toBe('Stopped');
});
