const { chromium } = require('playwright');
const fs = require('node:fs/promises');
const https = require('node:https');
const { pathToFileURL } = require('node:url');
const assert = require('node:assert/strict');
const root = require('node:path').resolve(__dirname, '..');
const tlsDir = process.env.LOS_TLS_FIXTURE_DIR;
if (!tlsDir) throw new Error('Set LOS_TLS_FIXTURE_DIR to a disposable certificate directory');
(async () => {
  const { createServer } = await import(pathToFileURL(root + '/node_modules/vite/dist/node/index.js'));
  const html = root + '/.qa-dependency.html', css = root + '/.qa-dependency.css';
  for (const file of [html, css]) await fs.access(file).then(() => { throw Error('fixture path exists'); }, () => {});
  let vite, browser, upstream;
  try {
    upstream = https.createServer({ key: await fs.readFile(tlsDir + '/fixture.key'), cert: await fs.readFile(tlsDir + '/fixture.crt') }, (req, res) => {
      res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ fixture: true, path: req.url }));
    });
    await new Promise(resolve => upstream.listen(0, '127.0.0.1', resolve));
    await fs.writeFile(css, '#marker { color: rgb(255, 0, 0); }');
    await fs.writeFile(html, `<!doctype html><html><body><div id="marker">HMR</div><div id="root"></div><img id="qr"><script type="module">
      import './.qa-dependency.css';
      import React from 'react'; import {createRoot} from 'react-dom/client';
      import ReactFlow, {Controls} from 'reactflow'; import 'reactflow/dist/style.css';
      import {LineChart,Line} from 'recharts';
      import QRCode from 'qrcode'; import QrScanner from 'qr-scanner';
      import i18n from '/src/i18n/index.ts';
      window.persistedMarker = 'alive';
      const h=React.createElement;
      createRoot(document.getElementById('root')).render(h('div',{},
        h(LineChart,{width:400,height:200,data:[{v:1},{v:4},{v:2}]},h(Line,{dataKey:'v',isAnimationActive:false})),
        h('div',{style:{width:500,height:300}},h(ReactFlow,{nodes:[{id:'1',position:{x:0,y:0},data:{label:'one'}},{id:'2',position:{x:100,y:100},data:{label:'two'}}],edges:[{id:'edge',source:'1',target:'2'}]},h(Controls)))));
      const img=document.getElementById('qr'); img.src=await QRCode.toDataURL('dependency-regression-fixture');
      await img.decode(); delete window.BarcodeDetector;
      window.qrResult=(await QrScanner.scanImage(img,{returnDetailedScanResult:true})).data;
      await i18n.changeLanguage('pt-BR'); window.translated=i18n.t('nav.wallet');
      window.proxyResult=await (await fetch('/api/dependency-fixture?check=1')).json();
    </script></body></html>`);
    vite = await createServer({ root, server: { host:'127.0.0.1',port:5179,strictPort:true,proxy:{'/api':{target:'https://127.0.0.1:'+upstream.address().port,changeOrigin:true,secure:false}}} });
    await vite.listen();
    browser = await chromium.launch({channel:process.env.LOS_BROWSER_CHANNEL || 'msedge',headless:true});
    const page=await browser.newPage(); const errors=[]; page.on('pageerror',e=>errors.push(e.message));
    await page.goto('http://127.0.0.1:5179/.qa-dependency.html');
    await page.waitForFunction(()=>window.qrResult && window.proxyResult, null, {timeout: 10000}).catch(e=>{console.log('page errors',errors);throw e});
    assert.equal(await page.evaluate(()=>window.qrResult),'dependency-regression-fixture');
    assert.equal(await page.evaluate(()=>window.translated),'Carteira');
    assert.deepEqual(await page.evaluate(()=>window.proxyResult),{fixture:true,path:'/api/dependency-fixture?check=1'});
    assert.equal(await page.locator('.react-flow__node').count(),2);
    assert.equal(await page.locator('.recharts-line-curve').count(),1);
    const initial=await page.locator('.react-flow__viewport').getAttribute('style');
    await page.locator('.react-flow__controls-zoomin').click();
    await page.waitForFunction(before=>document.querySelector('.react-flow__viewport').getAttribute('style')!==before,initial);
    await fs.writeFile(css,'#marker { color: rgb(0, 0, 255); }');
    await page.waitForFunction(()=>getComputedStyle(document.getElementById('marker')).color==='rgb(0, 0, 255)');
    assert.equal(await page.evaluate(()=>window.persistedMarker),'alive');
    assert.deepEqual(errors,[]);
    console.log(JSON.stringify({hmr:true,httpsAPIProxy:true,recharts:true,reactFlowZoom:true,qrWorkerRoundtrip:true,i18n:true,pageErrors:errors}));
  } finally {
    if(browser) await browser.close(); if(vite) await vite.close(); if(upstream) upstream.close();
    await fs.rm(html,{force:true}); await fs.rm(css,{force:true});
  }
})().catch(e=>{console.error(e);process.exitCode=1});
