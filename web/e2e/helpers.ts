// Shared helpers for the end-to-end specs. The server runs with
// BANK_DEV_TEST_LOGIN=1, which mounts POST /api/auth/dev-login and
// POST /api/auth/dev-stepup (test-only; never in production).
import { createHash, randomBytes } from 'node:crypto';
import { expect, request as playwrightRequest, type APIRequestContext, type Page } from '@playwright/test';
import type { Account, Approval } from '../src/lib/types';

/**
 * Attach a CTAP2 platform authenticator to the page that creates resident
 * keys and verifies the user automatically, so passkey prompts resolve
 * without interaction. Credentials live as long as the page.
 */
export async function enableVirtualAuthenticator(page: Page): Promise<string> {
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('WebAuthn.enable');
  const { authenticatorId } = await cdp.send('WebAuthn.addVirtualAuthenticator', {
    options: {
      protocol: 'ctap2',
      transport: 'internal',
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  });
  return authenticatorId;
}

/** A handle the server has not seen: 3–32 chars of [a-z0-9-]. */
export function uniqueHandle(prefix: string): string {
  const suffix = `${Date.now().toString(36)}${randomBytes(3).toString('hex')}`;
  return `${prefix}-${suffix}`.toLowerCase().slice(0, 32);
}

/**
 * Sign the browser in as `handle` (created on first use) through the
 * dev-login route, sharing the page's cookie jar, then load Home afresh so
 * the app re-reads /api/me.
 */
export async function devLogin(page: Page, handle: string): Promise<void> {
  const res = await page.request.post('/api/auth/dev-login', { data: { handle } });
  expect(res.ok(), `dev-login ${handle}: ${res.status()}`).toBeTruthy();
  // A hash-only change would keep the old in-memory session; leave the document first.
  await page.goto('about:blank');
  await page.goto('/#/');
  await expect(page.getByRole('heading', { level: 1, name: /^Hello, / })).toBeVisible();
}

/** A separate cookie jar signed in as `handle`, for API-only setup and checks. */
export async function apiAs(baseURL: string, handle: string): Promise<APIRequestContext> {
  const ctx = await playwrightRequest.newContext({ baseURL });
  const res = await ctx.post('/api/auth/dev-login', { data: { handle } });
  expect(res.ok(), `dev-login ${handle}: ${res.status()}`).toBeTruthy();
  return ctx;
}

export function sha256Hex(s: string): string {
  return createHash('sha256').update(s).digest('hex');
}

/**
 * Add a passkey (in the page's virtual authenticator) to the signed-in
 * principal through the real add-passkey ceremony. A dev-login principal
 * has no passkey to step up with, so only the add-credential step-up is
 * bootstrapped with dev-stepup; every later step-up is a real assertion.
 * register/finish replaces the session cookie, which the page keeps.
 */
export async function addPasskey(page: Page, handle: string): Promise<void> {
  const res = await page.request.post('/api/auth/dev-stepup', {
    data: { payload_hash: sha256Hex(`add-credential:${handle}`) },
  });
  expect(res.ok(), `dev-stepup: ${res.status()}`).toBeTruthy();
  const status = await page.evaluate(async (h) => {
    const post = (path: string, body: unknown) =>
      fetch(path, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) });
    const begin = await post('/api/auth/register/begin', { handle: h, display_name: h });
    if (!begin.ok) return `begin ${begin.status}`;
    const { challenge_id, options } = (await begin.json()) as { challenge_id: string; options: { publicKey?: unknown } };
    const PKC = PublicKeyCredential as unknown as {
      parseCreationOptionsFromJSON(o: unknown): PublicKeyCredentialCreationOptions;
    };
    const cred = (await navigator.credentials.create({
      publicKey: PKC.parseCreationOptionsFromJSON(options.publicKey ?? options),
    })) as PublicKeyCredential;
    const finish = await post('/api/auth/register/finish', { challenge_id, credential: cred.toJSON() });
    return finish.ok ? 'ok' : `finish ${finish.status}`;
  }, handle);
  expect(status).toBe('ok');
}

export async function accounts(api: APIRequestContext): Promise<Account[]> {
  const res = await api.get('/api/accounts');
  expect(res.ok()).toBeTruthy();
  return ((await res.json()) as { accounts: Account[] }).accounts;
}

export async function accountByCode(api: APIRequestContext, code: string): Promise<Account> {
  const a = (await accounts(api)).find((x) => x.code === code);
  if (!a) throw new Error(`no account ${code}`);
  return a;
}

export async function approvals(api: APIRequestContext): Promise<{ inbox: Approval[]; mine: Approval[] }> {
  const res = await api.get('/api/approvals');
  expect(res.ok()).toBeTruthy();
  return (await res.json()) as { inbox: Approval[]; mine: Approval[] };
}

// Seeded account codes (internal/seed/seed.go).
export const ALICE_ILS = 'cust.alice.ILS.current';
export const JOINT = 'cust.alice-bob.ILS.joint';
export const BOB_ILS = 'cust.bob.ILS.current';
