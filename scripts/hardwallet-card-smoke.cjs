// Authenticated lab UI regression; app state/preferences are browser-only fixtures.
const fs=require('node:fs'),assert=require('node:assert/strict');
const {chromium}=require(process.env.PLAYWRIGHT_MODULE || 'playwright');
(async()=>{
 const password=fs.readFileSync(0,'utf8').trim();
 const base=process.env.LOS_MANAGER_URLS.split(',')[0];
 const browser=await chromium.launch({headless:true,executablePath:process.env.CHROME_PATH});
 try {
  const context=await browser.newContext({viewport:{width:1445,height:1050}});
  const login=await context.request.post(base+'/api/auth/login',{data:{password},headers:{Origin:base}});
  assert.equal(login.status(),200);
  const catalog=await (await context.request.get(base+'/api/apps')).json();
  let state='running',hidden=[];
  await context.route('**/api/apps',r=>r.fulfill({json:catalog.map(a=>a.id==='brln-hardwallet'?{...a,installed:true,status:state}:a)}));
  await context.route('**/api/ui/preferences/apps',r=>{
   if(r.request().method()==='PUT')hidden=r.request().postDataJSON().hidden;
   return r.fulfill({json:{version:1,exists:true,hidden}});
  });
  const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.name));
  const card=page.locator('.section-card').filter({has:page.getByRole('heading',{name:/Hardwallet/})});
  const hide=card.getByRole('button',{name:/^Hide |^Esconder |^Ocultar /});
  await page.goto(base+'/#apps');
  const title=card.getByRole('heading',{name:/Hardwallet/});await title.waitFor();
  const status=card.getByText(/^Running$|^Executando$/i);await status.waitFor();
  const h=await title.boundingBox(),b=await status.boundingBox();
  assert(Math.abs(h.y-b.y)<12 && b.x>h.x,'Status must share the title row, at the right');
  assert.equal(await hide.count(),0,'Running apps follow Store rule: cannot hide');
  const electrs=page.locator('.section-card').filter({has:page.getByRole('heading',{name:'Electrs',exact:true})});
  const referenceTitle=await electrs.getByRole('heading',{name:'Electrs',exact:true}).boundingBox();
  const referenceBadge=await electrs.getByText(/^Stopped$|^Parado$/i).boundingBox();
  assert(Math.abs(referenceTitle.y-referenceBadge.y)<12 && referenceBadge.x>referenceTitle.x,'Existing Electrs header must keep the same layout');
  state='stopped';await page.reload();await hide.waitFor();
  const eye=await hide.boundingBox(),titleBox=await title.boundingBox();
  assert(Math.abs(eye.y-titleBox.y)<12,'Hide control must remain in the header');
  await hide.click();await card.waitFor({state:'detached'});
  await page.getByRole('button',{name:/^Hidden \(1\)|^Ocultos \(1\)/}).click();
  const show=card.getByRole('button',{name:/^Show .* in the store$|^Mostrar /});await show.waitFor();
  await page.reload();await show.waitFor();await show.click();
  assert.deepEqual(hidden,[],'Show must remove the saved hidden preference');
  await page.getByRole('button',{name:/^All$|^Todos$/}).click();await hide.waitFor();
  if(process.env.SCREENSHOT_PATH)await card.screenshot({path:process.env.SCREENSHOT_PATH});
  await page.setViewportSize({width:390,height:844});await title.waitFor();
  assert(await card.evaluate(el=>el.scrollWidth<=el.clientWidth+1),'Mobile card must not overflow');
  assert.deepEqual(errors,[]);
  console.log('PASS: card header/status alignment, standard running/stopped visibility, hide/show/reload and mobile width.');
 }finally{await browser.close()}
})().catch(e=>{console.error(e.message);process.exitCode=1});
