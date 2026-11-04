import { expect, test, type Page } from '@playwright/test';
import {
  accountByCode,
  addPasskey,
  ALICE_ILS,
  apiAs,
  approvals,
  BOB_ILS,
  devLogin,
  enableVirtualAuthenticator,
  JOINT,
} from './helpers';

/** Fill the Pay form (from/to preselected by query) and confirm; the virtual authenticator answers the step-up. */
async function pay(page: Page, fromId: string, toId: string, amount: string, description: string) {
  await page.goto(`/#/pay?from=${fromId}&to=${toId}`);
  await expect(page.getByRole('heading', { level: 1, name: 'Pay' })).toBeVisible();
  await expect(page.getByLabel('From')).toHaveValue(fromId);
  await expect(page.getByLabel('Destination account id')).toHaveValue(toId);
  await page.getByLabel(/^Amount/).fill(amount);
  await page.getByLabel(/^Description/).fill(description);
  await page.getByRole('button', { name: 'Confirm and pay' }).click();
}

test('payments: step-up, maker-checker on the joint account, no self-approval', async ({ page, baseURL }) => {
  const run = Date.now().toString(36);
  const bobApi = await apiAs(baseURL!, 'bob');
  const bobIls = await accountByCode(bobApi, BOB_ILS);

  await enableVirtualAuthenticator(page);
  await devLogin(page, 'alice');
  await addPasskey(page, 'alice');
  const aliceIls = await accountByCode(page.request, ALICE_ILS);
  const joint = await accountByCode(page.request, JOINT);

  await test.step('50.00 ILS to Bob is sent after a passkey step-up', async () => {
    const lunch = `e2e lunch ${run}`;
    await pay(page, aliceIls.id, bobIls.id, '50.00', lunch);
    await expect(page.getByRole('heading', { name: 'Sent', exact: true })).toBeVisible();
    await expect(page.getByText(/^Sent .*50\.00/)).toBeVisible();
    expect((await accountByCode(page.request, ALICE_ILS)).balance).toBe(aliceIls.balance - 5000);

    await page.getByRole('link', { name: 'Back to Home' }).click();
    await page.getByRole('button', { name: /Alice current account/ }).click();
    await expect(page.getByRole('region', { name: 'Recent activity' }).getByText(lunch)).toBeVisible();
  });

  const big = `e2e joint ${run}`;
  await test.step('2,000.00 ILS from the joint account awaits approval', async () => {
    await pay(page, joint.id, bobIls.id, '2000.00', big);
    await expect(page.getByRole('heading', { name: 'Awaiting approval', exact: true })).toBeVisible();
    expect((await accountByCode(page.request, JOINT)).balance).toBe(joint.balance);

    await page.goto('/#/approvals');
    const item = page.getByRole('region', { name: 'Started by you' }).getByRole('listitem').filter({ hasText: big });
    await expect(item.getByText('pending', { exact: true })).toBeVisible();
    await expect(item.getByRole('button', { name: /Approve/ })).toHaveCount(0);
  });

  await test.step('Bob approves it from his inbox', async () => {
    await devLogin(page, 'bob');
    await page.goto('/#/approvals');
    const item = page.getByRole('region', { name: 'Waiting for you' }).getByRole('listitem').filter({ hasText: big });
    await expect(item).toBeVisible();
    await expect(item.getByText('Alice', { exact: true })).toBeVisible();
    await item.getByRole('button', { name: new RegExp(`^Approve ${big}$`) }).click();
    await expect(item.getByRole('button', { name: /Approve/ })).toHaveCount(0);
  });

  await test.step('Alice sees it executed and the joint balance dropped', async () => {
    await devLogin(page, 'alice');
    await page.goto('/#/approvals');
    const item = page.getByRole('region', { name: 'Started by you' }).getByRole('listitem').filter({ hasText: big });
    await expect(item.getByText('executed', { exact: true })).toBeVisible();
    expect((await accountByCode(page.request, JOINT)).balance).toBe(joint.balance - 200000);
    const mine = (await approvals(page.request)).mine.find((a) => a.subject.description === big);
    expect(mine?.status).toBe('executed');
  });

  await test.step('Bob is never offered Approve on a payment he started', async () => {
    const own = `e2e bob-own ${run}`;
    await devLogin(page, 'bob');
    await addPasskey(page, 'bob');
    await pay(page, joint.id, aliceIls.id, '1500.00', own);
    await expect(page.getByRole('heading', { name: 'Awaiting approval', exact: true })).toBeVisible();

    await page.goto('/#/approvals');
    await expect(page.getByRole('heading', { level: 1, name: 'Approvals' })).toBeVisible();
    const mineItem = page.getByRole('region', { name: 'Started by you' }).getByRole('listitem').filter({ hasText: own });
    await expect(mineItem.getByText('pending', { exact: true })).toBeVisible();
    await expect(page.getByRole('listitem').filter({ hasText: own }).getByRole('button', { name: /Approve/ })).toHaveCount(0);
    const inbox = (await approvals(page.request)).inbox;
    expect(inbox.some((a) => a.subject.description === own)).toBe(false);

    // Alice, the other holder, rejects it, which clears the joint account.
    await devLogin(page, 'alice');
    await page.goto('/#/approvals');
    const inboxItem = page.getByRole('region', { name: 'Waiting for you' }).getByRole('listitem').filter({ hasText: own });
    await inboxItem.getByRole('button', { name: /^Reject/ }).click();
    const dialog = page.getByRole('dialog', { name: 'Reject payment' });
    await dialog.getByLabel('Reason').fill('e2e: not needed');
    await dialog.getByRole('button', { name: 'Reject', exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(inboxItem.getByRole('button', { name: /^Reject/ })).toHaveCount(0);
  });

  await bobApi.dispose();
});
