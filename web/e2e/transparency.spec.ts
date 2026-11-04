import { expect, test } from '@playwright/test';
import { apiAs } from './helpers';

test('audit chain verifies, fees and recent events render', async ({ page, baseURL }) => {
  // Make sure the chain has at least one event of this run (auth.dev_login).
  const api = await apiAs(baseURL!, 'carol');
  await api.dispose();

  const verify = await page.request.get('/api/transparency/audit/verify');
  expect(verify.ok()).toBeTruthy();
  const v = (await verify.json()) as { ok: boolean; length: number; head_hash: string };
  expect(v.ok).toBe(true);
  expect(v.length).toBeGreaterThan(0);

  await page.goto('/#/transparency');
  await expect(page.getByRole('heading', { level: 1, name: 'Transparency' })).toBeVisible();

  const audit = page.getByRole('region', { name: 'Audit chain' });
  await expect(audit.getByText(/^OK — every audit event links/)).toBeVisible();
  const length = audit.getByRole('definition').first();
  await expect(length).toHaveText(/^[1-9][\d,.\s]*$/);

  const fees = page.getByRole('region', { name: 'Published fee logic' });
  const rules = fees.getByRole('table', { name: 'Rules as enforced by the code' });
  await expect(rules.locator('tbody tr').first()).toBeVisible();
  await expect(fees.getByText('None (0 bp)')).toBeVisible();

  // The public feed shows only the event class, the hour and a hash prefix;
  // nobody's id or activity is published.
  const events = page.getByRole('region', { name: 'Recent audit events' });
  const table = events.getByRole('table', { name: 'Most recent audit events' });
  await expect(table.locator('tbody tr').first()).toBeVisible();
  await expect(table.getByRole('cell', { name: 'auth', exact: true }).first()).toBeVisible();
  await expect(table.getByRole('columnheader')).toHaveText(['#', 'Hour', 'Kind', 'Hash']);
  const recent = (await (await page.request.get('/api/transparency/audit/recent')).json()) as { events: Record<string, unknown>[] };
  for (const ev of recent.events) expect(Object.keys(ev).sort()).toEqual(['action', 'at', 'hash_prefix', 'seq']);

  // Today's root is provisional and never signed.
  const today = new Date().toISOString().slice(0, 10);
  const root = (await (await page.request.get(`/api/transparency/audit/root?day=${today}`)).json()) as Record<string, unknown>;
  expect(root).toMatchObject({ day: today, provisional: true });
  expect(root).not.toHaveProperty('signature');
});
