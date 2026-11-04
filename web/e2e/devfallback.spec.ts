import { expect, test } from '@playwright/test';
import { accountByCode, ALICE_ILS, apiAs, BOB_ILS } from './helpers';
import { escapeRe, L } from './i18n';

// A WebView without WebAuthn (as in a test APK on http://192.168.x.x): the
// server has BANK_DEV_TEST_LOGIN=1, so sign-in offers only the demo users and
// payment step-up goes through /api/auth/dev-stepup with a visible banner.
test('dev-mode fallback when the browser has no PublicKeyCredential', async ({ page, baseURL }) => {
  await page.addInitScript(() => {
    delete (window as unknown as { PublicKeyCredential?: unknown }).PublicKeyCredential;
  });

  await page.goto('/#/sign-in');
  await expect(page.getByText(L('signin.devFallback'))).toBeVisible();
  await expect(page.getByRole('button', { name: L('signin.withPasskey') })).toHaveCount(0);
  await expect(page.getByRole('button', { name: L('signin.create') })).toHaveCount(0);
  const demo = page.getByRole('region', { name: L('signin.demoTitle') });
  await demo.getByRole('button', { name: L('signin.demoAs', { who: L('signin.demo.alice') }) }).click();
  await expect(page.getByRole('heading', { level: 1, name: L('home.hello', { name: 'Alice' }) })).toBeVisible();

  const alice = await accountByCode(page.request, ALICE_ILS);
  const before = alice.balance;
  // Bob's account id, from a separate demo session.
  const bobApi = await apiAs(baseURL!, 'bob');
  const bob = await accountByCode(bobApi, BOB_ILS);
  await bobApi.dispose();

  const note = `e2e dev-fallback ${Date.now().toString(36)}`;
  await page.goto(`/#/pay?from=${alice.id}&to=${bob.id}`);
  await page.getByLabel(new RegExp(`^${escapeRe(L('pay.amount'))}`)).fill('1.00');
  await page.getByLabel(new RegExp(`^${escapeRe(L('pay.description'))}`)).fill(note);
  await page.getByRole('button', { name: L('pay.confirm') }).click();

  await expect(page.getByRole('heading', { name: L('pay.sent'), exact: true })).toBeVisible();
  await expect(page.getByTestId('dev-stepup-banner')).toContainText(L('app.devStepUpSkipped'));
  expect((await accountByCode(page.request, ALICE_ILS)).balance).toBe(before - 100);
});
