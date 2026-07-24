// @ts-check
const { test, expect } = require('@playwright/test');
const http = require('http');

const LIVE_TARGET = 'http://127.0.0.1:4301';
let liveServer;

test.beforeAll(async () => {
  liveServer = http.createServer((req, res) => {
    if (req.url === '/app.js') {
      res.writeHead(200, { 'Content-Type': 'text/javascript' });
      res.end(`
        function render() {
          const app = document.getElementById('app');
          const route = location.pathname === '/rentals' ? 'Rentals' : 'Dashboard';
          app.innerHTML = '<h1>' + route + '</h1><p class="copy">Rendered route ' + location.pathname + '</p><button id="load-state">Load state</button><pre id="state"></pre>';
          document.getElementById('load-state').addEventListener('click', async () => {
            const response = await fetch('/api/state?route=' + encodeURIComponent(location.pathname));
            document.getElementById('state').textContent = await response.text();
          });
        }
        document.addEventListener('click', (event) => {
          const link = event.target.closest('a[data-link]');
          if (!link) return;
          event.preventDefault();
          history.pushState({}, '', link.getAttribute('href'));
          render();
        });
        addEventListener('popstate', render);
        render();
      `);
      return;
    }
    if (req.url && req.url.startsWith('/api/state')) {
      res.writeHead(200, { 'Content-Type': 'application/json' });
      res.end(JSON.stringify({ ok: true, url: req.url }));
      return;
    }
    res.writeHead(200, {
      'Content-Type': 'text/html; charset=utf-8',
      'Content-Security-Policy': "default-src 'self'; script-src 'self'; connect-src 'self'; style-src 'self' 'unsafe-inline'",
    });
    res.end(`<!doctype html>
      <html>
        <head><title>Fake SPA</title></head>
        <body>
          <nav><a href="/" data-link>Dashboard</a> <a href="/rentals" data-link>Rentals</a></nav>
          <main id="app"></main>
          <script src="/app.js"></script>
        </body>
      </html>`);
  });
  await new Promise((resolve) => liveServer.listen(4301, '127.0.0.1', resolve));
});

test.afterAll(async () => {
  if (!liveServer) return;
  await new Promise((resolve) => liveServer.close(resolve));
});

async function createReview(request, title = 'Flux rollout') {
  const response = await request.post('/api/sessions', {
    data: {
      title,
      markdown: '# Deploy safely\n\n1. Build an immutable image.\n2. Render through **Flux**.\n3. Verify the live service.',
    },
  });
  expect(response.status()).toBe(201);
  return response.json();
}

test('launcher creates a review room', async ({ page }) => {
  await page.goto('/');
  await expect(page.getByRole('heading', { name: /put the plan/i })).toBeVisible();
  await page.getByLabel('Title').fill('Cluster review');
  await page.getByLabel('Markdown').fill('# The plan\n\n- Make it observable\n- Reconcile with Flux');
  await page.getByRole('button', { name: /open review room/i }).click();
  const link = page.locator('#review-link');
  await expect(link).toBeVisible();
  await expect(link).toHaveAttribute('href', /\/r\/[A-Za-z0-9_-]+$/);
});

test('comments are sent back to the waiting agent', async ({ page, request }) => {
  const created = await createReview(request);
  await page.addInitScript(() => localStorage.setItem('an-author', 'Reviewer'));
  await page.goto(created.url);
  await expect(page.getByRole('heading', { name: 'Flux rollout' })).toBeVisible();
  await expect(page.locator('.markdown-body')).toContainText('Build an immutable image');

  await page.keyboard.press('p');
  await page.locator('.markdown-body h1').click();
  const composer = page.locator('#__an_compose');
  await composer.locator('textarea').fill('Verify this from another LAN client.');
  await composer.locator('.an-primary').click();
  await expect(page.locator('.an-pin')).toHaveCount(1);
  await page.locator('#__an_panel .an-x').click();

  await page.getByLabel(/overall note/i).fill('Show the exact Flux readiness check.');
  await page.getByRole('button', { name: 'Send comments to agent' }).click();
  await expect(page.locator('#room-status')).toContainText('COMMENTS SENT');
  await expect(page.getByText(/waiting agent will resume/i)).toBeVisible();

  const response = await request.get('/api/sessions/' + created.id);
  const session = await response.json();
  expect(session.status).toBe('changes_requested');
  expect(session.decision.summary).toBe('Show the exact Flux readiness check.');
  expect(session.decision.feedback).toHaveLength(1);
  expect(session.decision.feedback[0].text).toBe('Verify this from another LAN client.');
  expect(session.decision.feedback[0].author).toBe('Reviewer');
});

test('live SPA proxy collects comments across routes', async ({ page, request }) => {
  const response = await request.post('/api/site-sessions', {
    data: { title: 'Live AnyRent SPA', target: LIVE_TARGET },
  });
  expect(response.status()).toBe(201);
  const created = await response.json();

  await page.addInitScript(() => localStorage.setItem('an-author', 'Reviewer'));
  await page.goto(created.url);
  await page.waitForFunction(() => Boolean(window.Annotate && window.Annotate.allComments));
  await expect(page.getByRole('heading', { name: 'Dashboard' })).toBeVisible();

  await page.keyboard.press('p');
  await page.locator('#app h1').click();
  await page.locator('#__an_compose textarea').fill('Dashboard total is hard to scan.');
  await page.locator('#__an_compose .an-primary').click();
  await expect(page.locator('.an-pin')).toHaveCount(1);

  await page.getByRole('link', { name: 'Rentals' }).click();
  await expect(page.getByRole('heading', { name: 'Rentals' })).toBeVisible();
  await page.getByRole('button', { name: 'Load state' }).click();
  await expect(page.locator('#state')).toContainText('/api/state');

  await page.keyboard.press('p');
  await page.locator('#app h1').click();
  await page.locator('#__an_compose textarea').fill('Rentals route needs an empty state.');
  await page.locator('#__an_compose .an-primary').click();

  await page.locator('#__annotate_site_note').fill('Review covers more than one SPA route.');
  await page.locator('#__annotate_site_send').click();
  await expect(page.locator('#__annotate_site_result')).toContainText('Comments sent');

  const sessionResponse = await request.get('/api/sessions/' + created.id);
  const session = await sessionResponse.json();
  expect(session.status).toBe('changes_requested');
  expect(session.decision.summary).toBe('Review covers more than one SPA route.');
  expect(session.decision.feedback).toHaveLength(2);
  expect(session.decision.feedback.map((item) => item.text).sort()).toEqual([
    'Dashboard total is hard to scan.',
    'Rentals route needs an empty state.',
  ]);
  expect(session.decision.feedback.some((item) => item.page.endsWith('/rentals'))).toBe(true);
});

for (const viewport of [
  { width: 320, height: 568 },
  { width: 390, height: 844 },
]) {
  test(`live site handoff stays compact at ${viewport.width}px`, async ({ page, request }) => {
    const response = await request.post('/api/site-sessions', {
      data: { title: `Compact ${viewport.width}px handoff`, target: LIVE_TARGET },
    });
    expect(response.status()).toBe(201);
    const created = await response.json();

    await page.setViewportSize(viewport);
    await page.goto(created.url);
    await page.waitForFunction(() => Boolean(window.Annotate));

    const toggle = page.locator('#__annotate_site_toggle');
    const panel = page.locator('#__annotate_site_panel');
    await expect(toggle).toBeVisible();
    await expect(toggle).toHaveAttribute('aria-expanded', 'false');
    await expect(panel).toBeHidden();

    const collapsed = await toggle.evaluate((element) => {
      const box = element.getBoundingClientRect();
      const toolbar = document.querySelector('#__an_bar').getBoundingClientRect();
      return {
        width: box.width,
        height: box.height,
        aboveToolbar: box.bottom < toolbar.top,
        overflow: document.documentElement.scrollWidth > window.innerWidth,
      };
    });
    expect(collapsed.width).toBeLessThanOrEqual(170);
    expect(collapsed.height).toBeLessThanOrEqual(52);
    expect(collapsed.aboveToolbar).toBe(true);
    expect(collapsed.overflow).toBe(false);

    await toggle.click();
    await expect(panel).toBeVisible();
    await expect(toggle).toHaveAttribute('aria-expanded', 'true');
    await expect(page.getByLabel('Overall note')).toBeFocused();
    await expect(page.getByRole('button', { name: 'Send comments to agent' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Approve and continue' })).toBeVisible();

    const expanded = await panel.evaluate((element) => {
      const box = element.getBoundingClientRect();
      return {
        width: box.width,
        height: box.height,
        viewportWidth: window.innerWidth,
        viewportHeight: window.innerHeight,
      };
    });
    expect(expanded.width).toBeLessThanOrEqual(expanded.viewportWidth - 20);
    expect(expanded.height).toBeLessThanOrEqual(expanded.viewportHeight * 0.73);

    await page.getByRole('button', { name: 'Close review handoff' }).click();
    await expect(panel).toBeHidden();
    await expect(toggle).toBeFocused();
  });
}

test('desktop to mobile resize does not leave horizontal overflow', async ({ page, request }) => {
  const created = await createReview(request, 'Responsive review');
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto(created.url);
  await page.waitForFunction(() => Boolean(window.Annotate));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.waitForTimeout(300);

  const dimensions = await page.evaluate(() => {
    const overlay = document.getElementById('__an_overlay');
    return {
      viewport: window.innerWidth,
      scrollWidth: document.documentElement.scrollWidth,
      overlayWidth: overlay ? overlay.getBoundingClientRect().width : 0,
    };
  });
  expect(dimensions.scrollWidth).toBeLessThanOrEqual(dimensions.viewport);
  expect(dimensions.overlayWidth).toBeLessThanOrEqual(dimensions.viewport);
});

test('mobile keeps the send action ahead of the document and clear of the toolbar', async ({ page, request }) => {
  const created = await createReview(request, 'Mobile handoff');
  await page.setViewportSize({ width: 320, height: 568 });
  await page.goto(created.url);
  await page.waitForFunction(() => Boolean(window.Annotate));

  const layout = await page.evaluate(() => {
    const send = document.querySelector('#send-feedback').getBoundingClientRect();
    const documentPanel = document.querySelector('.document').getBoundingClientRect();
    const toolbar = document.querySelector('#__an_bar').getBoundingClientRect();
    return {
      sendBottom: send.bottom,
      documentTop: documentPanel.top,
      toolbarTop: toolbar.top,
      overflow: document.documentElement.scrollWidth > window.innerWidth,
    };
  });

  expect(layout.sendBottom).toBeLessThan(layout.documentTop);
  expect(layout.sendBottom).toBeLessThanOrEqual(layout.toolbarTop);
  expect(layout.overflow).toBe(false);
});

test('raw HTML in markdown is not executable', async ({ page, request }) => {
  const response = await request.post('/api/sessions', {
    data: {
      title: 'Safe markdown',
      markdown: '# Safe\n\n<script>window.__reviewXSS = true</script>',
    },
  });
  const created = await response.json();
  await page.goto(created.url);
  await expect(page.getByRole('heading', { name: 'Safe markdown' })).toBeVisible();
  expect(await page.evaluate(() => window.__reviewXSS)).toBeUndefined();
  await expect(page.locator('.markdown-body script')).toHaveCount(0);
});
