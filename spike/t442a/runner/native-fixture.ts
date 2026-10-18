import { test as base, expect, chromium, type Page, type TestInfo } from "/Users/ben/.codex/worktrees/t442a-preview-recovery/phebs.com/ui/node_modules/@playwright/test/index.mjs"
import { writeFileSync } from 'node:fs'
export { expect }
export type { Page }
export const test=base.extend({browser:[async ({},use)=>{const b=await chromium.connectOverCDP(process.env.T442_CDP!);try{await use(b)}finally{await b.close()}},{scope:'worker'}]})
test.beforeEach(async({context})=>{await context.addInitScript({path:process.env.T442_AXE!})})
export async function auditAppearance(page:Page,info:TestInfo) {
 const audit=await page.evaluate(async()=>{
  const axe=(window as any).axe;
  const r=await axe.run(document.querySelector('[aria-labelledby=appearance-heading]'),{runOnly:{type:'tag',values:['wcag2a','wcag2aa','wcag21a','wcag21aa']}});
  return {version:axe.version,violations:r.violations,passes:r.passes.length,incomplete:r.incomplete.length};
 });
 writeFileSync(info.outputPath('native-qa.json'),JSON.stringify({title:info.title,viewport:page.viewportSize(),capture_state:info.title.includes('unavailable')?'unavailable':'ready',audit},null,2)+'\n');
 expect(audit.version).toBe('4.12.1');expect(audit.violations).toEqual([]);expect(audit.incomplete).toBe(0);
}
