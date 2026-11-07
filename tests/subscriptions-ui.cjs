// Run with playwright-core available (NODE_PATH may point to an external install).
// Optional: CHROMIUM_PATH=/path/to/chrome node tests/subscriptions-ui.cjs
const { chromium } = require('playwright-core');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

(async () => {
    const browser = await chromium.launch({ headless: true,
        ...(process.env.CHROMIUM_PATH ? { executablePath: process.env.CHROMIUM_PATH } : {}),
        args: ['--no-sandbox'] });
    try {
        for (const width of [1200, 390]) {
            const page = await browser.newPage({ viewport: { width, height: 844 } });
            const errors = [];
            page.on('pageerror', error => errors.push(error.message));
            page.on('dialog', dialog => dialog.accept());
            await page.context().grantPermissions(['clipboard-read', 'clipboard-write']);
            let profiles = [], nextID = 1, failSave = false;
            const nodes = [
                { key:'node:a', name:'分享节点 A', source:'自建节点' },
                { key:'custom:b', name:'私有节点 B', source:'自定义节点' },
                { key:'airport:c', name:'<img src=x onerror=alert(1)>', source:'机场节点' }
            ];
            const source = fs.readFileSync(path.join(__dirname, '../templates/components/sub_links_modal.html'), 'utf8')
                .replace('{{define "sub_links_modal.html"}}', '').replace('{{end}}', '')
                .replace(/<script src="[^"]+"><\/script>/g, '');
            const document = `<html><head><meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1"><style>*{box-sizing:border-box}body{margin:0;font-family:sans-serif}</style></head><body><button id="open" onclick="openSubLinksModal()">订阅分发</button>${source}</body></html>`;
            await page.route('https://nodectl.test/**', async route => {
                const req = route.request(), url = new URL(req.url());
                if (url.pathname === '/') return route.fulfill({ contentType:'text/html', body:document });
                let result, status = 200;
                if (url.pathname === '/api/get-settings') result = { data:{ sub_token:'legacy', sub_custom_name:'原有订阅' } };
                else if (url.pathname === '/api/subscriptions/reset-token') {
                    const profile = profiles.find(p => p.id === req.postDataJSON().id);
                    profile.token += '-rotated'; result = { token:profile.token };
                } else if (url.pathname === '/api/subscriptions') {
                    if (req.method() === 'GET') result = { data:profiles, landing_nodes:nodes };
                    else if (req.method() === 'DELETE') {
                        profiles = profiles.filter(p => p.id !== url.searchParams.get('id')); result = {};
                    } else if (failSave) { status = 500; result = { status:'error', message:'模拟保存失败' }; }
                    else {
                        const data = req.postDataJSON();
                        if (req.method() === 'POST') {
                            data.id = String(nextID++); data.token = 'token-' + data.id; profiles.push(data);
                        } else {
                            const index = profiles.findIndex(p => p.id === data.id);
                            profiles[index] = { ...profiles[index], ...data };
                        }
                        result = { data:profiles.find(p => p.id === data.id) };
                    }
                } else result = {};
                return route.fulfill({ status, contentType:'application/json', body:JSON.stringify({ status:'success', ...result }) });
            });
            await page.goto('https://nodectl.test/');
            await page.click('#open');
            await page.waitForFunction(() => !document.getElementById('subProfileEditor').disabled);
            assert.match(await page.locator('#currentSubLinkText').inputValue(), /token=legacy/);
            await page.getByRole('button', {name:'＋ 新建独立订阅'}).click();
            await page.fill('#inputSubName', '分享 A');
            assert.equal(await page.locator('#subLandingNodes input:checked').count(), 0);
            assert.equal(await page.locator('#subLandingNodes img').count(), 0, 'node names must be text');
            await page.locator('#subLandingNodes input').nth(0).check();
            await page.click('#subProfileSave');
            await page.waitForFunction(() => document.getElementById('subProfileSelect').value === '1');
            const aLink = await page.locator('#currentSubLinkText').inputValue();
            assert.match(aLink, /token=token-1/);
            await page.click('#btnCopySubLink');
            assert.equal(await page.evaluate(() => navigator.clipboard.readText()), aLink);
            assert.deepEqual(profiles[0].landing_node_keys, ['node:a']);
            await page.getByRole('button', {name:'＋ 新建独立订阅'}).click();
            await page.fill('#inputSubName', '自用 B');
            await page.selectOption('#subLandingMode', 'all');
            await page.click('#subProfileSave');
            await page.waitForFunction(() => document.getElementById('subProfileSelect').value === '2');
            assert.equal(profiles[1].all_landing_nodes, true);
            assert.notEqual(await page.locator('#currentSubLinkText').inputValue(), aLink);
            await page.selectOption('#subProfileSelect', '1');
            assert.equal(await page.locator('#subLandingNodes input:checked').count(), 1);
            await page.fill('#subLandingSearch', '私有');
            await page.getByRole('button', {name:'全选搜索结果'}).click();
            await page.fill('#subLandingSearch', '');
            assert.equal(await page.locator('#subLandingNodes input:checked').count(), 2);
            failSave = true;
            await page.click('#subProfileSave');
            await page.waitForFunction(() => document.getElementById('subProfileStatus').textContent.includes('模拟保存失败'));
            assert.deepEqual(profiles[0].landing_node_keys, ['node:a']);
            assert.equal(await page.locator('#subLandingNodes input:checked').count(), 2, 'failed save preserves draft');
            failSave = false;
            await page.click('#subProfileSave');
            await page.waitForFunction(() => document.getElementById('subProfileStatus').textContent.includes('已保存'));
            assert.equal(profiles[0].landing_node_keys.length, 2);
            // Reopen fetches persisted data and restores the selected profile.
            await page.getByRole('button', {name:'关闭',exact:true}).click();
            await page.click('#open');
            await page.waitForFunction(() => !document.getElementById('subProfileEditor').disabled);
            assert.equal(await page.locator('#subProfileSelect').inputValue(), '1');
            assert.equal(await page.locator('#subLandingNodes input:checked').count(), 2);
            // Reset must update both the displayed link and the one-click import URL.
            await page.getByRole('button', {name:'重置链接'}).click();
            await page.waitForFunction(() => document.getElementById('currentSubLinkText')?.value.includes('rotated'));
            assert.match(await page.locator('#subClashImport').getAttribute('href'), /rotated/);
            await page.locator('#wrap_v2ray .sub-format-card').click();
            assert.match(await page.locator('#currentSubLinkText').inputValue(), /\/sub\/v2ray\?token=token-1-rotated/);
            await page.uncheck('#subProfileEnabled');
            await page.click('#subProfileSave');
            await page.waitForFunction(() => document.getElementById('subRightContent').textContent.includes('启用'));
            assert.equal(profiles[0].enabled, false);
            assert.equal(await page.locator('#currentSubLinkText').count(), 0);
            await page.check('#subProfileEnabled');
            await page.click('#subProfileSave');
            await page.waitForSelector('#currentSubLinkText');
            // Empty selections are explicit, and must survive save/reload.
            await page.getByRole('button', {name:'清空搜索结果'}).click();
            await page.click('#subProfileSave');
            await page.waitForFunction(() => document.getElementById('subProfileStatus').textContent.includes('已保存'));
            assert.deepEqual(profiles[0].landing_node_keys, []);
            assert.equal(profiles[0].all_landing_nodes, false);
            assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'no horizontal viewport overflow');
            await page.screenshot({ path:`/tmp/nodectl-subscriptions-${width}.png`, fullPage:true });
            await page.getByRole('button', {name:'删除订阅',exact:true}).click();
            await page.waitForFunction(() => document.getElementById('subProfileSelect').value === '2');
            assert.equal(profiles.length, 1);
            assert.equal(profiles[0].name, '自用 B');
            assert.deepEqual(errors, []);
            await page.close();
            console.log(`Subscription UI passed at ${width}px`);
        }
    } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
