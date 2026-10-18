const fs=require('node:fs'),path=require('node:path'),assert=require('node:assert/strict'),crypto=require('node:crypto');
const qa='/tmp/phebs-t4510a-qa-z3azm2qg';
const {utils}=require('/Users/ben/.codex/worktrees/t512-field-registration/phebs.com/ui/node_modules/playwright-core/lib/coreBundle.js');
const {PNG}=require('/Users/ben/.codex/worktrees/t512-field-registration/phebs.com/ui/node_modules/playwright-core/lib/utilsBundle.js');
const sha=b=>crypto.createHash('sha256').update(b).digest('hex');
const read=n=>JSON.parse(fs.readFileSync(path.join(qa,n,'result.json')));
const author=read('author-final'),fresh=read('fresh-ready');
assert.equal(author.outcome,'passed');assert.equal(fresh.outcome,'passed');assert(author.all_owned_groups_joined_cleanly&&fresh.all_owned_groups_joined_cleanly);
assert.equal(author.source_checkpoint,fresh.source_checkpoint);assert.equal(author.scroll_helper_sha256,fresh.scroll_helper_sha256);assert.deepEqual(author.tools,fresh.tools);assert.deepEqual(author.verified_census,fresh.verified_census);
for(const key of ['Browser','Protocol-Version','User-Agent','V8-Version','WebKit-Version'])assert.equal(author.renderer[key],fresh.renderer[key]);
const map=items=>new Map(items.map(x=>[x.name,x]));const a=map(author.frames),b=map(fresh.frames);assert.equal(a.size,8);assert.deepEqual([...a.keys()].sort(),[...b.keys()].sort());
const result={schema:'phebs-t4510a-native-pixel-comparison-v1',source_checkpoint:author.source_checkpoint,renderer:author.renderer.Browser,platform:'Darwin arm64; no Noble equivalence',frozen_page_Date:'2026-08-07T20:00:00Z',maxDiffPixelRatio:0.001,threshold:0.2,comparator:'Existing pinned Playwright image/png comparator; no masks, missing images or baseline reauthoring',rows:[],outcome:'passed'};
for(const name of [...a.keys()].sort()){
 const aa=fs.readFileSync(a.get(name).path),bb=fs.readFileSync(b.get(name).path),ap=PNG.sync.read(aa),bp=PNG.sync.read(bb);
 assert.equal(ap.width,bp.width);assert.equal(ap.height,bp.height);
 let diff=0;for(let i=0;i<ap.data.length;i+=4)if(ap.data[i]!==bp.data[i]||ap.data[i+1]!==bp.data[i+1]||ap.data[i+2]!==bp.data[i+2]||ap.data[i+3]!==bp.data[i+3])diff++;
 const comparison=utils.getComparator('image/png')(bb,aa,{maxDiffPixelRatio:0.001,threshold:0.2});
 const row={name,author_path:a.get(name).path,fresh_path:b.get(name).path,width:ap.width,height:ap.height,author_sha256:sha(aa),fresh_sha256:sha(bb),bytes_equal:aa.equals(bb),exact_rgba_different_pixels:diff,exact_rgba_difference_ratio:diff/(ap.width*ap.height),passed:comparison===null};
 if(comparison){row.error=comparison.errorMessage;if(comparison.diff)fs.writeFileSync(path.join(qa,name+'.diff.png'),comparison.diff);result.outcome='stopped';}
 result.rows.push(row);
}
fs.writeFileSync(path.join(qa,'comparison.json'),JSON.stringify(result,null,2)+'\n');
console.log(JSON.stringify({outcome:result.outcome,frames:result.rows.length,all_bytes_equal:result.rows.every(x=>x.bytes_equal),maximum_exact_pixel_difference_ratio:Math.max(...result.rows.map(x=>x.exact_rgba_difference_ratio)),path:path.join(qa,'comparison.json')}));
assert.equal(result.outcome,'passed');
