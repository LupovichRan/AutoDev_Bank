// Helpers for /api/opendata records.
import type { OpenDataRecord } from './types';

export interface ProviderInfo {
  provider: string;
  fetchedAt: string;
  sourceUrl: string;
  license: string;
}

/** One badge per provider: the most recently fetched record's metadata. */
export function providersOf<D>(records: OpenDataRecord<D>[]): ProviderInfo[] {
  const latest = new Map<string, OpenDataRecord<D>>();
  for (const r of records) {
    const prev = latest.get(r.provider);
    if (!prev || Date.parse(r.fetched_at) > Date.parse(prev.fetched_at)) latest.set(r.provider, r);
  }
  return [...latest.values()].map((r) => ({
    provider: r.provider,
    fetchedAt: r.fetched_at,
    sourceUrl: r.source_url,
    license: r.license,
  }));
}

export interface Rate {
  base: string;
  quote: string;
  rate: number;
}

/**
 * Convert between currencies using EUR-based rates (1 EUR = rate QUOTE).
 * Display only: money movement never uses floating point.
 */
export function convertViaEur(amount: number, from: string, to: string, eurRates: Rate[]): number | null {
  const perEur = (c: string): number | null => {
    if (c === 'EUR') return 1;
    const r = eurRates.find((x) => x.base === 'EUR' && x.quote === c);
    return r && r.rate > 0 ? r.rate : null;
  };
  const f = perEur(from);
  const t = perEur(to);
  if (f === null || t === null || !Number.isFinite(amount)) return null;
  return (amount / f) * t;
}
