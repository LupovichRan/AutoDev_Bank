import { expect, test, type Locator } from '@playwright/test';

async function expectRows(table: Locator) {
  await expect(table).toBeVisible();
  await expect(table.locator('tbody tr').first()).toBeVisible();
  expect(await table.locator('tbody tr').count()).toBeGreaterThan(0);
}

test.beforeEach(async ({ page }) => {
  await page.goto('/#/open-finance');
  await expect(page.getByRole('heading', { level: 1, name: 'Open Finance' })).toBeVisible();
});

test('FX reference rates with a live-data badge', async ({ page }) => {
  const fx = page.getByRole('region', { name: 'Exchange rates' });
  await expectRows(fx.getByRole('table', { name: /Euro reference rates/ }));

  const fetched = fx.locator('time').filter({ hasText: /^fetched / }).first();
  await expect(fetched).toBeVisible();
  const at = await fetched.getAttribute('datetime');
  expect(Number.isFinite(Date.parse(at ?? ''))).toBe(true);
  const source = fx.getByRole('link', { name: /^Source for / }).first();
  await expect(source).toBeVisible();
  expect(await source.getAttribute('href')).toMatch(/^https:\/\//);

  // The converter answers from the same rates.
  await fx.getByLabel('Amount').fill('100');
  await expect(fx.getByText(/^≈/)).toBeVisible();
});

test('central bank policy rates', async ({ page }) => {
  const rates = page.getByRole('region', { name: 'Central bank policy rates' });
  await expectRows(rates.getByRole('table', { name: 'Policy rates by central bank' }));
});

test('UK current account products', async ({ page }) => {
  const products = page.getByRole('region', { name: 'UK current accounts compared' });
  await expectRows(products.getByRole('table', { name: /Open Banking UK open data/ }));
});

test('nearby ATMs for manually entered coordinates', async ({ page }) => {
  const nearby = page.getByRole('region', { name: 'Nearby ATMs and branches' });
  await nearby.getByLabel('ATMs').check();
  await nearby.getByLabel('Near (latitude,longitude)').fill('51.5138,-0.0984');
  await nearby.getByRole('button', { name: 'Search' }).click();
  const places = nearby.getByRole('list').filter({ has: page.getByRole('link', { name: /^Map/ }) });
  await expect(places.getByRole('listitem').first()).toBeVisible();
  expect(await places.getByRole('listitem').count()).toBeGreaterThan(0);
  await expect(places.getByText(/\d km$/).first()).toBeVisible();

  const api = await page.request.get('/api/opendata/atms?near=51.5138,-0.0984&limit=5');
  expect(api.ok()).toBeTruthy();
  expect(((await api.json()) as { records: unknown[] }).records.length).toBeGreaterThan(0);
});
