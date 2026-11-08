// Windows/headless Edge integration test. Local synthetic GETs only; no node access.
// Run from ui/: node tests/fiat-display.browser.test.mjs
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdtemp, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { createServer } from 'vite'
import react from '@vitejs/plugin-react'

const html = `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="root"></div><script type="module">
import React from 'react';import {createRoot} from 'react-dom/client';
import i18n from '/src/i18n';import {FiatProvider,FiatAmount} from '/src/components/FiatDisplay';
import Topbar from '/src/components/Topbar';import Pulse from '/src/components/dashboard/NodePulseRow';import Wallet from '/src/pages/Wallet';
import '/src/styles/main.css';import '/src/styles/fiat.css';
window.i18n=i18n;window.errors=[];window.calls=[];window.mode=new URLSearchParams(location.search).get('mode');
window.fetch=async (input,options={})=>{
 const path=String(input).split('?')[0];window.calls.push(path);
 if(options.method && options.method!=='GET')throw Error('Unexpected mutation');
 let data={};
 if(path==='/api/bitcoin/market') {
  if(mode==='offline')throw Error('offline');
  data={updated_at:new Date(mode==='expired'?0:Date.now()).toISOString(),stale:mode==='stale',price_error:mode==='price-error'?'unavailable':'',
   fees:{fastestFee:2},prices:mode==='missing'?[]:[{currency:'BRL',value:500000},{currency:'USD',value:mode==='zero'?0:100000},{currency:'EUR',value:90000}]};
 }
 if(path==='/api/health')data={status:'OK'};
 if(path==='/api/wallet/summary')data={updated_at:new Date().toISOString(),balances:{onchain_sat:1000000,onchain_confirmed_sat:1000000,lightning_sat:2000000,lightning_local_sat:2000000}};
 return new Response(JSON.stringify(data),{headers:{'Content-Type':'application/json'}});
};
function Fixture(){const [wallet,setWallet]=React.useState(false);return React.createElement(FiatProvider,null,
 React.createElement(Topbar,{theme:'dark',visualTheme:'default',palette:'default',onAppearanceApply:()=>{}}),
 React.createElement('button',{id:'navigate',onClick:()=>setWallet(v=>!v)},'Navigate'),
 wallet?React.createElement(Wallet):React.createElement(Pulse,{live:{forward_fee_revenue_sats:10000,net_routing_profit_sats:-5000},range:null,summary:null,movement:null,lnd:null}),
 React.createElement('div',{id:'zero'},React.createElement(FiatAmount,{sats:0})),
 React.createElement('div',{id:'unknown'},React.createElement(FiatAmount,{sats:undefined})));}
createRoot(document.getElementById('root')).render(React.createElement(Fixture));
</script></body></html>`
const server = await createServer({ configFile: false, plugins: [{name:'fiat-fixture',configureServer(s) {
 s.middlewares.use(async(req,res,next)=>{if(!req.url?.startsWith('/fixture'))return next();res.setHeader('Content-Type','text/html');res.end(await s.transformIndexHtml('/fixture',html))})
}},react()],server:{host:'127.0.0.1',port:0},logLevel:'error'})
const profile = await mkdtemp(join(tmpdir(),'los-fiat-test-'))
const sleep=ms=>new Promise(r=>setTimeout(r,ms))
async function connect(url){const ws=new WebSocket(url);await new Promise((r,j)=>{ws.addEventListener('open',r,{once:true});ws.addEventListener('error',j,{once:true})});let n=0;const pending=new Map();ws.addEventListener('message',ev=>{const v=JSON.parse(ev.data);const p=pending.get(v.id);if(p){pending.delete(v.id);v.error?p.reject(Error(v.error.message)):p.resolve(v.result)}});return {ws,call:(method,params={})=>new Promise((resolve,reject)=>{const id=++n;pending.set(id,{resolve,reject});ws.send(JSON.stringify({id,method,params}))})}}
let child,browser,page
try {
 await server.listen();const base='http://127.0.0.1:'+server.httpServer.address().port
 child=spawn(process.env.LOS_EDGE_PATH||'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',['--headless=new','--disable-gpu','--no-first-run','--remote-debugging-port=0','--user-data-dir='+profile],{stdio:'ignore',windowsHide:true})
 let port;for(let i=0;i<100;i++){try{port=(await readFile(join(profile,'DevToolsActivePort'),'utf8')).trim().split('\n');break}catch{await sleep(100)}}assert(port)
 browser=await connect('ws://127.0.0.1:'+port[0]+port[1]);const target=await(await fetch('http://127.0.0.1:'+port[0]+'/json/new?about:blank',{method:'PUT'})).json();page=await connect(target.webSocketDebuggerUrl)
 const ev=async expression=>{const r=await page.call('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw Error(r.exceptionDetails.exception?.description||r.exceptionDetails.text);return r.result.value}
 const wait=async exp=>{for(let i=0;i<150;i++){if(await ev(exp))return;await sleep(100)}throw Error('Timed out: '+exp)}
 const navigate=async suffix=>{await page.call('Page.navigate',{url:base+'/fixture'+suffix});await wait(`!!document.querySelector('.fiat-selector') && window.calls.includes('/api/bitcoin/market')`);await sleep(200)}
 await navigate('');await wait(`document.querySelectorAll('.fiat-equivalent').length===3`)
 await page.call('Emulation.setDeviceMetricsOverride',{width:1440,height:900,deviceScaleFactor:1,mobile:false})
 if(process.env.LOS_FIAT_SCREENSHOT) {
  const shot=await page.call('Page.captureScreenshot',{format:'png'});
  await writeFile(process.env.LOS_FIAT_SCREENSHOT,Buffer.from(shot.data,'base64'))
 }
 const sats=await ev(`[...document.querySelectorAll('.metric-tile__value')].map(x=>x.textContent)`)
 const select=async value=>{await ev(`(()=>{const s=document.querySelector('.fiat-selector');s.value='${value}';s.dispatchEvent(new Event('change',{bubbles:true}))})()`);await sleep(100)}
 for(const [currency,expected] of [['BRL',50],['USD',10],['EUR',9]]) {
  await select(currency)
  const text=await ev(`document.querySelector('.fiat-equivalent').textContent`)
  const formatted=await ev(`new Intl.NumberFormat(i18n.language==='pt-BR'?'pt-BR':'en-US',{style:'currency',currency:'${currency}'}).format(${expected})`)
  assert(text.includes(formatted),text)
  assert.deepEqual(await ev(`[...document.querySelectorAll('.metric-tile__value')].map(x=>x.textContent)`),sats)
 }
 assert((await ev(`document.querySelectorAll('.fiat-equivalent')[1].textContent`)).includes('-'))
 assert.equal(await ev(`document.querySelector('#unknown').textContent`),'')
 assert.equal(await ev(`window.calls.filter(x=>x==='/api/bitcoin/market').length`),1)
 await ev(`document.querySelector('#navigate').click()`);await wait(`document.querySelectorAll('.fiat-equivalent').length===3 && window.calls.includes('/api/wallet/summary')`)
 assert((await ev(`document.querySelector('.fiat-equivalent').textContent`)).includes('900'))
 assert.equal(await ev(`window.calls.filter(x=>x==='/api/bitcoin/market').length`),1)
 await navigate('');assert.equal(await ev(`document.querySelector('.fiat-selector').value`),'EUR')
 await ev(`i18n.changeLanguage('pt-BR')`);await wait(`document.querySelector('.fiat-selector').getAttribute('aria-label')==='Moeda de exibição'`)
 assert((await ev(`document.querySelector('.fiat-equivalent').textContent`)).includes('9,00'))
 await page.call('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:true})
 assert(await ev(`document.querySelector('.fiat-selector').getBoundingClientRect().right<=innerWidth`))
 for(const mode of ['stale','expired','price-error','missing','offline']) {
  await navigate('?mode='+mode);assert.equal(await ev(`document.querySelectorAll('.fiat-equivalent').length`),0,mode)
  assert.equal(await ev(`document.querySelectorAll('.metric-tile__value').length`),5)
 }
 await navigate('?mode=zero');await select('USD');assert.equal(await ev(`document.querySelectorAll('.fiat-equivalent').length`),0)
 await ev(`localStorage.setItem('los-fiat-currency','INVALID')`);await navigate('');assert.equal(await ev(`document.querySelector('.fiat-selector').value`),'BRL')
 await page.call('Page.addScriptToEvaluateOnNewDocument',{source:`Object.defineProperty(window,'localStorage',{get(){throw Error('Storage denied')}})`})
 await navigate('');await select('USD');assert.equal(await ev(`document.querySelector('.fiat-selector').value`),'USD')
 console.log('PASS: currencies, negative/zero/unknown amounts, unchanged sats, Wallet and dashboard, single shared quote request, persistence, PT/EN, mobile selector, stale/failed/missing quotes.')
} finally {page?.ws.close();if(browser){try{await browser.call('Browser.close')}catch{}browser.ws.close()}else child?.kill();await server.close()}
