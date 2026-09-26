// Browser regression check against the loopback-only TestBrowserPreview fixture.
// Wallet/poll responses are intercepted; this does not test a native wallet.
// Run VERIFYLINK_BROWSER_FIXTURE=1 go test ./internal/server -run '^TestBrowserPreview$' -timeout 10m
// Then PLAYWRIGHT_PATH=<playwright module> CHROME_PATH=<optional executable> node scripts/check-handoff.cjs
const {chromium} = require(process.env.PLAYWRIGHT_PATH || 'playwright');
const assert = require('node:assert/strict');
const base = 'http://127.0.0.1:18092';
const iphone = 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Mobile/15E148 Safari/604.1';
(async () => {
  const browser = await chromium.launch({executablePath: process.env.CHROME_PATH || undefined, headless: true});
  try {
    const operator = await browser.newContext({httpCredentials: {username: 'agent@example.test', password: 'local-test-password'}});
    const cases = [
      {name: 'iPhone QR and explicit launch', mobile: true, scheme: 'haip-vp'},
      {name: 'desktop QR', mobile: false, scheme: 'haip-vp'},
      {name: 'OpenID4VP URI unchanged', mobile: true, scheme: 'openid4vp'},
      {name: 'missing QR library', mobile: true, scheme: 'haip-vp', qrFailure: 'missing'},
      {name: 'QR rendering failure', mobile: true, scheme: 'haip-vp', qrFailure: 'throw'},
      {name: 'unsafe scheme rejected', mobile: true, scheme: 'javascript'}
    ];
    for (const test of cases) {
      const create = await operator.request.post(base + '/api/sessions', {
        headers: {Origin: base}, data: {channel: 'copylink', recipient: 'Synthetic handoff regression', preset: 'confirm-name'}
      });
      assert.equal(create.status(), 201);
      const {link} = await create.json();
      const context = await browser.newContext({viewport: {width: test.mobile ? 390 : 1280, height: 900}, isMobile: test.mobile, hasTouch: test.mobile, ...(test.mobile ? {userAgent: iphone} : {})});
      const page = await context.newPage();
      page.setDefaultTimeout(8000);
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));
      let state = 'pending', starts = 0;
      const uri = test.scheme + '://?client_id=test&request_uri=https%3A%2F%2Fexample.test%2Frequest';
      await page.route('**/v/*/start', route => {
        starts++;
        return route.fulfill({json: {authorization_request_uri: uri, poll_key: 'test-poll', same_device: test.mobile}});
      });
      await page.route('**/s/*', route => route.fulfill({json: {status: state}}));
      if (test.qrFailure) await page.route('**/assets/qrcode.min.js', route => route.abort());
      await page.goto(link);
      if (test.qrFailure === 'throw') await page.evaluate(() => {
        window.QRCode = function () { throw Error('Synthetic renderer failure'); };
        window.QRCode.CorrectLevel = {M: 0};
      });
      await page.locator('#verify').click();
      if (test.scheme === 'javascript') {
        await page.getByText('Wallet link unavailable. Ask for a new link.', {exact: true}).waitFor();
        assert.equal(await page.locator('#wallet-actions').isVisible(), false);
        assert.equal(await page.locator('#open-wallet').getAttribute('href'), null);
      } else {
        await page.locator('#open-wallet[href]').waitFor();
        assert.equal(await page.locator('#open-wallet').getAttribute('href'), uri);
        assert.equal(page.url(), link, 'request preparation must not navigate to a native app automatically');
        assert.equal(await page.locator('#qr-help').isVisible(), true);
        if (test.qrFailure) {
          assert.match(await page.locator('#qr-help').textContent(), /QR code could not load/);
        } else {
          await page.locator('#qr canvas').waitFor({state: 'attached'});
          assert.equal(await page.locator('#qr').getAttribute('title'), uri);
        }
        assert(await page.locator('body').evaluate(el => el.scrollWidth <= innerWidth));
        if (test.name === 'iPhone QR and explicit launch' && process.env.HANDOFF_SCREENSHOT) {
          await page.screenshot({path: process.env.HANDOFF_SCREENSHOT, fullPage: true});
        }
        state = 'declined';
        await page.getByText('You declined to share. You can go back to your chat.', {exact: true}).waitFor();
        assert.equal(await page.locator('#wallet-actions').isVisible(), false);
        assert.equal(await page.locator('#qr').evaluate(el => el.childElementCount), 0);
        assert.equal(await page.locator('#open-wallet').getAttribute('href'), null);
      }
      assert.equal(starts, 1);
      assert.deepEqual(errors, []);
      await context.close();
      console.log('PASS: ' + test.name);
    }
    await operator.close();
  } finally {
    await browser.close();
  }
})().catch(e => { console.error(e); process.exitCode = 1; });
