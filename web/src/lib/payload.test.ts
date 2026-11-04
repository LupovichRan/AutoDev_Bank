import { createHash } from 'node:crypto';
import { describe, expect, it } from 'vitest';
import { canonicalTransferJSON, payloadHash } from './payload';

const body = {
  to_account_id: 'acc_b',
  from_account_id: 'acc_a',
  description: 'Rent "Nov"',
  currency: 'EUR',
  amount: 125000,
};

describe('canonicalTransferJSON', () => {
  it('sorts keys and emits compact JSON', () => {
    expect(canonicalTransferJSON(body)).toBe(
      '{"amount":125000,"currency":"EUR","description":"Rent \\"Nov\\"","from_account_id":"acc_a","to_account_id":"acc_b"}',
    );
  });

  it('ignores extra fields', () => {
    const withExtra = { ...body, extra: 1 } as typeof body;
    expect(canonicalTransferJSON(withExtra)).toBe(canonicalTransferJSON(body));
  });
});

describe('payloadHash', () => {
  it('is the lowercase hex sha256 of the canonical JSON', async () => {
    const expected = createHash('sha256').update(canonicalTransferJSON(body), 'utf8').digest('hex');
    await expect(payloadHash(body)).resolves.toBe(expected);
    expect(expected).toMatch(/^[0-9a-f]{64}$/);
  });

  it('hashes UTF-8 descriptions', async () => {
    const b = { ...body, description: 'שכר דירה €' };
    const expected = createHash('sha256').update(canonicalTransferJSON(b), 'utf8').digest('hex');
    await expect(payloadHash(b)).resolves.toBe(expected);
  });
});
