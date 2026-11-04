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
  sha256Hex,
} from './helpers';

/** Fill the Pay form (from/to preselected by query) and confirm; the virtual authenticator answers the step-up. */
async function pay(page: Page, fromId: string, toId: string, amount: string, description: string) {
  await page.goto(`/#/pay?from=${fromId}&to=${toId}`);
  await expect(page.getByRole('heading', { level: 1, name: 'Pay' })).toBeVisible();
  await expect(page.getByLabel('From', { exact: true })).toHaveValue(fromId);
  await expect(page.getByLabel('Destination account id', { exact: true })).toHaveValue(toId);
  await page.getByLabel(/^Amount/).fill(amount);
  await page.getByLabel(/^Description/).fill(description);
  await page.getByRole('button', { name: 'Confirm and pay' }).click();
}

test('payments: step-up, maker-checker on the joint account, no self-approval', async ({ page, baseURL }) => {
  const run = Date.now().toString(36);

  // Both people need a passkey: paying and approving each take a step-up
  // through the page's virtual authenticator. Adding a passkey ends the
  // principal's other sessions, so the API-only context for Bob is opened
  // after his passkey exists.
  await enableVirtualAuthenticator(page);
  await devLogin(page, 'bob');
  await addPasskey(page, 'bob');
  const bobApi = await apiAs(baseURL!, 'bob');
  const bobIls = await accountByCode(bobApi, BOB_ILS);

  await devLogin(page, 'alice');
  await addPasskey(page, 'alice');
  const aliceIls = await accountByCode(page.request, ALICE_ILS);
  const joint = await accountByCode(page.request, JOINT);
  // Balances are always compared with a value read just before the action, so
  // the spec holds on a database other runs have already paid from.
  const balance = async (api: typeof page.request, code: string) => (await accountByCode(api, code)).balance;

  await test.step('50.00 ILS to Bob is sent after a passkey step-up', async () => {
    const lunch = `e2e lunch ${run}`;
    const before = await balance(page.request, ALICE_ILS);
    await pay(page, aliceIls.id, bobIls.id, '50.00', lunch);
    await expect(page.getByRole('heading', { name: 'Sent', exact: true })).toBeVisible();
    await expect(page.getByTestId('pay-outcome').getByText(/^Sent .*50\.00/)).toBeVisible();
    expect(await balance(page.request, ALICE_ILS)).toBe(before - 5000);

    await page.getByRole('link', { name: 'Back to Home' }).click();
    await page.getByRole('button', { name: /Alice current account/ }).click();
    await expect(page.getByRole('region', { name: 'Recent activity' }).getByText(lunch)).toBeVisible();
  });

  const big = `e2e joint ${run}`;
  let jointBeforeBig = 0;
  await test.step('2,000.00 ILS from the joint account awaits approval', async () => {
    jointBeforeBig = await balance(page.request, JOINT);
    await pay(page, joint.id, bobIls.id, '2000.00', big);
    await expect(page.getByRole('heading', { name: 'Awaiting approval', exact: true })).toBeVisible();
    expect(await balance(page.request, JOINT)).toBe(jointBeforeBig);

    await page.goto('/#/approvals');
    const item = page.getByRole('region', { name: 'Started by you' }).getByRole('listitem').filter({ hasText: big });
    await expect(item.getByText('pending', { exact: true })).toBeVisible();
    await expect(item.getByRole('button', { name: /Approve/ })).toHaveCount(0);
  });

  await test.step('Bob cannot approve through the API without a step-up', async () => {
    const pending = (await approvals(bobApi)).inbox.find((a) => a.subject.description === big);
    expect(pending, 'in bob\'s inbox').toBeTruthy();
    const res = await bobApi.post(`/api/approvals/${pending!.id}/decide`, { data: { approve: true, reason: 'no step-up' } });
    expect(res.status()).toBe(403);
    expect(((await res.json()) as { detail?: string }).detail).toBe('step_up_required');
  });

  await test.step('Bob approves it from his inbox with his passkey', async () => {
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
    expect(await balance(page.request, JOINT)).toBe(jointBeforeBig - 200000);
    const mine = (await approvals(page.request)).mine.find((a) => a.subject.description === big);
    expect(mine?.status).toBe('executed');
  });

  await test.step('Bob is never offered Approve on a payment he started', async () => {
    const own = `e2e bob-own ${run}`;
    const jointBeforeOwn = await balance(page.request, JOINT);
    let approvalId = '';
    await devLogin(page, 'bob');
    await pay(page, joint.id, aliceIls.id, '1500.00', own);
    await expect(page.getByRole('heading', { name: 'Awaiting approval', exact: true })).toBeVisible();

    await page.goto('/#/approvals');
    await expect(page.getByRole('heading', { level: 1, name: 'Approvals' })).toBeVisible();
    const mineItem = page.getByRole('region', { name: 'Started by you' }).getByRole('listitem').filter({ hasText: own });
    await expect(mineItem.getByText('pending', { exact: true })).toBeVisible();
    await expect(page.getByRole('listitem').filter({ hasText: own }).getByRole('button', { name: /Approve/ })).toHaveCount(0);
    const inbox = (await approvals(page.request)).inbox;
    expect(inbox.some((a) => a.subject.description === own)).toBe(false);

    // The API refuses the initiator too, not just the UI, even with a
    // step-up for this approval (bootstrapped with dev-stepup).
    const ownApproval = (await approvals(page.request)).mine.find((a) => a.subject.description === own);
    expect(ownApproval, "bob's pending approval").toBeTruthy();
    approvalId = ownApproval!.id;
    const su = await page.request.post('/api/auth/dev-stepup', { data: { payload_hash: sha256Hex(`approve:${approvalId}`) } });
    expect(su.ok()).toBeTruthy();
    const self = await page.request.post(`/api/approvals/${approvalId}/decide`, {
      data: { approve: true, reason: 'e2e self' },
    });
    expect(self.status()).toBe(403);
    expect(((await self.json()) as { detail?: string }).detail).toBe('self_approval');

    // Alice, the other holder, rejects it, which clears the joint account.
    await devLogin(page, 'alice');
    await page.goto('/#/approvals');
    const inboxItem = page.getByRole('region', { name: 'Waiting for you' }).getByRole('listitem').filter({ hasText: own });
    await inboxItem.getByRole('button', { name: /^Reject/ }).click();
    const dialog = page.getByRole('dialog', { name: 'Reject payment' });
    await dialog.getByLabel('Reason', { exact: true }).fill('e2e: not needed');
    await dialog.getByRole('button', { name: 'Reject', exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(inboxItem.getByRole('button', { name: /^Reject/ })).toHaveCount(0);
    // Via the API: the approval is rejected and no money moved.
    const bobView = (await approvals(bobApi)).mine.find((a) => a.id === approvalId);
    expect(bobView?.status).toBe('rejected');
    expect(await balance(page.request, JOINT)).toBe(jointBeforeOwn);
  });

  await test.step('Alice cancels a payment she started', async () => {
    const oops = `e2e cancel ${run}`;
    await devLogin(page, 'alice');
    const jointBefore = await balance(page.request, JOINT);
    await pay(page, joint.id, bobIls.id, '1200.00', oops);
    await expect(page.getByRole('heading', { name: 'Awaiting approval', exact: true })).toBeVisible();
    await page.goto('/#/approvals');
    const item = page.getByRole('region', { name: 'Started by you' }).getByRole('listitem').filter({ hasText: oops });
    await expect(item.getByText('pending', { exact: true })).toBeVisible();
    await item.getByRole('button', { name: new RegExp(`^Cancel payment ${oops}$`) }).click();
    await expect(item.getByText('rejected', { exact: true })).toBeVisible();
    await expect(item.getByRole('button', { name: /Cancel payment/ })).toHaveCount(0);
    expect(await balance(page.request, JOINT)).toBe(jointBefore);
    // Bob's inbox no longer offers it.
    expect((await approvals(bobApi)).inbox.some((a) => a.subject.description === oops)).toBe(false);
  });

  await bobApi.dispose();
});
