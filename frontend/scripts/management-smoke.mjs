// Explicit local integration driver. No camera capture, screenshots, raw payload
// logging or secret persistence. Supply an isolated host and unsaved RTSP source.
import { createRequire } from 'node:module';
const require=createRequire(import.meta.url);
const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const origin=process.env.SMOKE_ORIGIN,endpoint=process.env.SMOKE_ENDPOINT;
if(!origin||!endpoint)throw Error('Set SMOKE_ORIGIN and SMOKE_ENDPOINT');
const browser=await chromium.launch({headless:true});
const context=await browser.newContext({ignoreHTTPSErrors:true});
const page=await context.newPage(), observer=await context.newPage();
const errors=[];let dropped, saved, completes=0,saves=0;
page.on('pageerror',error=>errors.push(error.message));
let drop;
await page.route('**/api/**',async route=>{
 const request=route.request(),pathname=new URL(request.url()).pathname;
 if(request.method()==='POST'&&pathname.endsWith('/complete')){
  const evidence=request.postDataJSON().evidence;
  if(evidence.frames<2||evidence.width<1||evidence.paused)throw Error('Missing decoded evidence');
  completes++;
 }
 if(request.method()==='POST'&&pathname==='/api/cameras')saves++;
 if(drop&&drop.method===request.method()&&drop.path.test(pathname)){
  const settle=drop.resolve;drop=undefined;const response=await route.fetch();
  if(response.status()!==200)throw Error('Mutation failed before response-loss injection');
  if(pathname==='/api/cameras')saved=await response.json();
  await route.abort('failed');settle();return;
 }
 await route.continue();
});
function lose(method,path){dropped=new Promise(resolve=>{drop={method,path,resolve}})}
async function click(name){await page.getByRole('button',{name,exact:true}).click()}
async function ready(name){await page.getByRole('button',{name,exact:true}).waitFor();await page.waitForFunction(label=>[...document.querySelectorAll('button')].some(b=>b.textContent===label&&!b.disabled),name,{timeout:30000})}
async function draft(name){await click('Add Camera');await page.getByLabel('Camera name',{exact:true}).fill(name);await page.getByLabel('RTSP address',{exact:true}).fill(endpoint);if(process.env.SMOKE_USERNAME){await page.getByRole('combobox',{name:'Authentication',exact:true}).selectOption('credentials');await page.getByLabel('Username',{exact:true}).fill(process.env.SMOKE_USERNAME);await page.getByLabel('Password',{exact:true}).fill(process.env.SMOKE_PASSWORD)}await click('Review camera');await ready('Test connection')}
async function live(p){await p.waitForFunction(()=>{let v=document.querySelector('main video');return v&&v.videoWidth>0&&!v.paused&&v.getVideoPlaybackQuality().totalVideoFrames>=2},{},{timeout:30000})}
try{
 for(const p of[page,observer]){await p.goto(origin);await live(p)}
 lose('POST',/^\/api\/camera-drafts$/);await draft('Loss recovery');await dropped;
 lose('PATCH',/^\/api\/camera-drafts\/[^/]+$/);await click('Edit details');await page.getByLabel('Camera name',{exact:true}).fill('Recovered edit');await click('Apply changes');await dropped;await ready('Test connection');
 lose('POST',/\/tests$/);await click('Test connection');await dropped;await ready('Cancel test');
 lose('DELETE',/\/tests\/[^/]+$/);await click('Cancel test');await dropped;await ready('Test connection');
 lose('POST',/\/complete$/);await click('Test connection');await dropped;await ready('Save Camera');
 lose('DELETE',/^\/api\/camera-drafts\/[^/]+$/);await click('Cancel draft');await dropped;await page.getByRole('dialog').waitFor({state:'detached'});
 console.log('LOST_CREATE_PATCH_TEST_COMPLETE_CANCEL_RECOVERED');
 await draft('Persistent onboarding');await click('Test connection');await ready('Save Camera');
 const before=await observer.locator('main video').evaluate(v=>{window.smokeVideo=v;return v.getVideoPlaybackQuality().totalVideoFrames});
 lose('POST',/^\/api\/cameras$/);const started=Date.now();await click('Save Camera');await dropped;
 await observer.waitForFunction(id=>[...document.querySelectorAll('a')].some(a=>new URL(a.href).searchParams.get('camera')===id),saved.camera_id,{timeout:5000});
 const refreshMs=Date.now()-started;if(refreshMs>5000)throw Error('Catalog propagation exceeded five seconds');
 await page.reload();await ready('Watch Camera');if(saves!==1)throw Error('Save replayed after reload');
 if(!await observer.locator('main video').evaluate((v,n)=>v===window.smokeVideo&&v.getVideoPlaybackQuality().totalVideoFrames>n,before))throw Error('Existing playback changed');
 const stored=await page.evaluate(()=>Object.values(sessionStorage).join(''));
 if(stored.includes('rtsp:')||stored.includes('Persistent onboarding')||(process.env.SMOKE_PASSWORD&&stored.includes(process.env.SMOKE_PASSWORD)))throw Error('Private data persisted');
 console.log(JSON.stringify({lost_save_reload:true,saves,decoded_completions:completes,refresh_ms:refreshMs,existing_video_preserved:true}));
 await click('Watch Camera');await live(page);console.log('WATCH_DECODED_PASS');
 const mobile=await context.newPage({viewport:{width:375,height:812}}).catch(()=>null);
 // Context viewport is set per page for a real narrow layout.
 if(mobile){await mobile.setViewportSize({width:375,height:812});await mobile.goto(origin);await mobile.getByRole('button',{name:'Add Camera',exact:true}).click();await mobile.getByLabel('Camera name',{exact:true}).waitFor();if(!await mobile.evaluate(()=>document.documentElement.scrollWidth<=innerWidth))throw Error('Mobile horizontal overflow');await mobile.keyboard.press('Escape');await mobile.getByRole('dialog').waitFor({state:'detached'});await mobile.close()}
 const blocked=await browser.newContext({ignoreHTTPSErrors:true});await blocked.addInitScript(()=>Object.defineProperty(window,'sessionStorage',{get(){throw new DOMException('blocked','SecurityError')}}));const denied=await blocked.newPage();await denied.goto(origin);await denied.getByRole('button',{name:'Add Camera',exact:true}).click();await denied.getByRole('alert').filter({hasText:'storage'}).waitFor();if(await denied.getByRole('button',{name:'Review camera',exact:true}).isEnabled())throw Error('Blocked storage allowed mutation');await blocked.close();
 if(errors.length)throw Error('Browser JavaScript errors');console.log('MOBILE_KEYBOARD_STORAGE_PASS');
}finally{await browser.close()}
