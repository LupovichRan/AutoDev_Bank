import { expect, test } from '@playwright/test';
import { enableVirtualAuthenticator, uniqueHandle } from './helpers';
import { L } from './i18n';

test('register with a passkey, sign out, sign back in with it', async ({ page }) => {
  await enableVirtualAuthenticator(page);
  const handle = uniqueHandle('erin');
  const displayName = `Erin ${handle.slice(-6)}`;

  await page.goto('/#/sign-in');
  await expect(page.getByRole('heading', { level: 1, name: L('title.signIn') })).toBeVisible();
  await page.getByLabel(L('signin.handle'), { exact: true }).fill(handle);
  await page.getByLabel(L('signin.displayName'), { exact: true }).fill(displayName);
  await page.getByRole('button', { name: L('signin.create') }).click();

  await expect(page.getByRole('heading', { level: 1, name: L('home.hello', { name: displayName }) })).toBeVisible();
  const me = await page.request.get('/api/me');
  expect(me.status()).toBe(200);
  expect(await me.json()).toMatchObject({ handle, display_name: displayName, kind: 'customer' });

  await page.getByRole('button', { name: L('app.signOut') }).click();
  await expect(page.getByRole('heading', { level: 1, name: L('title.signIn') })).toBeVisible();
  expect((await page.request.get('/api/me')).status()).toBe(401);

  // Discoverable credential: no handle needed.
  await page.getByRole('button', { name: L('signin.withPasskey') }).click();
  await expect(page.getByRole('heading', { level: 1, name: L('home.hello', { name: displayName }) })).toBeVisible();
  const again = await page.request.get('/api/me');
  expect(again.status()).toBe(200);
  expect(await again.json()).toMatchObject({ handle });
});
