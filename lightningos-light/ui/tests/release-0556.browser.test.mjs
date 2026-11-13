// Real components with local synthetic transport; never connects to a node.
import assert from 'node:assert/strict'
import { spawn } from 'node:child_process'
import { mkdtemp, readFile, writeFile } from 'node:fs/promises'
import { join } from 'node:path'
import { tmpdir } from 'node:os'
import { createServer } from 'vite'
import react from '@vitejs/plugin-react'

const html = `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head><body><div id="root"></div><script type="module">
import React from 'react';import {createRoot} from 'react-dom/client';import i18n from '/src/i18n';import '/src/styles/main.css';
window.i18n=i18n;window.calls=[];window.reconciled=false;
const scenario=new URL(location.href).searchParams.get('case');const app=scenario.startsWith('app-');
sessionStorage.setItem('los-financial-intent-references-v1',JSON.stringify([{key:'a'.repeat(64),id:'b'.repeat(32)}]));
window.fetch=async(input,options={})=>{
 const path=String(input).split('?')[0];const method=options.method||'GET';window.calls.push({path,method});let data={},status=200;
 if(path==='/api/auth/state')data={authenticated:true,csrf_token:'synthetic-browser-token'};
 else if(path==='/api/apps')data=[{id:'lnbits',name:'LNbits',installed:true,status:'stopped',available:true,port:5000}];
 else if(path==='/api/bitcoin/source')data={source:'remote'};
 else if(path==='/api/bitcoin-local/status')data={source:'none'};
 else if(path==='/api/apps/lnbits/start'){status=409;data={error:'Synthetic broker refusal',code:scenario.slice(4)};}
 else if(path.endsWith('/reconcile')){window.reconciled=true;status=409;data={error:'Synthetic reconcile refusal',code:'channel_open_not_found'};}
 else if(path.startsWith('/api/financial/intents/')){
  if(window.reconciled&&scenario==='lookup-failure'){status=503;data={error:'Synthetic lookup unavailable'};}
  else data={state:window.reconciled?scenario:'uncertain',operation:'/api/lnops/channel/open',result:{error:'Synthetic operation rejected'}};
 }
 else if(method!=='GET')throw Error('Unexpected mutation '+path);
 return new Response(JSON.stringify(data),{status,headers:{'Content-Type':'application/json'}});
};
const {default:Component}=await import(app?'/src/pages/AppStore':'/src/components/PendingFinancialOperations');
createRoot(document.getElementById('root')).render(React.createElement(Component,{confirming:false}));
</script></body></html>`
const baseline = process.env.LOS_TEST_BASELINE === '1'
const server=await createServer({configFile:false,plugins:[{
 name:'release-0556-fixture',enforce:'pre',
 transform(code,id){
  if(!baseline||!id.endsWith('/PendingFinancialOperations.tsx'))return
  const start=code.indexOf('    let reconcileError =');const end=code.indexOf('    finally { setBusy(false) }',start)
  assert(start>=0&&end>start)
  return code.slice(0,start)+`    try { await reconcileFinancialIntent(id); const state = await getFinancialIntent(id); setStates(current => ({ ...current, [id]: state })) }
    catch (failure: any) { setError(failure?.message || 'Outcome remains uncertain.') }
`+code.slice(end)
 },
 configureServer(s){s.middlewares.use(async(req,res,next)=>{if(!req.url?.startsWith('/fixture'))return next();res.setHeader('Content-Type','text/html');res.end(await s.transformIndexHtml('/fixture',html))})}
},react()],server:{host:'127.0.0.1',port:0},logLevel:'error'})
const profile=await mkdtemp(join(tmpdir(),'los-0556-browser-'))
const sleep=ms=>new Promise(r=>setTimeout(r,ms))
async function connect(url){const ws=new WebSocket(url);await new Promise((r,j)=>{ws.addEventListener('open',r,{once:true});ws.addEventListener('error',j,{once:true})});let n=0;const pending=new Map();ws.addEventListener('message',ev=>{const v=JSON.parse(ev.data);const p=pending.get(v.id);if(p){pending.delete(v.id);v.error?p.reject(Error(v.error.message)):p.resolve(v.result)}});return {ws,call:(method,params={})=>new Promise((resolve,reject)=>{const id=++n;pending.set(id,{resolve,reject});ws.send(JSON.stringify({id,method,params}))})}}
let child,browser,page
try{
 await server.listen();const base='http://127.0.0.1:'+server.httpServer.address().port
 child=spawn(process.env.LOS_EDGE_PATH||'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe',['--headless=new','--disable-gpu','--no-first-run','--remote-debugging-port=0','--user-data-dir='+profile],{stdio:'ignore',windowsHide:true})
 let port;for(let i=0;i<100;i++){try{port=(await readFile(join(profile,'DevToolsActivePort'),'utf8')).trim().split('\n');break}catch{await sleep(100)}}assert(port)
 browser=await connect('ws://127.0.0.1:'+port[0]+port[1]);const target=await(await fetch('http://127.0.0.1:'+port[0]+'/json/new?about:blank',{method:'PUT'})).json();page=await connect(target.webSocketDebuggerUrl)
 const ev=async expression=>{const r=await page.call('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw Error(r.exceptionDetails.exception?.description||r.exceptionDetails.text);return r.result.value}
 const wait=async exp=>{for(let i=0;i<80;i++){if(await ev(exp))return;await sleep(100)}throw Error('Timed out: '+exp)}
 for(const language of ['en','pt-BR'])for(const width of [1280,390]){
  await page.call('Emulation.setDeviceMetricsOverride',{width,height:900,deviceScaleFactor:1,mobile:width===390})
  for(const outcome of ['rejected','completed','cancelled','uncertain','lookup-failure']){
   await page.call('Page.navigate',{url:base+'/fixture?case='+outcome});await wait(`!!window.i18n && !!document.querySelector('button')`)
   await ev(`i18n.changeLanguage('${language}')`);await sleep(75)
   await ev(`document.querySelector('button').click()`);await wait(`!!document.querySelector('[role="dialog"] article button')`)
   await ev(`document.querySelector('article button').click()`);await wait(`window.reconciled && !document.querySelector('article button').disabled`)
   await sleep(100)
   const terminal=['rejected','completed','cancelled'].includes(outcome)
   const text=await ev(`document.querySelector('article').textContent`)
   assert.equal(await ev(`!!document.querySelector('[role="alert"]')`),!terminal)
   if(terminal){const expected=language==='en'?{rejected:'Operation rejected',completed:'Operation completed',cancelled:'Confirmation cancelled'}:{rejected:'Operação rejeitada',completed:'Operação concluída',cancelled:'Confirmação cancelada'};assert(text.includes(expected[outcome]));assert(text.includes(language==='en'?'Result reviewed':'Resultado conferido'))}
   const calls=await ev('window.calls');assert.equal(calls.filter(c=>c.method==='POST').length,1);assert(calls.filter(c=>c.method==='POST').every(c=>c.path.endsWith('/reconcile')))
   assert.equal(calls.filter(c=>c.path.includes('/financial/intents/')&&c.method==='GET').length,2)
  }
  for(const [code,key] of [['app_catalog_mismatch','catalogMismatch'],['lnbits_credential_enrollment_missing','lnbitsCredentialNotEnrolled'],['lnbits_credential_lnd_unavailable','lnbitsCredentialLndUnavailable']]){
   await page.call('Page.navigate',{url:base+'/fixture?case=app-'+code});await wait(`!!window.i18n && !!document.querySelector('h3')`)
   await ev(`i18n.changeLanguage('${language}')`);await sleep(75)
   await ev(`[...document.querySelectorAll('button')].find(x=>x.textContent.trim()===i18n.t('common.start')).click()`)
   await wait(`document.body.textContent.includes(i18n.t('appStore.${key}'))`)
   assert(!await ev(`document.body.textContent.includes('Synthetic broker refusal')`))
   assert(await ev(`document.documentElement.scrollWidth<=innerWidth`))
   if(language==='pt-BR'&&width===390&&code==='lnbits_credential_enrollment_missing'){
    const png=await page.call('Page.captureScreenshot',{format:'png'});await writeFile(join(tmpdir(),'los-0556-enrollment-ui.png'),Buffer.from(png.data,'base64'))
   }
  }
 }
 console.log('PASS: PT/EN desktop/mobile terminal outcome, uncertain/error retention, one reconcile/no financial resubmission, translated app diagnostics.')
}finally{page?.ws.close();if(browser){try{await browser.call('Browser.close')}catch{}browser.ws.close()}else child?.kill();await server.close()}
