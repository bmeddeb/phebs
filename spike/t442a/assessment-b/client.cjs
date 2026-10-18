const fs = require('node:fs');
const { chromium } = require('/Users/ben/.codex/worktrees/t442a-preview-recovery/phebs.com/ui/node_modules/playwright');
const [inputFile] = process.argv.slice(2);
const input = JSON.parse(fs.readFileSync(inputFile, 'utf8'));
const result = {method:'cached Playwright; installed Darwin Chrome; headless only', visible_overlay:false, views:[]};
const pause = ms => new Promise(resolve => setTimeout(resolve,ms));
let browser;
(async()=>{
 browser=await chromium.connectOverCDP(input.cdp);
 for(const [index,view] of [{theme:'light',width:1280,state:'ready'},{theme:'dark',width:1280,state:'unavailable'},{theme:'light',width:390,state:'unavailable'}].entries()){
  const context=await browser.newContext({viewport:{width:view.width,height:view.width===390?900:800},colorScheme:view.theme,reducedMotion:'reduce'});
  const page=await context.newPage();const row={...view,console:[],pageerrors:[],unexpected:[],faults:[]};result.views.push(row);
  try{
   page.on('console',m=>row.console.push({type:m.type(),text:m.text(),url:m.location().url}));page.on('pageerror',e=>row.pageerrors.push(e.message));
   await page.route('**/api/**',async route=>{
    const request=route.request(); const key=request.method()+' '+new URL(request.url()).pathname;let value;
    if(key==='GET /api/auth/status')value={authenticated:true,auth_required:true,setup_required:false,oidc_enabled:false,password_enabled:true,csrf_token:'appearance-receipt-csrf',user:{id:'appearance-reader',email:'appearance@localhost.test',display_name:'Receipt reader',is_admin:false}};
    else if(key==='GET /api/version')value={version:'receipt',capabilities:[]};else if(key==='GET /api/auth/keys')value={keys:[]};else{row.unexpected.push(key);value={error:'unexpected Appearance fixture request'}};
    await route.fulfill({status:row.unexpected.includes(key)?501:200,contentType:'application/json',body:JSON.stringify(value)});
   });
   if(view.state==='unavailable')await page.route('**/assets/highlight-*.js',async route=>{row.faults.push(route.request().url());await route.fulfill({status:503,contentType:'application/javascript',body:'/* deliberate critique-only module failure */'});});
   await page.addInitScript(theme=>{localStorage.setItem('phebs-theme',theme);localStorage.setItem('phebs-density','comfortable');localStorage.setItem('phebs-palette','phebs')},view.theme);
   await page.goto(input.base+'/#/settings');
   const appearance=page.getByRole('region',{name:'Appearance',exact:true});await appearance.waitFor({state:'visible'});
   if(view.state==='unavailable')await appearance.getByRole('status').waitFor({state:'visible'});else await appearance.locator('pre[data-palette-ready=true]').waitFor();
   await page.getByText('No API keys.',{exact:true}).waitFor();
   row.preflight=await page.evaluate(()=>{document.title='[Human] Impeccable · Appearance';const s=document.createElement('script');s.textContent='window.__impeccableMutablePreflight = true';document.head.append(s);return {title:document.title,scriptRan:window.__impeccableMutablePreflight===true}});
   if(!row.preflight.scriptRan)throw Error('mutable title/script preflight unavailable');
   if(index===0){fs.writeFileSync(input.out+'/mutable-preflight.json',JSON.stringify(row.preflight));while(!fs.existsSync(input.out+'/live-url.json'))await pause(50);input.live=JSON.parse(fs.readFileSync(input.out+'/live-url.json')).url;}
   await page.evaluate(()=>scrollTo(0,0));
   await page.addScriptTag({url:input.live+'/detect.js'});row.injection=true;await pause(2600);
   row.impeccableConsole=row.console.filter(m=>/impeccable/i.test(m.text));
   row.domEvidence=await page.evaluate(()=>({title:document.title,scriptSources:[...document.scripts].map(s=>s.src).filter(s=>/detect\.js/.test(s)),overlayElements:[...document.querySelectorAll('*')].filter(e=>/impeccable/i.test(e.id+' '+(typeof e.className==='string'?e.className:''))).slice(0,30).map(e=>({tag:e.tagName,id:e.id,className:typeof e.className==='string'?e.className:''})),appearanceText:document.querySelector('[aria-labelledby=appearance-heading]').innerText,documentWidth:document.documentElement.scrollWidth,viewportWidth:innerWidth}));
   row.controls=await appearance.evaluate(el=>[...el.querySelectorAll('select,button')].map(e=>{const r=e.getBoundingClientRect(),s=getComputedStyle(e);return {tag:e.tagName,text:e.tagName==='SELECT'?e.value:e.textContent,width:r.width,height:r.height,font:s.fontSize}}));
   const shot=input.out+`/overlay-${view.theme}-${view.width}-${view.state}.png`;await page.screenshot({path:shot});row.screenshot=shot;
   if(row.unexpected.length)throw Error('unexpected fixture requests');if(row.pageerrors.length)throw Error('page error');
  }finally{await context.close();row.contextClosed=true;fs.writeFileSync(input.out+'/browser-result.json',JSON.stringify(result,null,2)+'\n');}
 }
 result.outcome='passed';
})().catch(error=>{result.outcome='stopped';result.error=String(error);process.exitCode=1}).finally(async()=>{if(browser)await browser.close();result.browserControlClosed=true;fs.writeFileSync(input.out+'/browser-result.json',JSON.stringify(result,null,2)+'\n');});
