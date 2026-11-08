// Run from ui/: node tests/insights-components.browser.test.mjs (Windows/headless Edge).
import assert from 'node:assert/strict'
import {spawn} from 'node:child_process'
import {mkdtemp,readFile} from 'node:fs/promises'
import {tmpdir} from 'node:os'
import {join} from 'node:path'
import {createServer} from 'vite'
import react from '@vitejs/plugin-react'
const root=process.cwd()
const html=`<html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="root"></div><script type="module">
import React from 'react';import {createRoot} from 'react-dom/client';import i18n from '/src/i18n';import Card from '/src/pages/insights/InsightCard';import Map from '/src/pages/insights/SignalMap';import '/src/styles/main.css';
window.i18n=i18n;window.calls=[];window.quoteFee=1000;
window.fetch=async(input,options={})=>{window.calls.push([String(input),options.body]);return new Response(JSON.stringify(String(input).includes('mempool')?{economyFee:2}:{estimated_all_in_fee_sat:window.quoteFee}),{headers:{'Content-Type':'application/json'}})};
const item={id:'r1:fixture',rule:'R1',kind:'repor_vendedor',status:'open',peer_alias:'Fixture',peer_pubkey:'02'+'ab'.repeat(32),channel_point:'ab:0',confidence:'high',value_sat_day:100,first_seen_at:new Date().toISOString(),invalidate_when:'local_at_target_24h',evidence:[{key:'sell_ppm_7d',value:800,unit:'ppm'}],actions:[{step:1,tool:'loop_in',params:{amount_sat:'1000000',amount_limit:'onchain',cap_ppm:'500'}},{step:2,tool:'set_fee',params:{channel_point:'ab:0'}}],guards:[{code:'we_are_funder',passed:true},{code:'peer_funded_close_free',passed:false,advisory:true}]};
function Fixture(){const [status,setStatus]=React.useState('open');window.changeStatus=setStatus;return React.createElement('main',null,React.createElement(Map,{sources:['autofee','interlock','rebalance','ranking','open_candidates'].map(id=>({id,ok:true,age_sec:18})),teach:true,formatAge:()=> '18 s'}),React.createElement(Card,{item:{...item,status},teach:true,busy:false,formatNumber:String,formatRelative:()=> 'now',actionError:'fixture error',onRule:()=>{},onStatus:(item,status,reason)=>{window.decision={status,reason};setStatus(status)}}))};createRoot(document.getElementById('root')).render(React.createElement(Fixture));
</script></body></html>`
const server=await createServer({root,configFile:false,plugins:[{name:'fixture',configureServer(s){s.middlewares.use(async(req,res,next)=>{if(!req.url.startsWith('/fixture'))return next();res.setHeader('Content-Type','text/html');res.end(await s.transformIndexHtml('/fixture',html))})}},react()],server:{host:'127.0.0.1',port:0},logLevel:'error'})
const sleep=ms=>new Promise(r=>setTimeout(r,ms));let child,page,browser
async function connect(url){const ws=new WebSocket(url);await new Promise((r,j)=>{ws.onopen=r;ws.onerror=j});let n=0;const waits=new Map();ws.onmessage=e=>{const v=JSON.parse(e.data),w=waits.get(v.id);if(w){waits.delete(v.id);v.error?w.j(Error(v.error.message)):w.r(v.result)}};return {ws,call:(method,params={})=>new Promise((r,j)=>{const id=++n;waits.set(id,{r,j});ws.send(JSON.stringify({id,method,params}))})}}
try{
 await server.listen();const base='http://127.0.0.1:'+server.httpServer.address().port;const profile=await mkdtemp(join(tmpdir(),'los0554-insights-'));
 child=spawn('C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',['--headless=new','--disable-gpu','--no-first-run','--remote-debugging-port=0','--user-data-dir='+profile],{stdio:'ignore',windowsHide:true});let info;
 for(let i=0;i<100;i++){try{info=(await readFile(join(profile,'DevToolsActivePort'),'utf8')).trim().split('\n');break}catch{await sleep(100)}}assert(info);
 browser=await connect('ws://127.0.0.1:'+info[0]+info[1]);const tab=await(await fetch('http://127.0.0.1:'+info[0]+'/json/new?about:blank',{method:'PUT'})).json();page=await connect(tab.webSocketDebuggerUrl);
 const ev=async expression=>{const result=await page.call('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(result.exceptionDetails)throw Error(JSON.stringify(result.exceptionDetails));return result.result.value};
 const wait=async expression=>{for(let i=0;i<150;i++){if(await ev(expression))return;await sleep(100)}throw Error('Timed out: '+expression)};
 await page.call('Page.navigate',{url:base+'/fixture'});await wait('!!document.querySelector("article")');
 const click=async key=>{await ev(`[...document.querySelectorAll('button')].find(b=>b.textContent.trim().startsWith(i18n.t('${key}'))).click()`);await sleep(150)};
 for(const language of ['en','pt-BR']){
  await ev(`i18n.changeLanguage('${language}')`);
  for(const width of [1280,768,390]){await page.call('Emulation.setDeviceMetricsOverride',{width,height:1200,deviceScaleFactor:1,mobile:width<768});await sleep(150);assert.equal(await ev('document.documentElement.scrollWidth>innerWidth'),false,language+' '+width+' overflow')}
  assert.equal(await ev('document.body.innerText.includes("we_are_funder")'),false);
  assert.equal(await ev('document.querySelector("[role=alert]").textContent'),'fixture error');
  await ev("changeStatus('open')");await sleep(150);await click('insights.card.dismiss');
  assert(await ev("(()=>{const a=document.querySelector('article').getBoundingClientRect(),m=document.querySelector('.bottom-full').getBoundingClientRect();return m.top>=a.top&&m.bottom<=a.bottom})()"));
  await click('insights.dismissReason.later');assert.equal(await ev('decision.status'),'dismissed');
  await ev("changeStatus('expired')");await sleep(150);await click('insights.card.doneAfterExpired');assert.equal(await ev('decision.status'),'done');
  assert(await ev("!!document.querySelector('a[href=\"#lightning-ops?channel_point=ab%3A0\"]')"));
 }
 assert.equal(await ev('calls.length'),0,'quotes must be opt-in');
 await click('insights.card.quote');await wait("document.body.innerText.includes(i18n.t('insights.card.quoteAboveCap',{cap:'500',sale:'800'}))");assert.equal(await ev('calls.length'),2);
 await ev('window.quoteFee=100');await click('insights.card.quote');await wait("document.body.innerText.includes(i18n.t('insights.card.quoteWithinCap',{cap:'500'}))");
 console.log('PASS: real Insights components EN/PT at 1280/768/390px, no overflow, legacy translations, inline error, dismiss, expired-to-done, set-fee navigation, opt-in quote above/below cap. Synthetic local APIs only.');
}finally{if(browser){try{await browser.call('Browser.close')}catch{}}page?.ws.close();browser?.ws.close();child?.kill();await server.close()}
