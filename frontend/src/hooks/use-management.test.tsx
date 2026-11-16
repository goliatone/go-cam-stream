import {render,screen,fireEvent,waitFor,cleanup} from '@testing-library/preact';
import {afterEach,expect,it,vi} from 'vitest';
import {useState} from 'preact/hooks';
import type {AppProps} from '../app';
import ManagementHost from './use-management';
const api=vi.hoisted(()=>({capabilities:vi.fn(async()=>({can_manage_cameras:false,recovery_scope:'scope',runtime_epoch:'epoch'})),reconnect:vi.fn(async()=>({affected:[],closed_sessions:[]}))}));
vi.mock('../services/management-api',async original=>({...await original<object>(),ManagementClient:class{capabilities=api.capabilities;reconnect=api.reconnect}}));
afterEach(()=>{cleanup();sessionStorage.clear();vi.clearAllMocks()});
it('binds reconnect on an initially faulted stream when management is disabled',async()=>{
 const ref={camera_id:'saved',stream_id:'default'},refresh=vi.fn(async()=>({runtime_epoch:'epoch',generation:1})),recovered=vi.fn(async()=>{});
 const viewer={catalog:{cameras:[{id:'saved',name:'Saved',availability:'failed',streams:[{id:'default',name:'Default',capabilities:{video:true,audio:false},can_reconnect:true,recovery_required:true}]}]},viewer:{selection:ref,presentation:{state:"failed"}}} as AppProps;
 function Bound(){const[props,set]=useState<Partial<AppProps>>({});return <><ManagementHost viewer={viewer} refresh={refresh} recovered={recovered} onChange={set}/>{props.recoveryControl}</>}
 render(<Bound/>);await waitFor(()=>expect(api.capabilities).toHaveBeenCalled());
 fireEvent.click(await screen.findByRole('button',{name:'Reconnect Camera'}));
 await waitFor(()=>expect(screen.getAllByRole('button',{name:'Reconnect Camera'})).toHaveLength(2));
 fireEvent.click(screen.getAllByRole('button',{name:'Reconnect Camera'})[1]!);
 await waitFor(()=>expect(api.reconnect).toHaveBeenCalledOnce());expect(api.reconnect).toHaveBeenCalledWith(ref,expect.objectContaining({runtime_epoch:'epoch'}));expect(recovered).toHaveBeenCalledOnce();
});
