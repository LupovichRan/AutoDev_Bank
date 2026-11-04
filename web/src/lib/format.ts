// Small display helpers shared by screens. Locales default to the app language.
import { currentLocale, t } from './i18n.svelte';

const UNITS: [Intl.RelativeTimeFormatUnit, number][] = [
  ['year', 365 * 24 * 3600],
  ['month', 30 * 24 * 3600],
  ['week', 7 * 24 * 3600],
  ['day', 24 * 3600],
  ['hour', 3600],
  ['minute', 60],
  ['second', 1],
];

/** "5 minutes ago" style text for an ISO timestamp. */
export function relativeTime(iso: string, now: number = Date.now(), locale: string = currentLocale()): string {
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return t('format.unknownTime');
  const diff = Math.round((ms - now) / 1000);
  const fmt = new Intl.RelativeTimeFormat(locale, { numeric: 'auto' });
  for (const [unit, secs] of UNITS) {
    if (Math.abs(diff) >= secs || unit === 'second') return fmt.format(Math.round(diff / secs), unit);
  }
  return fmt.format(0, 'second');
}

export function formatDateTime(iso: string, locale: string = currentLocale()): string {
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return iso;
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium', timeStyle: 'short' }).format(ms);
}

export function formatDate(iso: string, locale: string = currentLocale()): string {
  const ms = Date.parse(iso);
  if (Number.isNaN(ms)) return iso;
  return new Intl.DateTimeFormat(locale, { dateStyle: 'medium' }).format(ms);
}

/** Percent with up to 2 decimals; null/undefined shows an em dash. */
export function formatPercent(v: number | null | undefined, locale: string = currentLocale()): string {
  if (v === null || v === undefined || Number.isNaN(v)) return '—';
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(v)}%`;
}

export interface LatLon {
  lat: number;
  lon: number;
}

/** Parse "51.5074,-0.1278" (spaces allowed). Returns null when invalid. */
export function parseLatLon(input: string): LatLon | null {
  const m = /^\s*(-?\d+(?:\.\d+)?)\s*,\s*(-?\d+(?:\.\d+)?)\s*$/.exec(input);
  if (!m) return null;
  const lat = Number(m[1]);
  const lon = Number(m[2]);
  if (lat < -90 || lat > 90 || lon < -180 || lon > 180) return null;
  return { lat, lon };
}

export function truncateMiddle(s: string, keep = 8): string {
  return s.length <= keep * 2 + 1 ? s : `${s.slice(0, keep)}…${s.slice(-keep)}`;
}

/** Plain number in the app locale (counts, rates, distances). */
export function formatNumber(n: number, maximumFractionDigits = 0, locale: string = currentLocale()): string {
  return new Intl.NumberFormat(locale, { maximumFractionDigits }).format(n);
}
