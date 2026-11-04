import { describe, expect, it } from 'vitest';
import { formatPercent, parseLatLon, relativeTime, truncateMiddle } from './format';

describe('relativeTime', () => {
  const now = Date.parse('2026-11-04T12:00:00Z');
  it('formats minutes and hours in the past', () => {
    expect(relativeTime('2026-11-04T11:55:00Z', now)).toBe('5 minutes ago');
    expect(relativeTime('2026-11-04T09:00:00Z', now)).toBe('3 hours ago');
    expect(relativeTime('2026-11-03T12:00:00Z', now)).toBe('yesterday');
  });
  it('handles now and bad input', () => {
    expect(relativeTime('2026-11-04T12:00:00Z', now)).toBe('now');
    expect(relativeTime('nope', now)).toBe('unknown time');
  });
});

describe('parseLatLon', () => {
  it('parses the London default', () => {
    expect(parseLatLon('51.5074,-0.1278')).toEqual({ lat: 51.5074, lon: -0.1278 });
    expect(parseLatLon(' 51.5 , -0.12 ')).toEqual({ lat: 51.5, lon: -0.12 });
  });
  it('rejects garbage and out-of-range values', () => {
    expect(parseLatLon('')).toBeNull();
    expect(parseLatLon('abc')).toBeNull();
    expect(parseLatLon('91,0')).toBeNull();
    expect(parseLatLon('0,181')).toBeNull();
  });
});

describe('misc', () => {
  it('formats percent', () => {
    expect(formatPercent(4.5, 'en')).toBe('4.5%');
    expect(formatPercent(null)).toBe('—');
  });
  it('truncates long hashes in the middle', () => {
    expect(truncateMiddle('a'.repeat(10) + 'b'.repeat(10), 4)).toBe('aaaa…bbbb');
    expect(truncateMiddle('short', 4)).toBe('short');
  });
});
