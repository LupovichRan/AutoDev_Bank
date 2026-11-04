import { afterEach, describe, expect, it, vi } from 'vitest';
import { devMode } from './devmode.svelte';
import { b64urlDecode, b64urlEncode, creationOptionsFromJSON, requestOptionsFromJSON, stepUp, useDevFallback } from './webauthn';

const bytes = (b: ArrayBuffer) => Array.from(new Uint8Array(b));

describe('base64url', () => {
  it('round-trips arbitrary bytes without padding', () => {
    const input = new Uint8Array([0, 251, 255, 62, 63, 1, 2]);
    const enc = b64urlEncode(input);
    expect(enc).not.toMatch(/[+/=]/);
    expect(bytes(b64urlDecode(enc))).toEqual(Array.from(input));
  });

  it('matches a known vector', () => {
    expect(b64urlEncode(new TextEncoder().encode('hello?>'))).toBe('aGVsbG8_Pg');
  });
});

describe('options conversion', () => {
  it('decodes creation options, unwrapping {publicKey}', () => {
    const o = creationOptionsFromJSON({
      publicKey: {
        challenge: 'AQID',
        rp: { name: 'AutoDev Bank', id: 'localhost' },
        user: { id: 'BAU', name: 'alice', displayName: 'Alice' },
        pubKeyCredParams: [{ type: 'public-key', alg: -7 }],
        excludeCredentials: [{ id: 'Bgc', type: 'public-key' }],
      },
    });
    expect(bytes(o.challenge as ArrayBuffer)).toEqual([1, 2, 3]);
    expect(bytes(o.user.id as ArrayBuffer)).toEqual([4, 5]);
    expect(bytes(o.excludeCredentials![0]!.id as ArrayBuffer)).toEqual([6, 7]);
    expect(o.rp.name).toBe('AutoDev Bank');
  });

  it('decodes request options', () => {
    const o = requestOptionsFromJSON({ challenge: 'AQID', allowCredentials: [{ id: 'Bgc', type: 'public-key' }] });
    expect(bytes(o.challenge as ArrayBuffer)).toEqual([1, 2, 3]);
    expect(bytes(o.allowCredentials![0]!.id as ArrayBuffer)).toEqual([6, 7]);
  });

  it('leaves allowCredentials undefined for discoverable login', () => {
    expect(requestOptionsFromJSON({ challenge: 'AQID' }).allowCredentials).toBeUndefined();
  });
});

describe('useDevFallback', () => {
  const secure = { isSecureContext: true, hasPublicKeyCredential: true };
  const insecure = { isSecureContext: false, hasPublicKeyCredential: true };
  const noApi = { isSecureContext: true, hasPublicKeyCredential: false };
  const neither = { isSecureContext: false, hasPublicKeyCredential: false };

  it('is used only with dev_test_login and a context without WebAuthn', () => {
    expect(useDevFallback({ dev_test_login: true }, insecure)).toBe(true);
    expect(useDevFallback({ dev_test_login: true }, noApi)).toBe(true);
    expect(useDevFallback({ dev_test_login: true }, neither)).toBe(true);
  });

  it('is not used where passkeys work, even on a dev server', () => {
    expect(useDevFallback({ dev_test_login: true }, secure)).toBe(false);
  });

  it('is never used when dev_test_login is false or unknown', () => {
    for (const env of [secure, insecure, noApi, neither]) {
      expect(useDevFallback({ dev_test_login: false }, env)).toBe(false);
      expect(useDevFallback(null, env)).toBe(false);
      expect(useDevFallback(undefined, env)).toBe(false);
      expect(useDevFallback({ dev_test_login: 'true' as unknown as boolean }, env)).toBe(false);
    }
  });
});

describe('stepUp in an insecure dev context', () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    devMode.stepUpSkipped = false;
  });

  it('calls dev-stepup with the payload hash instead of the ceremony and flags the banner', async () => {
    vi.stubGlobal('window', { isSecureContext: false });
    const calls: { url: string; body?: string }[] = [];
    vi.stubGlobal('fetch', async (url: string, init: RequestInit) => {
      calls.push({ url, body: init.body as string | undefined });
      const body = url === '/api/config' ? { dev_test_login: true, origin: 'http://192.168.1.20:8080' } : { stepped_up: true };
      return new Response(JSON.stringify(body), { status: 200, headers: { 'content-type': 'application/json' } });
    });
    const hash = 'ab'.repeat(32);
    await expect(stepUp(hash)).resolves.toEqual({ stepped_up: true });
    expect(calls.map((c) => c.url)).toEqual(['/api/config', '/api/auth/dev-stepup']);
    expect(JSON.parse(calls[1]!.body!)).toEqual({ payload_hash: hash });
    expect(devMode.stepUpSkipped).toBe(true);
  });
});
