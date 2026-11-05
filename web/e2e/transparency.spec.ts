import { expect, test } from '@playwright/test';
import { apiAs } from './helpers';
import { HL, L } from './i18n';

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
  await expect(page.getByRole('heading', { level: 1, name: L('title.transparency') })).toBeVisible();

  const audit = page.getByRole('region', { name: L('tr.audit') });
  await expect(audit.getByText(L('tr.ok'), { exact: true })).toBeVisible();
  const length = audit.getByRole('definition').first();
  await expect(length).toHaveText(/^[1-9][\d,.\s]*$/);

  const fees = page.getByRole('region', { name: L('tr.fees') });
  const rules = fees.getByRole('table', { name: L('tr.rulesCaption') });
  await expect(rules.locator('tbody tr').first()).toBeVisible();
  await expect(fees.getByText(L('tr.noneBp'))).toBeVisible();
  // The rules are served in the UI language (Hebrew by default)…
  await expect(rules.getByRole('rowheader', { name: 'ללא עמלות', exact: true })).toBeVisible();
  await expect(rules.getByRole('rowheader', { name: 'הכול ניתן לביקורת', exact: true })).toBeVisible();
  const heFees = (await (await page.request.get('/api/transparency/fees?lang=he')).json()) as { lang: string };
  expect(heFees.lang).toBe('he');

  // The public feed shows only the event class, the hour and a hash prefix;
  // nobody's id or activity is published.
  const events = page.getByRole('region', { name: L('tr.recent') });
  const table = events.getByRole('table', { name: L('tr.caption') });
  await expect(table.locator('tbody tr').first()).toBeVisible();
  await expect(table.getByRole('cell', { name: HL('auditAction', 'auth'), exact: true }).first()).toBeVisible();
  await expect(table.getByRole('columnheader')).toHaveText(['#', L('tr.colHour'), L('tr.colKind'), L('tr.colHash')]);
  const recent = (await (await page.request.get('/api/transparency/audit/recent')).json()) as { events: Record<string, unknown>[] };
  for (const ev of recent.events) expect(Object.keys(ev).sort()).toEqual(['action', 'at', 'hash_prefix', 'seq']);

  // …and in English after the language switch.
  await page.getByRole('group', { name: L('lang.label') }).getByRole('button', { name: 'English' }).click();
  await expect(page.getByRole('rowheader', { name: 'No fees', exact: true })).toBeVisible();
  await expect(page.getByRole('cell', { name: 'sign-in', exact: true }).first()).toBeVisible();
  await page.getByRole('group', { name: 'Language' }).getByRole('button', { name: 'עברית' }).click();
  await expect(rules.getByRole('rowheader', { name: 'ללא עמלות', exact: true })).toBeVisible();

  // Today's root is provisional and never signed.
  const today = new Date().toISOString().slice(0, 10);
  const root = (await (await page.request.get(`/api/transparency/audit/root?day=${today}`)).json()) as Record<string, unknown>;
  expect(root).toMatchObject({ day: today, provisional: true });
  expect(root).not.toHaveProperty('signature');
});
