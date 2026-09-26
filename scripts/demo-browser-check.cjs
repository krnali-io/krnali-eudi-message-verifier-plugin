// Exercise the actual demo binary on loopback; only sample data is submitted.
const {chromium}=require(process.env.PLAYWRIGHT_PATH||'/tmp/verify-link-browser/playwright-core');
const fs=require('node:fs');const assert=require('node:assert/strict');
const base='http://127.0.0.1:18095',root='/src/test-results';
(async()=>{fs.mkdirSync(root,{recursive:true});const chrome=fs.readdirSync('/ms-playwright').find(n=>/^chromium-/.test(n));const browser=await chromium.launch({executablePath:'/ms-playwright/'+chrome+'/chrome-linux64/chrome',headless:true,args:['--no-sandbox']});
try{const operator=await browser.newContext({viewport:{width:1440,height:1100},httpCredentials:{username:'agent@example.test',password:'demo-browser-only'}});const page=await operator.newPage();const visitor=await browser.newContext({viewport:{width:412,height:915}});const phone=await visitor.newPage();const errors=[];
for(const p of [page,phone]){p.on('pageerror',e=>errors.push(e.message));p.on('console',m=>{if(m.type()==='error'&&m.text().includes('Content Security Policy'))errors.push(m.text());});}
await page.goto(base+'/console');await page.getByText('Live · updates every 2s',{exact:true}).waitFor();assert.equal(await page.locator('.demo-banner').count(),1);assert.equal(await page.locator('#recipient').inputValue(),'Demo customer');await page.screenshot({path:root+'/demo-console-desktop.png',fullPage:true});
const channels=await page.request.get(base+'/api/channels');assert.deepEqual((await channels.json()).map(c=>c.id),['copylink']);
assert.equal((await visitor.request.get(base+'/api/sessions')).status(),401);
async function flow(preset,outcome,label,expectedStatus){
  await page.locator('input[name=preset][value="'+preset+'"]').check();await page.locator('#recipient').fill(label);await page.getByRole('button',{name:'Create demo link'}).click();await page.locator('#copy-result').waitFor({state:'visible'});const link=await page.locator('#copy-link').inputValue();
  await phone.goto(link);assert.equal(await phone.locator('.demo-banner').count(),1);await phone.getByRole('button',{name:'Try the demo check'}).click();await phone.getByRole('heading',{name:'You choose what to share.'}).waitFor();assert.equal(await phone.locator('.demo-banner').count(),1);
  if(label==='Demo matched person')await phone.screenshot({path:root+'/demo-wallet-mobile.png',fullPage:true});
  assert(await phone.locator('body').evaluate(el=>el.scrollWidth<=innerWidth));await phone.locator('button[value="'+outcome+'"]').click();await phone.getByText('Your simulated result is ready in the console. No real identity was verified.',{exact:true}).waitFor();
  await page.bringToFront();const row=page.locator('article.request').filter({has:page.getByText(label,{exact:true})});await row.locator('.badge').filter({hasText:'Demo · '+expectedStatus}).waitFor({timeout:15000});
  assert.equal((await phone.request.post(link+'/start',{headers:{Origin:base}})).status(),410);
  await page.locator('#dismiss-link').click();return row;
}
await flow('confirm-name','share','Demo matched person','Verified');await flow('confirm-name','different','Demo different person','Mismatch');await flow('confirm-name','decline','Demo declined','Declined');const age=await flow('over-18','under18','Demo under 18','Verified');assert.match(await age.innerText(),/Over 18: no/);
await page.screenshot({path:root+'/demo-console-results.png',fullPage:true});await page.setViewportSize({width:412,height:915});assert(await page.locator('body').evaluate(el=>el.scrollWidth<=innerWidth));await page.screenshot({path:root+'/demo-console-mobile.png',fullPage:true});
const csv=await(await page.request.get(base+'/api/sessions/export.csv')).text();assert(csv.startsWith('mode,'));assert(csv.includes('synthetic-demo'));assert(!csv.includes('Alex Demo')&&!csv.includes('Demo matched person'));
for(const path of ['/wallet/request','/hooks/telegram','/ui/presentations','/utilities/validations/msoMdoc/deviceResponse'])assert.equal((await phone.request.get(base+path)).status(),404,path);
assert.equal(errors.length,0,errors.join('\n'));console.log('PASS: demo branding, authenticated console, copy-link-only delivery, synthetic match/mismatch/decline/age, single-use links, CSV labels, responsive layout, private-route isolation and no JS/CSP errors');
}finally{await browser.close();}})().catch(e=>{console.error(e);process.exit(1);});
