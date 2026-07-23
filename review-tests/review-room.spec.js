// @ts-check
const { test, expect } = require('@playwright/test');

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
