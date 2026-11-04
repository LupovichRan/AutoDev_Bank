import { expect, test } from '@playwright/test';

// The e2e server runs with BANK_DEV_TEST_LOGIN=1, as `./dev demo` does, so
// the Sign-in screen offers the seeded demo users (R5).
test('demo sign-in buttons appear only with dev test login, and sign in', async ({ page }) => {
  const cfg = (await (await page.request.get('/api/config')).json()) as { dev_test_login: boolean; origin: string };
  expect(cfg.dev_test_login).toBe(true);

  await page.goto('/#/sign-in');
  const demo = page.getByRole('region', { name: 'Demo sign-in (dev only)' });
  await expect(demo).toBeVisible();
  for (const who of ['Alice (customer)', 'Bob (customer)', 'Carol (staff approver)', 'Dave (staff initiator)']) {
    await expect(demo.getByRole('button', { name: `Demo sign-in as ${who}` })).toBeVisible();
  }
  await demo.getByRole('button', { name: 'Demo sign-in as Carol (staff approver)' }).click();
  await expect(page.getByRole('heading', { level: 1, name: 'Hello, Carol' })).toBeVisible();
  expect(await (await page.request.get('/api/me')).json()).toMatchObject({ handle: 'carol', kind: 'staff' });
});
