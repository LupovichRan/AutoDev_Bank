import { afterEach, describe, expect, it } from 'vitest';
import { currentLocale, DEFAULT_LANG, i18n, t, tValue } from './i18n.svelte';
import { en, he, interpolate } from './messages';
import { formatMinor, parseToMinor } from './money';

afterEach(() => i18n.set(DEFAULT_LANG));

const placeholders = (s: string) => [...s.matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();

describe('dictionaries', () => {
  it('have the same keys and the same placeholders', () => {
    expect(Object.keys(he).sort()).toEqual(Object.keys(en).sort());
    for (const k of Object.keys(en) as (keyof typeof en)[]) {
      expect(placeholders(he[k]), k).toEqual(placeholders(en[k]));
      expect(he[k].trim(), k).not.toBe('');
    }
  });
});

describe('t()', () => {
  it('defaults to Hebrew, right to left', () => {
    expect(DEFAULT_LANG).toBe('he');
    expect(i18n.lang).toBe('he');
    expect(i18n.dir).toBe('rtl');
    expect(currentLocale()).toBe('he-IL');
    expect(t('title.home')).toBe('בית');
  });

  it('interpolates {name} params, numbers included', () => {
    expect(t('home.hello', { name: 'Carol' })).toBe('שלום, Carol');
    expect(t('approvals.countValue', { done: 1, required: 2 })).toBe('1 מתוך 2');
    i18n.set('en');
    expect(t('home.hello', { name: 'Carol' })).toBe('Hello, Carol');
    expect(t('pay.awaitingMany', { n: 3 })).toBe('Awaiting approval (3 approvers required).');
  });

  it('switches language, direction and locale', () => {
    i18n.set('en');
    expect(i18n.dir).toBe('ltr');
    expect(currentLocale()).toBe('en');
    expect(t('title.openFinance')).toBe('Open Finance');
  });

  it('leaves unknown placeholders and missing params alone', () => {
    expect(interpolate('{a} and {b}', { a: 'x' })).toBe('x and {b}');
    expect(interpolate('no params {x}')).toBe('no params {x}');
  });

  it('translates known server values and passes unknown ones through', () => {
    expect(tValue('status', 'pending')).toBe('ממתין לאישור');
    expect(tValue('status', 'brand_new')).toBe('brand_new');
  });
});

describe('money in he-IL', () => {
  it('places the shekel sign after the number with bidi marks', () => {
    const s = formatMinor(123456, 'ILS', 'he-IL');
    expect(s).toContain('1,234.56');
    expect(s.indexOf('₪')).toBeGreaterThan(s.indexOf('1,234.56'));
  });

  it('follows the app language when no locale is given', () => {
    expect(formatMinor(5000, 'ILS')).toBe(formatMinor(5000, 'ILS', 'he-IL'));
    i18n.set('en');
    expect(formatMinor(5000, 'ILS')).toBe('₪50.00');
  });

  it('shows a sign for credits when asked', () => {
    expect(formatMinor(1050, 'ILS', 'he-IL', { signed: true })).toMatch(/\+10\.50/);
    expect(formatMinor(-1050, 'ILS', 'he-IL', { signed: true })).toMatch(/-10\.50/);
    expect(formatMinor(1050, 'EUR', 'en', { signed: true })).toBe('+€10.50');
  });

  it('parses grouped and plain amounts whatever the language', () => {
    expect(parseToMinor('1,000.50', 'ILS')).toBe(100050);
    expect(parseToMinor('1000.50', 'ILS')).toBe(100050);
    i18n.set('en');
    expect(parseToMinor('1,000.50', 'ILS')).toBe(100050);
  });
});
