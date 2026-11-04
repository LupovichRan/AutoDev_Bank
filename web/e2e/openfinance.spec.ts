import { expect, test, type Locator } from '@playwright/test';
import { escapeRe, L, startsWith } from './i18n';

async function expectRows(table: Locator) {
  await expect(table).toBeVisible();
  await expect(table.locator('tbody tr').first()).toBeVisible();
  expect(await table.locator('tbody tr').count()).toBeGreaterThan(0);
}

test.beforeEach(async ({ page }) => {
  await page.goto('/#/open-finance');
  await expect(page.getByRole('heading', { level: 1, name: L('title.openFinance') })).toBeVisible();
});

test('FX reference rates with a live-data badge', async ({ page }) => {
  const fx = page.getByRole('region', { name: L('fx.title') });
  await expectRows(fx.getByRole('table', { name: L('fx.eurCaption') }));

  const fetched = fx.locator('time').filter({ hasText: startsWith('live.fetched') }).first();
  await expect(fetched).toBeVisible();
  const at = await fetched.getAttribute('datetime');
  expect(Number.isFinite(Date.parse(at ?? ''))).toBe(true);
  const source = fx.getByRole('link', { name: new RegExp(`^${escapeRe(L('live.source'))}`) }).first();
  await expect(source).toBeVisible();
  expect(await source.getAttribute('href')).toMatch(/^https:\/\//);

  // The converter answers from the same rates.
  await fx.getByLabel(L('fx.amount'), { exact: true }).fill('100');
  await expect(fx.getByText(/^≈/)).toBeVisible();
});

test('central bank policy rates', async ({ page }) => {
  const rates = page.getByRole('region', { name: L('rates.title') });
  await expectRows(rates.getByRole('table', { name: L('rates.caption') }));
});

test('UK current account products', async ({ page }) => {
  const products = page.getByRole('region', { name: L('products.title') });
  await expectRows(products.getByRole('table', { name: /Open Banking UK/ }));
});

test('nearby ATMs for manually entered coordinates', async ({ page }) => {
  const nearby = page.getByRole('region', { name: L('nearby.title') });
  await nearby.getByLabel(L('nearby.atms'), { exact: true }).check();
  await nearby.getByLabel(L('nearby.near'), { exact: true }).fill('51.5138,-0.0984');
  await nearby.getByRole('button', { name: L('nearby.search') }).click();
  const places = nearby.getByRole('list').filter({ has: page.getByRole('link', { name: startsWith('nearby.map') }) });
  await expect(places.getByRole('listitem').first()).toBeVisible();
  expect(await places.getByRole('listitem').count()).toBeGreaterThan(0);
  await expect(places.getByText(new RegExp(`\\d ${escapeRe(L('nearby.km', { n: '' }).trim())}$`)).first()).toBeVisible();

  const api = await page.request.get('/api/opendata/atms?near=51.5138,-0.0984&limit=5');
  expect(api.ok()).toBeTruthy();
  expect(((await api.json()) as { records: unknown[] }).records.length).toBeGreaterThan(0);
});
