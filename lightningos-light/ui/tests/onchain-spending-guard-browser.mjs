// Run against a loopback Vite dev server. Uses a separate headless browser
// profile and fake API only; never connects to a real node or wallet.
// CHROMIUM_BINARY=/path/to/chromium node tests/onchain-spending-guard-browser.mjs
import { spawn } from 'node:child_process'
import { mkdtemp, readFile, writeFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import assert from 'node:assert/strict'

const origin=process.env.GUARD_TEST_ORIGIN || 'http://127.0.0.1:15352'
assert.equal(new URL(origin).hostname,'127.0.0.1','Only a loopback fixture server is allowed')
const binary=process.env.CHROMIUM_BINARY || (process.platform==='win32'?'C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe':'chromium')
const profile=await mkdtemp(join(tmpdir(),'los0552-guard-browser-'))
const child=spawn(binary,['--headless=new','--disable-gpu','--no-first-run','--no-default-browser-check','--remote-debugging-port=0',`--user-data-dir=${profile}`],{stdio:'ignore',windowsHide:true})
child.on('error',error=>{ console.error(error.message); process.exitCode=1 })
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms))
let socket
let browserSocket
async function connect(url) {
  const ws=new WebSocket(url)
  await new Promise((resolve,reject)=>{ws.addEventListener('open',resolve,{once:true});ws.addEventListener('error',reject,{once:true})})
  let next=0;const pending=new Map()
  ws.addEventListener('message',event=>{const message=JSON.parse(event.data);const item=pending.get(message.id);if(item){pending.delete(message.id);message.error?item.reject(new Error(message.error.message)):item.resolve(message.result)}})
  return {ws,call:(method,params={})=>new Promise((resolve,reject)=>{const id=++next;pending.set(id,{resolve,reject});ws.send(JSON.stringify({id,method,params}))})}
}
try {
  let active
  for(let i=0;i<100;i++){try{active=(await readFile(join(profile,'DevToolsActivePort'),'utf8')).trim().split('\n');break}catch{await sleep(200)}}
  assert(active,'Headless browser did not become ready')
  browserSocket=await connect(`ws://127.0.0.1:${active[0]}${active[1]}`)
  const target=await(await fetch(`http://127.0.0.1:${active[0]}/json/new?${encodeURIComponent(origin+'/tests/onchain-spending-guard.html')}`,{method:'PUT'})).json()
  socket=await connect(target.webSocketDebuggerUrl)
  const evaluate=async expression=>{const result=await socket.call('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true,userGesture:true});if(result.exceptionDetails)throw new Error(result.exceptionDetails.text+' '+result.exceptionDetails.exception?.description);return result.result.value}
  const wait=async expression=>{for(let i=0;i<80;i++){if(await evaluate(expression))return;await sleep(100)}throw new Error('Timed out: '+expression)}
  await wait(`document.querySelector('button[aria-expanded]') && document.body.innerText.includes('DISABLED')`)
  await evaluate(`document.querySelector('button[aria-expanded]').click()`)
  await wait(`document.querySelectorAll('input[type=number]').length===2`)
  const setInput=async(index,value)=>{
    await evaluate(`(()=>{const input=document.querySelectorAll('input[type=number]')[${index}];input.focus();input.select()})()`)
    await socket.call('Input.insertText',{text:String(value)})
  }
  await evaluate(`document.querySelector('input[type=checkbox]').click()`)
  await setInput(0,1000);await setInput(1,5000)
  await evaluate(`document.querySelector('button.btn-primary').click()`)
  await wait(`window.guardFixture.writes.length===1 && document.body.innerText.includes('updated.')`)
  assert.deepEqual(await evaluate('window.guardFixture.writes[0]'),{enabled:true,max_send_sat:1000,rolling_24h_limit_sat:5000})
  await setInput(0,750)
  await evaluate(`window.guardFixture.status={...window.guardFixture.status,used_sat:200,reserved_sat:100,remaining_sat:4700};window.guardFixture.poll()`)
  await wait(`document.body.innerText.includes('200 sats')`)
  assert.equal(await evaluate(`document.querySelector('input[type=number]').value`),'750','Polling overwrote draft')
  await evaluate(`window.guardFixture.language('pt-BR')`)
  await wait(`document.body.innerText.includes('Proteção de Gastos On-chain')`)
  assert.equal(await evaluate(`document.querySelector('input[type=number]').value`),'750','Locale refresh overwrote draft')
  await setInput(0,-1)
  await evaluate(`document.querySelector('button.btn-primary').click()`)
  await wait(`document.body.innerText.includes('limites inteiros')`)
  assert.equal(await evaluate('window.guardFixture.writes.length'),1,'Invalid cap reached API')
  await evaluate(`window.guardFixture.fail=true;window.guardFixture.poll()`)
  await wait(`document.body.innerText.includes('NÃO VERIFICADO')`)
  assert.equal(await evaluate(`document.querySelector('button.btn-primary').disabled`),true)
  await socket.call('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:true})
  assert.equal(await evaluate('document.documentElement.scrollWidth<=window.innerWidth'),true,'Mobile horizontal overflow')
  const screenshot=await socket.call('Page.captureScreenshot',{format:'png',captureBeyondViewport:true})
  await writeFile(join(profile,'onchain-guard-pt.png'),Buffer.from(screenshot.data,'base64'))
  console.log(JSON.stringify({passed:['collapsed/default disabled','enable/save exact policy','polling preserves draft','Portuguese locale','invalid limit rejected','unavailable is not disabled','mobile layout'],screenshot:join(profile,'onchain-guard-pt.png')}))
} finally {
  socket?.ws.close()
  if(browserSocket){try{await browserSocket.call('Browser.close')}catch{}browserSocket.ws.close()}else child.kill()
}
