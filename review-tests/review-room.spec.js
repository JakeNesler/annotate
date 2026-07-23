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

test('review decision returns structured feedback', async ({ page, request }) => {
  const created = await createReview(request);
  await page.goto(created.url);
  await expect(page.getByRole('heading', { name: 'Flux rollout' })).toBeVisible();
  await expect(page.locator('.markdown-body')).toContainText('Build an immutable image');
  await page.getByLabel(/decision note/i).fill('Show the exact Flux readiness check.');
  await page.getByRole('button', { name: 'Request changes' }).click();
  await expect(page.locator('#room-status')).toContainText('CHANGES REQUESTED');
  await expect(page.getByText(/waiting agent has the annotations/i)).toBeVisible();

  const response = await request.get('/api/sessions/' + created.id);
  const session = await response.json();
  expect(session.status).toBe('changes_requested');
  expect(session.decision.summary).toBe('Show the exact Flux readiness check.');
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
