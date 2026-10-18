const fs=require('node:fs');
const assert=require('node:assert/strict');
const {chromium}=require('/Users/ben/.codex/worktrees/t442b-key-copy/phebs.com/ui/node_modules/playwright');
const out=process.env.B_OUTPUT, base=process.env.B_BASE, live=process.env.B_LIVE;
const token='phebs_receipt.synthetic-key-for-presentation-only';
const result={schema:'phebs-t442b-assessment-b-browser-v1',outcome:'started',contexts:[],browser_visibility:'headless fallback; no user-visible Human tab'};
let browser;
async function waitFor(fn,message){const end=Date.now()+5000;while(Date.now()<end){if(await fn())return;await new Promise(r=>setTimeout(r,50));}throw Error(message);}
async function prep(theme,width,preview){
 const context=await browser.newContext({viewport:{width,height:width===390?900:800},colorScheme:theme,reducedMotion:'reduce'});
 const page=await context.newPage();page.setDefaultTimeout(5000);
 const row={theme,width,preview,requests:[],unexpected:[],console:[],pageerrors:[],clipboardWrites:[],mutable_preflight:false};
 page.on('pageerror',e=>row.pageerrors.push(e.message));
 page.on('console',async m=>{if(/impeccable/i.test(m.text())){const args=await Promise.all(m.args().map(a=>a.jsonValue().catch(()=>null)));row.console.push({type:m.type(),text:m.text(),args});}});
 await context.addInitScript(({theme})=>{localStorage.setItem('phebs-theme',theme);localStorage.setItem('phebs-density','comfortable');localStorage.setItem('phebs-palette','phebs');},{theme});
 await page.route('**/api/**',async route=>{
  const req=route.request(),u=new URL(req.url()),key=req.method()+' '+u.pathname+u.search;row.requests.push(key);let value,status=200;
  if(key==='GET /api/auth/status') value={authenticated:true,auth_required:true,setup_required:false,oidc_enabled:false,password_enabled:true,csrf_token:'appearance-receipt-csrf',user:{id:'appearance-reader',email:'appearance@localhost.test',display_name:'Receipt reader',is_admin:false}};
  else if(key==='GET /api/version') value={version:'receipt',capabilities:[]};
  else if(key==='GET /api/auth/keys') value={keys:[]};
  else if(!preview&&key==='POST /api/auth/keys'&&req.postDataJSON().name==='Receipt client') value={key:{id:'receipt-key',name:'Receipt client',prefix:'phebs_receipt',capabilities:[],created_at:'2026-08-07T20:00:00.000Z'},token};
  else {row.unexpected.push(key);status=501;value={error:'unexpected assessment-b fixture request'};}
  await route.fulfill({status,contentType:'application/json',body:JSON.stringify(value)});
 });
 if(preview)await page.route('**/assets/highlight-*.js',r=>r.fulfill({status:503,contentType:'application/javascript',body:'/* deliberate presentation-only module failure */'}));
 await page.goto(base+'/#/settings');
 await page.getByRole('heading',{name:'Appearance',exact:true}).waitFor();
 if(preview)await page.getByText('Preview unavailable.',{exact:true}).waitFor();
 else await page.locator('[data-palette-ready="true"]').waitFor();
 await page.evaluate(()=>document.fonts.ready);
 return {context,page,row};
}
async function keyState(theme,width,fail){
 const f=await prep(theme,width,false),{page,row}=f;
 await page.getByRole('textbox',{name:'Key name',exact:true}).fill('Receipt client');
 await page.getByRole('button',{name:'Create key',exact:true}).click();
 const card=page.getByRole('group',{name:'New API key',exact:true}),input=card.getByRole('textbox',{name:'New API key token',exact:true}),copy=card.getByRole('button',{name:'Copy API key',exact:true});
 await input.waitFor();assert.equal(await input.inputValue(),token);assert.equal(await input.getAttribute('readonly'),'');
 await input.focus();await page.keyboard.press('ControlOrMeta+a');
 row.full_readonly_selection=await input.evaluate(n=>[n.selectionStart,n.selectionEnd]);assert.deepEqual(row.full_readonly_selection,[0,token.length]);
 await page.evaluate(fail=>Object.defineProperty(navigator,'clipboard',{configurable:true,value:fail?undefined:{writeText:async t=>{window.__bClipboardText=t;}}}),fail);
 await page.keyboard.press('Tab');assert.equal(await copy.evaluate(n=>n===document.activeElement),true);await page.keyboard.press('Enter');
 await waitFor(async()=>await card.getByRole('status').innerText()===(fail?'Copy failed. Select and copy the key above.':'API key copied.'),'copy feedback did not settle');
 row.status=await card.getByRole('status').innerText();
 row.copy_enabled=await copy.isEnabled();assert.equal(row.copy_enabled,true);
 if(!fail){row.clipboardText=await page.evaluate(()=>window.__bClipboardText);assert.equal(row.clipboardText,token);}
 await capture(f,card,fail?'key-copy-failure-light-390':'key-copy-success-dark-1280');
 if(fail){
  await input.focus();await page.keyboard.press('ControlOrMeta+a');assert.deepEqual(await input.evaluate(n=>[n.selectionStart,n.selectionEnd]),[0,token.length]);
  await page.evaluate(()=>Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async t=>{window.__bClipboardText=t;}}}));
  await page.keyboard.press('Tab');assert.equal(await copy.evaluate(n=>n===document.activeElement),true);await page.keyboard.press('Enter');
  await waitFor(async()=>await card.getByRole('status').innerText()==='API key copied.','keyboard retry did not settle');
  assert.equal(await page.evaluate(()=>window.__bClipboardText),token);row.keyboard_failure_to_success=true;row.retry_status=await card.getByRole('status').innerText();
 }
 assert.equal(row.requests.filter(x=>x==='POST /api/auth/keys').length,1);assert.deepEqual(row.unexpected,[]);assert.deepEqual(row.pageerrors,[]);
 result.contexts.push(row);await f.context.close();
}
async function capture({page,row},target,name){
 await target.evaluate(n=>{const clearance=document.querySelector('header').getBoundingClientRect().height+12;n.scrollIntoView({block:'start',inline:'nearest'});window.scrollBy(0,-clearance);const shortfall=n.getBoundingClientRect().top-clearance;if(shortfall<0)window.scrollBy(0,Math.floor(shortfall));});
 row.bounds=await target.evaluate(n=>{const b=n.getBoundingClientRect();return {top:b.top,bottom:b.bottom,left:b.left,right:b.right,height:innerHeight,width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth};});
 assert.equal(row.bounds.overflow,false);assert(row.bounds.top>=0&&row.bounds.bottom<=row.bounds.height&&row.bounds.left>=0&&row.bounds.right<=row.bounds.width);
 await page.evaluate(()=>{document.title='[Human] Impeccable Assessment B';});
 await page.addScriptTag({content:'window.__bMutablePreflight = true;'});row.mutable_preflight=await page.evaluate(()=>window.__bMutablePreflight===true&&document.title.startsWith('[Human]'));
 assert.equal(row.mutable_preflight,true);
 await page.addScriptTag({url:live+'/detect.js'});row.injection_succeeded=true;
 await page.waitForTimeout(2700);
 row.overlay_dom=await page.evaluate(()=>Array.from(document.querySelectorAll('*')).filter(n=>/impeccable/i.test(n.tagName+' '+n.id+' '+n.className)).slice(-30).map(n=>({tag:n.tagName,id:n.id,class:String(n.className)})));
 row.name=name;row.screenshot=out+'/'+name+'.png';await page.screenshot({path:row.screenshot,fullPage:false});
}
(async()=>{try{
 browser=await chromium.connectOverCDP(process.env.B_CDP);result.browser_version=browser.version();
 await keyState('light',390,true);await keyState('dark',1280,false);
 const f=await prep('light',1280,true),{page,row}=f;const appearance=page.getByRole('region',{name:'Appearance',exact:true});
 const reminder=appearance.getByText('Finish Settings changes and copy any new API key before reloading.',{exact:true});await reminder.waitFor();
 row.reminder=await reminder.innerText();row.reload_height=(await appearance.getByRole('button',{name:'Reload page',exact:true}).boundingBox()).height;assert(row.reload_height>=44);
 await capture(f,appearance,'preview-reminder-light-1280');assert.deepEqual(row.unexpected,[]);assert.deepEqual(row.pageerrors,[]);result.contexts.push(row);await f.context.close();result.outcome='passed';
 }catch(e){result.outcome='stopped';result.error=e.stack;process.exitCode=1;}finally{if(browser)await browser.close();fs.writeFileSync(out+'/browser-result.json',JSON.stringify(result,null,2)+'\n');console.log(JSON.stringify({outcome:result.outcome,contexts:result.contexts.length,error:result.error}));}})();
