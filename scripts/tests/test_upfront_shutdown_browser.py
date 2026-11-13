"""Actual React card; API responses mocked, no node mutations. Start Vite first."""
import argparse
import json
from playwright.sync_api import sync_playwright, expect

p = argparse.ArgumentParser()
p.add_argument('--url', default='http://127.0.0.1:5173/tests/upfront-shutdown.html')
args = p.parse_args()
with sync_playwright() as pw:
    browser = pw.chromium.launch(channel='msedge', headless=True)
    for lang in ('en', 'pt-BR'):
        for width in (390, 1280):
            for editable in (True, False):
                page = browser.new_page(viewport={'width': width, 'height': 900})
                page.add_init_script(f"localStorage.setItem('los-lang', {json.dumps(lang)})")
                calls = []
                def api(route):
                    assert route.request.method == 'GET', 'unexpected mutation'
                    calls.append(route.request.url)
                    route.fulfill(json={'current': {'mode': 'off', 'address': ''}, 'editable': editable,
                                        'origin': 'native' if editable else 'unknown',
                                        'reason': '' if editable else 'installation_origin_unverified',
                                        'config_revision': 'fixture-revision'})
                page.route('**/api/lnd/config/upfront-shutdown', api)
                page.goto(args.url)
                expect(page.locator('summary')).to_be_visible()
                expect(page.locator('#upfront-mode')).to_have_count(0)
                assert calls == [], 'collapsed card fetched unnecessarily'
                page.locator('summary').click()
                mode = page.locator('#upfront-mode')
                if not editable:
                    expect(mode).to_be_disabled()
                    expect(page.get_by_role('button').first).to_be_disabled()
                else:
                    mode.select_option('external')
                    address = page.locator('#upfront-address')
                    expect(address).to_be_visible()
                    save = page.get_by_role('button').first
                    expect(save).to_be_disabled()
                    address.fill('bc1qfixture')
                    expect(save).to_be_disabled()
                    page.get_by_role('checkbox').check()
                    expect(save).to_be_enabled()
                    address.fill('bc1qchanged')
                    expect(page.get_by_role('checkbox')).not_to_be_checked()
                    mode.select_option('wallet')
                    expect(address).to_have_count(0)
                    save.click()
                    saved = json.loads(page.locator('body').get_attribute('data-saved'))
                    assert saved == {'mode': 'wallet', 'address': '', 'config_revision': 'fixture-revision', 'confirm_external': False}
                assert page.evaluate('document.documentElement.scrollWidth <= window.innerWidth'), 'horizontal overflow'
                page.close()
    browser.close()
print('PASS: EN/PT, desktop/mobile, collapsed default, readonly origin, external confirmation, reset and minimal payload')
