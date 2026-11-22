// Runs only against the isolated controlled-media browser fixture.
import {createRequire} from 'node:module';import{execFileSync}from'node:child_process';
const{chromium}=createRequire(import.meta.url)(process.env.PLAYWRIGHT_MODULE||'playwright');
const origin=process.env.SMOKE_ORIGIN,container=process.env.SMOKE_ORIGIN_CONTAINER;
if(!origin||!container)throw Error('Explicit fixture origin/container required');
const patch=source=>execFileSync('docker',['exec',container,'curl','--fail','--silent','--get','-X','PATCH','--data-urlencode','name=t05-main-private','--data-urlencode',`src=${source}`,'http://127.0.0.1:34000/api/streams'],{stdio:'pipe'});
const restored='rtsp://127.0.0.1:34003/t05-other-private';
const browser=await chromium.launch();let resets=0;
try{
 const context=await browser.newContext({ignoreHTTPSErrors:true}),page=await context.newPage(),other=await context.newPage();
 await other.goto(origin);await other.waitForFunction(()=>{let v=document.querySelector('main video');return v&&v.videoWidth>0&&!v.paused});
 const caps=await other.evaluate(()=>fetch('/api/capabilities').then(r=>r.json()));if(caps.can_manage_cameras)throw Error('Management remained enabled');
 const catalog=await other.evaluate(()=>fetch('/api/cameras').then(r=>r.json()));const camera=catalog.cameras.find(c=>c.name==='Persistent onboarding');if(!camera)throw Error('Saved camera did not survive restart');
 const frame=await other.locator('main video').evaluate(v=>{window.smokeVideo=v;return v.getVideoPlaybackQuality().totalVideoFrames});
 patch('rtsp://127.0.0.1:34003/t05-missing-private');await page.goto(`${origin}/?camera=${camera.id}`);const reconnect=page.getByRole('region',{name:'Camera details'}).getByRole('button',{name:'Reconnect',exact:true});await reconnect.waitFor({timeout:30000});
 if(await page.getByRole('button',{name:'Add Camera',exact:true}).count())throw Error('Management UI remained enabled');
 patch(restored);let drop=true;await page.route('**/recovery',async route=>{resets++;if(drop){drop=false;await route.fetch();await route.abort('failed')}else await route.continue()});
 await reconnect.click();await page.getByRole('dialog').getByRole('button',{name:'Reconnect camera',exact:true}).click();
 await page.waitForFunction(()=>{const v=document.querySelector('main video');return v&&v.videoWidth===640&&!v.paused&&v.getVideoPlaybackQuality().totalVideoFrames>=2},{},{timeout:30000});
 if(resets!==1)throw Error('Lost recovery reply repeated reset');
 if(!await other.locator('main video').evaluate((v,n)=>v===window.smokeVideo&&v.getVideoPlaybackQuality().totalVideoFrames>n,frame))throw Error('Unrelated source interrupted');
 const deniedContext=await browser.newContext({ignoreHTTPSErrors:true,extraHTTPHeaders:{'X-Test-Actor':'viewer'}}),denied=await deniedContext.newPage();await denied.goto(origin);
 const result=await denied.evaluate(async id=>{const cameras=await(await fetch('/api/cameras')).json(),caps=await(await fetch('/api/capabilities')).json(),csrf=await(await fetch('/api/csrf')).json();const response=await fetch(`/api/cameras/${id}/streams/default/recovery`,{method:'POST',headers:{'Content-Type':'application/json',[csrf.header_name]:csrf.token},body:JSON.stringify({operation_key:'e'.repeat(32),runtime_epoch:caps.runtime_epoch})});return{visible:cameras.cameras.some(c=>c.id===id),status:response.status,code:(await response.json()).error?.code}},camera.id);
 if(result.visible||![403,404].includes(result.status))throw Error(`View-only boundary: ${JSON.stringify(result)}`);
 console.log(JSON.stringify({restart_restored:true,management_disabled:true,reconnect_decoded:true,lost_reset_posts:resets,unrelated_playback_preserved:true,unauthorized_saved_camera_excluded:true,unauthorized_reset_status:result.status}));
}finally{patch(restored);await browser.close()}
