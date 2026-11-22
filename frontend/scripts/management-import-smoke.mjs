// Exact exported-file import checks; never starts camera capture or logs payloads.
import {createRequire} from 'node:module';
const {chromium}=createRequire(import.meta.url)(process.env.PLAYWRIGHT_MODULE||'playwright');
const origin=process.env.SMOKE_ORIGIN,yaml=process.env.SMOKE_YAML,env=process.env.SMOKE_ENV;
if(!origin||!yaml||!env)throw Error('Set isolated SMOKE_ORIGIN and exact SMOKE_YAML/SMOKE_ENV files');
const browser=await chromium.launch();
try{
 const page=await browser.newPage({ignoreHTTPSErrors:true});await page.goto(origin);
 const catalog=await page.evaluate(()=>fetch('/api/cameras').then(r=>r.json()));
 if(!catalog.cameras.length)throw Error('Expected persisted camera catalog');
 const click=name=>page.getByRole('button',{name,exact:true}).click();
 const open=async()=>{await click('Add Camera');await page.getByRole('radio',{name:'Import Mac files',exact:true}).check();await page.getByLabel('Connection YAML').setInputFiles(yaml)};
 // Intercept the response, not the upload: request replay omits Chromium's files.
 const cdp=await page.context().newCDPSession(page);let responses=0,status;
 await cdp.send('Fetch.enable',{patterns:[{urlPattern:'*/api/camera-drafts/import',requestStage:'Response'}]});
 cdp.on('Fetch.requestPaused',async event=>{responses++;status=event.responseStatusCode;await cdp.send('Fetch.failRequest',{requestId:event.requestId,errorReason:'Aborted'})});
 await open();await page.getByLabel('Credentials file (optional)').setInputFiles(env);await click('Import and review');
 await page.getByRole('button',{name:'Edit details',exact:true}).waitFor();
 if(responses!==1||status!==200)throw Error('Lost import did not recover one successful request');
 if(!await page.getByText('Provided (hidden)',{exact:true}).count())throw Error('Imported credentials missing');
 await click('Cancel draft');await page.getByRole('dialog').waitFor({state:'detached'});await cdp.send('Fetch.disable');
 await open();await click('Import and review');await page.getByRole('button',{name:'Edit details',exact:true}).waitFor();
 if(await page.getByRole('button',{name:'Test connection',exact:true}).isEnabled())throw Error('YAML-only import allowed test without credentials');
 await click('Edit details');if(!await page.getByLabel('Password',{exact:true}).isVisible())throw Error('Missing credential entry');
 await click('Cancel draft');await page.getByRole('dialog').waitFor({state:'detached'});
 const stored=await page.evaluate(()=>Object.values(sessionStorage).join(''));if(stored.includes('rtsp:')||stored.includes('MAC_CAMERA_'))throw Error('Import contents persisted');
 console.log(JSON.stringify({persisted_catalog_count:catalog.cameras.length,lost_import_posts:responses,exact_import_recovered:true,yaml_only_requires_credentials:true,cancel_cleanup:true}));
}finally{await browser.close()}
