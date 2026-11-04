import { describe, expect, it } from 'vitest';
import { formatMinor, minorExponent, parseToMinor } from './money';

describe('formatMinor', () => {
  it('formats ILS minor units with grouping and two decimals', () => {
    expect(formatMinor(123456, 'ILS', 'en-GB')).toContain('1,234.56');
  });

  it('uses the default locale when none is given', () => {
    expect(formatMinor(123456, 'ILS')).toContain('1,234.56');
  });

  it('includes the currency', () => {
    expect(formatMinor(1050, 'EUR', 'en-GB')).toBe('€10.50');
    expect(formatMinor(1050, 'GBP', 'en-GB')).toBe('£10.50');
    expect(formatMinor(1050, 'USD', 'en-US')).toBe('$10.50');
  });

  it('keeps the sign of negative amounts', () => {
    expect(formatMinor(-5, 'EUR', 'en-GB')).toBe('-€0.05');
  });

  it('formats zero', () => {
    expect(formatMinor(0, 'GBP', 'en-GB')).toBe('£0.00');
  });

  it('accepts bigint without losing precision beyond 2^53', () => {
    const big = 123456789012345678901n;
    expect(formatMinor(big, 'EUR', 'en-GB')).toBe('€1,234,567,890,123,456,789.01');
  });

  it('respects zero-decimal currencies', () => {
    expect(formatMinor(1500, 'JPY', 'en-GB')).toContain('1,500');
  });

  it('rejects non-integer numbers', () => {
    expect(() => formatMinor(1.5, 'EUR')).toThrow();
  });
});

describe('minorExponent', () => {
  it('is 2 for the bank currencies', () => {
    for (const c of ['ILS', 'EUR', 'GBP', 'USD']) expect(minorExponent(c)).toBe(2);
  });
  it('is 0 for JPY', () => {
    expect(minorExponent('JPY')).toBe(0);
  });
});

describe('parseToMinor', () => {
  it('parses one decimal', () => {
    expect(parseToMinor('12.5', 'EUR')).toBe(1250);
  });
  it('parses whole numbers', () => {
    expect(parseToMinor('12', 'ILS')).toBe(1200);
  });
  it('parses two decimals', () => {
    expect(parseToMinor('0.07', 'GBP')).toBe(7);
  });
  it('tolerates surrounding whitespace and thousands separators', () => {
    expect(parseToMinor('  1,234.56 ', 'USD')).toBe(123456);
  });
  it('accepts a leading decimal point', () => {
    expect(parseToMinor('.5', 'EUR')).toBe(50);
  });
  it('throws on more than two decimals', () => {
    expect(() => parseToMinor('1.234', 'EUR')).toThrow();
  });
  it('throws on garbage', () => {
    for (const bad of ['', ' ', 'abc', '1.2.3', '1e5', '12,34', '--1', '1.', 'NaN', 'Infinity']) {
      expect(() => parseToMinor(bad, 'EUR'), bad).toThrow();
    }
  });
  it('parses negatives', () => {
    expect(parseToMinor('-3.10', 'EUR')).toBe(-310);
  });
  it('throws beyond the safe integer range', () => {
    expect(() => parseToMinor('900719925474099.93', 'EUR')).toThrow();
  });
  it('does not suffer binary floating point error', () => {
    expect(parseToMinor('0.29', 'EUR')).toBe(29);
    expect(parseToMinor('1.15', 'EUR')).toBe(115);
  });
});
