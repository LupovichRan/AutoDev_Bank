import { expect, test } from '@playwright/test';
import { enableVirtualAuthenticator, uniqueHandle } from './helpers';

test('register with a passkey, sign out, sign back in with it', async ({ page }) => {
  await enableVirtualAuthenticator(page);
  const handle = uniqueHandle('erin');
  const displayName = `Erin ${handle.slice(-6)}`;

  await page.goto('/#/sign-in');
  await expect(page.getByRole('heading', { level: 1, name: 'Sign in' })).toBeVisible();
  await page.getByLabel('Handle', { exact: true }).fill(handle);
  await page.getByLabel('Display name', { exact: true }).fill(displayName);
  await page.getByRole('button', { name: 'Create account' }).click();

  await expect(page.getByRole('heading', { level: 1, name: `Hello, ${displayName}` })).toBeVisible();
  const me = await page.request.get('/api/me');
  expect(me.status()).toBe(200);
  expect(await me.json()).toMatchObject({ handle, display_name: displayName, kind: 'customer' });

  await page.getByRole('button', { name: 'Sign out' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Sign in' })).toBeVisible();
  expect((await page.request.get('/api/me')).status()).toBe(401);

  // Discoverable credential: no handle needed.
  await page.getByRole('button', { name: 'Sign in with passkey' }).click();
  await expect(page.getByRole('heading', { level: 1, name: `Hello, ${displayName}` })).toBeVisible();
  const again = await page.request.get('/api/me');
  expect(again.status()).toBe(200);
  expect(await again.json()).toMatchObject({ handle });
});
