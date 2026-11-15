// Real pages, local synthetic transport only. Run from ui/ with Node >=22.
import assert from 'node:assert/strict'
import {spawn} from 'node:child_process'
import {mkdtemp,readFile} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {createServer} from 'vite'
import react from '@vitejs/plugin-react'

const html=`<html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="root"></div><script type="module">
import React from 'react';import {createRoot} from 'react-dom/client';import i18n from '/src/i18n';import '/src/styles/main.css';
window.i18n=i18n;window.calls=[];window.cfg={enabled:false,profile:'custom',operation_mode:'balanced',inbound_discount_exit_share_min_override:0.15,profile_defaults:{moderate:{inbound_discount_exit_share_min:0.1}},inbound_passive_enabled:true};
const item={id:'r8:test',rule:'R8',kind:'desconto_entrada',status:'open',peer_alias:'Seller fixture',channel_point:'ab:0',confidence:'high',value_sat_day:100,first_seen_at:new Date().toISOString(),invalidate_when:'inbound_discount_set_or_refilled',evidence:[{key:'inbound_fee_ppm',value:0,unit:'ppm'},{key:'inbound_discount_suggested_ppm',value:701,unit:'ppm'}],actions:[{step:1,tool:'autofee_review',params:{channel_point:'ab:0'}}],guards:[]};
window.fetch=async(input,options={})=>{const path=String(input).split('?')[0],method=options.method||'GET';window.calls.push({path,method,body:options.body});let data={};
if(path==='/api/auth/state')data={authenticated:true,csrf_token:'synthetic-test-token'};
else if(path==='/api/lnops/autofee/config'){if(method==='POST')Object.assign(window.cfg,JSON.parse(options.body));data=window.cfg;}
else if(path==='/api/lnops/insights')data={computed_at:new Date().toISOString(),items:[item],sources:[],summary:{},scorecard:[]};
else if(path==='/api/lnops/channels')data={channels:[]};
else if(path==='/api/lnops/autofee/channels')data={settings:[]};
else if(path==='/api/lnops/autofee/results')data={items:[],lines:[]};
else if(method!=='GET')throw Error('Unexpected mutation '+path);
return new Response(JSON.stringify(data),{headers:{'Content-Type':'application/json'}})};
const which=new URL(location.href).searchParams.get('case');const {default:Component}=await import(which==='fees'?'/src/pages/FeeCenter':'/src/pages/Insights');
createRoot(document.getElementById('root')).render(React.createElement(Component));
</script></body></html>`
const server=await createServer({configFile:false,plugins:[{name:'release-0557',configureServer(s){s.middlewares.use(async(req,res,next)=>{if(!req.url?.startsWith('/fixture'))return next();res.setHeader('Content-Type','text/html');res.end(await s.transformIndexHtml('/fixture',html))})}},react()],server:{host:'127.0.0.1',port:0},logLevel:'error'})
const sleep=ms=>new Promise(r=>setTimeout(r,ms))
async function connect(url){const ws=new WebSocket(url);await new Promise((r,j)=>{ws.onopen=r;ws.onerror=j});let n=0;const pending=new Map();ws.onmessage=e=>{const v=JSON.parse(e.data),p=pending.get(v.id);if(p){pending.delete(v.id);v.error?p.reject(Error(v.error.message)):p.resolve(v.result)}};return{ws,call:(method,params={})=>new Promise((resolve,reject)=>{const id=++n;pending.set(id,{resolve,reject});ws.send(JSON.stringify({id,method,params}))})}}
let child,browser,page
try{
 await server.listen();const base='http://127.0.0.1:'+server.httpServer.address().port,profile=await mkdtemp(join(tmpdir(),'los0557-ui-'));
 child=spawn('C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',['--headless=new','--disable-gpu','--no-first-run','--remote-debugging-port=0','--user-data-dir='+profile],{stdio:'ignore',windowsHide:true});let port;
 for(let i=0;i<100;i++){try{port=(await readFile(join(profile,'DevToolsActivePort'),'utf8')).trim().split('\n');break}catch{await sleep(100)}}assert(port);
 browser=await connect('ws://127.0.0.1:'+port[0]+port[1]);const target=await(await fetch('http://127.0.0.1:'+port[0]+'/json/new?about:blank',{method:'PUT'})).json();page=await connect(target.webSocketDebuggerUrl);
 const ev=async expression=>{const r=await page.call('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw Error(r.exceptionDetails.exception?.description||r.exceptionDetails.text);return r.result.value};
 const wait=async expression=>{for(let i=0;i<150;i++){if(await ev(expression))return;await sleep(100)}throw Error('Timed out '+expression)};
 const click=async key=>{assert(await ev(`(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent.includes(i18n.t('${key}')));if(!b)return false;b.click();return true})()`));await sleep(100)};
 for(const language of ['en','pt-BR'])for(const width of [1280,390]){
  await page.call('Emulation.setDeviceMetricsOverride',{width,height:1100,deviceScaleFactor:1,mobile:width===390});
  await page.call('Page.navigate',{url:base+'/fixture?case=insights'});await wait('!!document.querySelector("article")');await ev(`i18n.changeLanguage('${language}')`);await sleep(100);
  const card=`[...document.querySelectorAll('article')].find(a=>a.textContent.includes('Seller fixture'))`;
  await wait(`!!(${card})`);
  assert(await ev(`(${card}).textContent.includes(i18n.t('insights.kind.desconto_entrada.label'))`));
  assert(await ev(`(${card}).textContent.includes(i18n.t('insights.evidence.inbound_discount_suggested_ppm'))`));
  assert(await ev(`!!document.querySelector('a[href="#fee-center?channel_point=ab%3A0"]')`));
  await click('insights.kind.desconto_entrada.label');
  assert.equal(await ev(`[...document.querySelectorAll('article')].filter(a=>a.textContent.includes('Seller fixture')).length`),1);
  assert.equal(await ev('calls.filter(c=>c.method!=="GET").length'),0,'R8 is advisory only');
  assert(await ev('document.documentElement.scrollWidth<=innerWidth'));
  await page.call('Page.navigate',{url:base+'/fixture?case=fees'});await wait('!!window.cfg && !!document.querySelector("h2")');await ev(`i18n.changeLanguage('${language}')`);await sleep(150);
  await click('feeCenter.controls.title');await click('lightningOps.autofeeMovementSettingsButton');
  const input=`[...document.querySelectorAll('label')].find(l=>l.textContent.includes(i18n.t('lightningOps.autofeeInboundDiscountExitShareMinOverride'))).querySelector('input')`;
  assert.equal(await ev(`(${input}).value`),'15');
  for(const [raw,expected] of [['100',1],['',0],['5',0.05]]){
   await ev(`(()=>{const e=${input};Object.getOwnPropertyDescriptor(HTMLInputElement.prototype,'value').set.call(e,'${raw}');e.dispatchEvent(new Event('input',{bubbles:true}))})()`);await sleep(75);await click('common.save');
   await wait(`calls.some(c=>c.method==='POST'&&JSON.parse(c.body).inbound_discount_exit_share_min_override===${expected})`);
   assert.equal(await ev('cfg.enabled'),false,'saving overrides must not enable AutoFee');
   assert.equal(await ev(`(${input}).value`),raw);
  }
  assert.equal(await ev(`calls.filter(c=>c.method!=='GET').every(c=>c.path==='/api/lnops/autofee/config')`),true);
  assert(await ev('document.documentElement.scrollWidth<=innerWidth'));
 }
 console.log('PASS: R8 card/filter/navigation is advisory; Fee Center cap load/save/default/off round trips EN/PT desktop/mobile; no unintended operation.');
}finally{page?.ws.close();if(browser){try{await browser.call('Browser.close')}catch{}browser.ws.close()}else child?.kill();await server.close()}
