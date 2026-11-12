// Actual AppStore component, synthetic local GETs only. No node mutations.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdtemp, readFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { createServer } from 'vite'
import react from '@vitejs/plugin-react'

const html = `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="root"></div><script type="module">
import React from 'react';import {createRoot} from 'react-dom/client';import i18n from '/src/i18n';import AppStore from '/src/pages/AppStore';import '/src/styles/main.css';
window.i18n=i18n;window.mutations=[];
window.fetch=async(input,options={})=>{
 const path=String(input).split('?')[0];if(options.method&&options.method!=='GET'){window.mutations.push(path);throw Error('Unexpected mutation')}
 let data={};
 if(path==='/api/apps')data=[{id:'lnbits',name:'LNbits',installed:false,status:'not_installed',port:5000,security_notices:['elevated_lnd_access']},{id:'btcpay',name:'BTCPay Server',installed:false,status:'not_installed',port:23000,security_notices:['limited_lnd_access']}];
 if(path==='/api/bitcoin/source')data={source:'remote'};
 if(path==='/api/bitcoin-local/status')data={source:'none'};
 return new Response(JSON.stringify(data),{headers:{'Content-Type':'application/json'}});
};createRoot(document.getElementById('root')).render(React.createElement(AppStore));
</script></body></html>`
const server=await createServer({configFile:false,plugins:[{name:'lnbits-fixture',configureServer(s){s.middlewares.use(async(req,res,next)=>{if(!req.url?.startsWith('/fixture'))return next();res.setHeader('Content-Type','text/html');res.end(await s.transformIndexHtml('/fixture',html))})}},react()],server:{host:'127.0.0.1',port:0},logLevel:'error'})
const profile=await mkdtemp(join(tmpdir(),'los-lnbits-notice-'))
const sleep=ms=>new Promise(r=>setTimeout(r,ms))
async function connect(url){const ws=new WebSocket(url);await new Promise((r,j)=>{ws.addEventListener('open',r,{once:true});ws.addEventListener('error',j,{once:true})});let n=0;const pending=new Map();ws.addEventListener('message',ev=>{const v=JSON.parse(ev.data);const p=pending.get(v.id);if(p){pending.delete(v.id);v.error?p.reject(Error(v.error.message)):p.resolve(v.result)}});return {ws,call:(method,params={})=>new Promise((resolve,reject)=>{const id=++n;pending.set(id,{resolve,reject});ws.send(JSON.stringify({id,method,params}))})}}
let child,browser,page
try{
 await server.listen();const base='http://127.0.0.1:'+server.httpServer.address().port;
 child=spawn(process.env.LOS_EDGE_PATH||'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',['--headless=new','--disable-gpu','--no-first-run','--remote-debugging-port=0','--user-data-dir='+profile],{stdio:'ignore',windowsHide:true});
 let port;for(let i=0;i<100;i++){try{port=(await readFile(join(profile,'DevToolsActivePort'),'utf8')).trim().split('\n');break}catch{await sleep(100)}}assert(port);
 browser=await connect('ws://127.0.0.1:'+port[0]+port[1]);const target=await(await fetch('http://127.0.0.1:'+port[0]+'/json/new?about:blank',{method:'PUT'})).json();page=await connect(target.webSocketDebuggerUrl);
 const ev=async expression=>{const r=await page.call('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw Error(r.exceptionDetails.exception?.description||r.exceptionDetails.text);return r.result.value};
 const wait=async exp=>{for(let i=0;i<150;i++){if(await ev(exp))return;await sleep(100)}throw Error('Timed out: '+exp)};
 for(const language of ['en','pt-BR'])for(const width of [1280,390]){
  await page.call('Emulation.setDeviceMetricsOverride',{width,height:900,deviceScaleFactor:1,mobile:width===390});
  await page.call('Page.navigate',{url:base+'/fixture'});await wait(`!!window.i18n && document.querySelectorAll('h3').length===2`);
  await ev(`i18n.changeLanguage('${language}')`);await sleep(150);
  const notice=await ev(`i18n.t('appStore.lnbitsWalletOnlyNotice')`);assert(!notice.startsWith('appStore.'));
  const card=await ev(`[...document.querySelectorAll('h3')].find(x=>x.textContent==='LNbits').closest('.section-card').textContent`);
  assert(card.includes(notice));assert(card.includes(await ev(`i18n.t('appStore.elevatedLndAccessCardBody')`)));
  const other=await ev(`[...document.querySelectorAll('h3')].find(x=>x.textContent==='BTCPay Server').closest('.section-card').textContent`);
  assert(!other.includes(notice));assert(other.includes(await ev(`i18n.t('appStore.limitedLndAccessCardBody')`)));
  assert(await ev(`document.documentElement.scrollWidth<=innerWidth`));
  await ev(`[...[...document.querySelectorAll('h3')].find(x=>x.textContent==='LNbits').closest('.section-card').querySelectorAll('button')].find(x=>x.textContent.trim()===i18n.t('appStore.install')).click()`);
  await wait(`document.body.textContent.split(i18n.t('appStore.lnbitsWalletOnlyNotice')).length===3`);
  assert.deepEqual(await ev('window.mutations'),[]);
 }
 console.log('PASS: LNbits notice + Lightning spending warning, unchanged BTCPay, PT/EN desktop/mobile, install confirmation, no mutations.')
}finally{page?.ws.close();if(browser){try{await browser.call('Browser.close')}catch{}browser.ws.close()}else child?.kill();await server.close()}
