// Real component and API client, local synthetic transport; no node credentials.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdtemp, readFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { createServer } from 'vite'
import react from '@vitejs/plugin-react'
const html=`<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="root"></div><script type="module">
import React from 'react';import {createRoot} from 'react-dom/client';import i18n from '/src/i18n';import Card from '/src/components/dashboard/TorLndRestOnion';import '/src/styles/main.css';
window.i18n=i18n;window.posts=[];window.mode='absent';window.copied='';
Object.defineProperty(navigator,'clipboard',{value:{writeText:async value=>{window.copied=value}}});
window.fetch=async(input,options={})=>{
 const path=String(input).split('?')[0];let data={};
 if(path==='/api/auth/state')data={authenticated:true,enabled:true,csrf_token:'local-fixture'};
 else if(path==='/api/tor/lnd-rest'||path==='/api/tor/lnd-rest/ensure'){
 if(options.method==='POST'){window.posts.push(JSON.parse(options.body));window.mode='configured'}
 data={status:window.mode==='down'?'absent':window.mode,rest_reachable:window.mode!=='down',managed:window.mode!=='manual'};
 if(window.mode==='configured'||window.mode==='manual'){data.status='configured';data.url='https://'+'a'.repeat(56)+'.onion:8080'}
 }else throw Error('Unexpected request '+path);
 return new Response(JSON.stringify(data),{headers:{'Content-Type':'application/json'}});
};createRoot(document.getElementById('root')).render(React.createElement(Card));
</script></body></html>`
const server=await createServer({configFile:false,plugins:[{name:'tor-fixture',configureServer(s){s.middlewares.use(async(req,res,next)=>{if(!req.url?.startsWith('/fixture'))return next();res.setHeader('Content-Type','text/html');res.end(await s.transformIndexHtml('/fixture',html))})}},react()],server:{host:'127.0.0.1',port:0},logLevel:'error'})
const profile=await mkdtemp(join(tmpdir(),'los-tor-rest-'))
const sleep=ms=>new Promise(r=>setTimeout(r,ms))
async function connect(url){const ws=new WebSocket(url);await new Promise((r,j)=>{ws.addEventListener('open',r,{once:true});ws.addEventListener('error',j,{once:true})});let n=0;const pending=new Map();ws.addEventListener('message',ev=>{const v=JSON.parse(ev.data);const p=pending.get(v.id);if(p){pending.delete(v.id);v.error?p.reject(Error(v.error.message)):p.resolve(v.result)}});return {ws,call:(method,params={})=>new Promise((resolve,reject)=>{const id=++n;pending.set(id,{resolve,reject});ws.send(JSON.stringify({id,method,params}))})}}
let child,browser,page
try{
 await server.listen();const base='http://127.0.0.1:'+server.httpServer.address().port;
 child=spawn(process.env.LOS_EDGE_PATH||'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',['--headless=new','--disable-gpu','--no-first-run','--remote-debugging-port=0','--user-data-dir='+profile],{stdio:'ignore',windowsHide:true});
 let port;for(let i=0;i<100;i++){try{port=(await readFile(join(profile,'DevToolsActivePort'),'utf8')).trim().split('\n');break}catch{await sleep(100)}}assert(port);
 browser=await connect('ws://127.0.0.1:'+port[0]+port[1]);const target=await(await fetch('http://127.0.0.1:'+port[0]+'/json/new?about:blank',{method:'PUT'})).json();page=await connect(target.webSocketDebuggerUrl);
 const ev=async expression=>{const r=await page.call('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw Error(r.exceptionDetails.exception?.description||r.exceptionDetails.text);return r.result.value};
 const wait=async exp=>{for(let i=0;i<100;i++){if(await ev(exp))return;await sleep(100)}throw Error('Timed out: '+exp)};
 const button=key=>`[...document.querySelectorAll('button')].find(x=>x.textContent.trim()===i18n.t('${key}'))`;
 for(const language of ['en','pt-BR'])for(const width of [1280,390]){
  await page.call('Emulation.setDeviceMetricsOverride',{width,height:900,deviceScaleFactor:1,mobile:width===390});
  await page.call('Page.navigate',{url:base+'/fixture'});await wait(`!!window.i18n && !!document.querySelector('input[type=checkbox]')`);
  await ev(`i18n.changeLanguage('${language}')`);await sleep(200);
  assert(await ev(`${button('torUpgrade.lndRest.create')}.disabled`));
  await ev(`document.querySelector('input[type=checkbox]').click()`);
  await wait(`!${button('torUpgrade.lndRest.create')}.disabled`);
  await ev(`${button('torUpgrade.lndRest.create')}.click()`);await wait(`!!document.querySelector('code')`);
  assert.deepEqual(await ev('window.posts'),[{confirm:true,confirm_password:''}]);
  await ev(`${button('common.copy')}.click()`);assert.match(await ev('window.copied'),/^https:\/\/[a-z]{56}\.onion:8080$/);
  assert(await ev(`document.body.textContent.includes(i18n.t('torUpgrade.lndRest.macaroon'))`));
  assert(await ev(`document.documentElement.scrollWidth<=innerWidth`));
  await ev(`window.mode='manual';${button('common.refresh')}.click()`);await wait(`document.body.textContent.includes(i18n.t('torUpgrade.lndRest.manual'))`);
  await ev(`window.mode='down';${button('common.refresh')}.click()`);await wait(`!!document.querySelector('input[type=checkbox]')`);
  assert(await ev(`${button('torUpgrade.lndRest.create')}.disabled`));
  assert.equal(await ev('window.posts.length'),1);
 }
 console.log('PASS: PT/EN desktop/mobile, confirmation gate, fixed request, copy, manual reuse, REST unavailable, read-only refresh.')
}finally{page?.ws.close();if(browser){try{await browser.call('Browser.close')}catch{}browser.ws.close()}else child?.kill();await server.close()}
