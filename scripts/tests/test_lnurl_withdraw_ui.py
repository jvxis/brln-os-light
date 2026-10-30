#!/usr/bin/env python3
"""Browser regression for the full Wallet using a synthetic LNURL provider.

Install Python Playwright/Chromium, start Vite at 127.0.0.1:5179, then run this
file. LOS_TEST_ARTIFACTS selects the screenshot directory. No live node or real
voucher is contacted. The separate opt-in Go test covers native regtest LND.
"""
from pathlib import Path
import json
from playwright.sync_api import sync_playwright, expect

ROOT=Path(__file__).resolve().parents[2]/'lightningos-light/ui'
import os
ART=Path(os.environ.get('LOS_TEST_ARTIFACTS', '/tmp/los-lnurl-ui'))
ART.mkdir(parents=True,exist_ok=True)
HTML='''<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"></head>
<body style="margin:0;background:#09131b;color:white"><div id="root" style="padding:16px;max-width:1200px;margin:auto"></div>
<script type="module">
import RefreshRuntime from '/@react-refresh';RefreshRuntime.injectIntoGlobalHook(window);window.$RefreshReg$=()=>{};window.$RefreshSig$=()=>type=>type;window.__vite_plugin_react_preamble_installed__=true;
import React from '/node_modules/.vite/deps/react.js';
import ReactDOM from '/node_modules/.vite/deps/react-dom_client.js';
const {default:Wallet}=await import('/src/pages/Wallet.tsx');import '/src/i18n/index.ts';import '/src/styles/main.css';
ReactDOM.createRoot(document.getElementById('root')).render(React.createElement(Wallet));
</script></body></html>'''

with sync_playwright() as p:
 browser=p.chromium.launch()
 for lang in ['en','pt-BR']:
  tr=json.loads((ROOT/'src/i18n'/f'{lang}.json').read_text(encoding='utf-8'))
  words=tr['wallet']['lnurlWithdraw']
  context=browser.new_context(viewport={'width':1280,'height':1000})
  context.add_init_script('localStorage.setItem("los-lang",'+json.dumps(lang)+')')
  page=context.new_page();errors=[];page.on('pageerror',lambda e:(errors.append(str(e)),print('BROWSER ERROR',str(e))))
  page.route('**/__lnurl-test',lambda r:r.fulfill(content_type='text/html',body=HTML))
  state={'mode':'unknown','preview_error':False};redeems=[];invoices=[]
  def api(route):
   req=route.request;path=req.url.split('/api/',1)[1].split('?')[0];code=200
   data={}
   if path=='wallet/lnurl-withdraw/preview':
    if state['preview_error']:code=400;data={'error':'lnurl_withdraw_wrong_type'}
    else:data={'token':'a'*64,'service':'voucher.example','callback_service':'pay.example','description':'Test <img src=x onerror=alert(1)>','min_sat':2,'max_sat':100,'expires_at':'2099-01-01T00:00:00Z'}
   elif path=='wallet/lnurl-withdraw':
    redeems.append(req.post_data_json);data={'status':state['mode'],'payment_hash':'ab'*32,'amount_sat':2}
   elif path=='wallet/invoice':invoices.append(req.post_data_json);data={'payment_request':'lnbc-test-existing-receive-flow'}
   elif path=='wallet/summary':data=None
   elif path=='wallet/activity':data={'items':[],'has_more':False}
   elif path=='lnops/channels':data={'channels':[]}
   elif path=='wallet/spending-guard':data={'enabled':False,'max_payment_sat':0,'rolling_24h_limit_sat':0}
   elif path=='mempool/fees':data={'fastestFee':1,'halfHourFee':1,'hourFee':1,'economyFee':1,'minimumFee':1}
   elif path=='auth/state':data={'enabled':True,'authenticated':True,'csrf_token':'test-csrf'}
   route.fulfill(status=code,content_type='application/json',body=json.dumps(data))
  page.route('**/api/**',api)
  page.goto('http://127.0.0.1:5179/__lnurl-test')
  expect(page.get_by_role('heading',name=words['title'],exact=True)).to_be_visible()
  # Existing receive form remains usable beside the new feature.
  page.get_by_placeholder(tr['wallet']['amountSats'],exact=True).first.fill('2')
  page.get_by_role('button',name=tr['wallet']['generateInvoice'],exact=True).click()
  expect(page.get_by_text('lnbc-test-existing-receive-flow',exact=True)).to_be_visible()
  assert len(invoices)==1 and invoices[0]['amount_sat']==2
  page.locator('#lnurl-withdraw-input').fill('LNURL1-test-private-voucher')
  page.get_by_role('button',name=words['inspect'],exact=True).click()
  expect(page.get_by_text('voucher.example',exact=True)).to_be_visible()
  expect(page.get_by_text('pay.example',exact=True)).to_be_visible()
  assert page.locator('img[onerror]').count()==0
  amount=page.locator('#lnurl-withdraw-amount')
  amount.fill('1');expect(page.get_by_role('button',name=words['confirm'],exact=True)).to_be_disabled()
  amount.fill('2.5');expect(page.get_by_role('button',name=words['confirm'],exact=True)).to_be_disabled()
  amount.fill('2');page.get_by_role('button',name=words['confirm'],exact=True).click()
  expect(page.get_by_text(words['unknown'],exact=True)).to_be_visible()
  expect(amount).to_be_disabled()
  state['mode']='accepted'
  page.get_by_role('button',name=words['retry'],exact=True).click()
  expect(page.get_by_text(words['accepted'],exact=True)).to_be_visible()
  assert len(redeems)==2 and redeems[0]==redeems[1]=={'token':'a'*64,'amount_sat':2}
  expect(page.get_by_role('button',name=words['retry'],exact=True)).to_have_count(0)
  # Inspect the actual card on desktop and mobile, including both hostnames.
  card=page.get_by_role('heading',name=words['title'],exact=True).locator('..')
  card.screenshot(path=str(ART/f'wallet-{lang}-desktop.png'))
  page.set_viewport_size({'width':390,'height':844})
  card.scroll_into_view_if_needed();card.screenshot(path=str(ART/f'wallet-{lang}-mobile.png'))
  assert page.evaluate('document.documentElement.scrollWidth <= innerWidth + 1')
  card.get_by_role('button',name=tr['common']['close'],exact=True).click()
  state['preview_error']=True
  page.locator('#lnurl-withdraw-input').fill('LNURL1-wrong-type')
  page.get_by_role('button',name=words['inspect'],exact=True).click()
  expect(page.get_by_role('alert')).to_have_text(words['errors']['lnurl_withdraw_wrong_type'])
  assert not errors, errors
  print('PASS',lang,': full Wallet receive form, preview, escaped description, bounds, uncertain retry, accepted wording, wrong type, desktop/mobile')
  context.close()
 browser.close()
