import { describe, expect, it } from 'vitest';
import { convertViaEur, providersOf } from './opendata';

const rates = [
  { base: 'EUR', quote: 'USD', rate: 1.1 },
  { base: 'EUR', quote: 'ILS', rate: 4 },
];

describe('convertViaEur', () => {
  it('converts from EUR', () => {
    expect(convertViaEur(10, 'EUR', 'ILS', rates)).toBe(40);
  });
  it('cross-converts through EUR', () => {
    expect(convertViaEur(11, 'USD', 'ILS', rates)).toBeCloseTo(40, 10);
  });
  it('returns null for unknown currencies', () => {
    expect(convertViaEur(1, 'EUR', 'XYZ', rates)).toBeNull();
  });
});

describe('providersOf', () => {
  it('keeps the latest record per provider', () => {
    const rec = (provider: string, fetched_at: string, source_url: string) => ({
      kind: 'fx_rate',
      key: 'k',
      provider,
      fetched_at,
      source_url,
      license: 'L',
      data: {},
    });
    const out = providersOf([
      rec('ecb', '2026-11-04T10:00:00Z', 'old'),
      rec('ecb', '2026-11-04T11:00:00Z', 'new'),
      rec('boi', '2026-11-04T09:00:00Z', 'b'),
    ]);
    expect(out).toEqual([
      { provider: 'ecb', fetchedAt: '2026-11-04T11:00:00Z', sourceUrl: 'new', license: 'L' },
      { provider: 'boi', fetchedAt: '2026-11-04T09:00:00Z', sourceUrl: 'b', license: 'L' },
    ]);
  });
});
