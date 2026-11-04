import { describe, expect, it } from 'vitest';
import { b64urlDecode, b64urlEncode, creationOptionsFromJSON, requestOptionsFromJSON } from './webauthn';

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
