// Guards the WCAG 2.2 AA contrast promise of tokens.css (>= 4.5:1 for text).
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';

const css = readFileSync(new URL('../styles/tokens.css', import.meta.url), 'utf8');
const darkStart = css.indexOf('@media (prefers-color-scheme: dark)');

function tokens(block: string): Record<string, string> {
  const out: Record<string, string> = {};
  for (const m of block.matchAll(/--(color-[\w-]+):\s*(#[0-9a-f]{6})/gi)) out[m[1]!] = m[2]!;
  return out;
}

function luminance(hex: string): number {
  const [r, g, b] = [1, 3, 5].map((i) => {
    const c = parseInt(hex.slice(i, i + 2), 16) / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  }) as [number, number, number];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}

const TEXT = ['text', 'text-muted', 'primary', 'positive', 'negative', 'warning'];
const BACKGROUNDS = ['bg', 'surface', 'surface-raised'];
const PAIRS: [string, string][] = [
  ...TEXT.flatMap((t) => BACKGROUNDS.map((b) => [t, b] as [string, string])),
  ['on-primary', 'primary'],
  ['banner-text', 'banner-bg'],
];

for (const [scheme, block] of [
  ['light', css.slice(0, darkStart)],
  ['dark', css.slice(darkStart)],
] as const) {
  describe(`${scheme} scheme contrast`, () => {
    const t = tokens(block);
    for (const [fg, bg] of PAIRS) {
      it(`${fg} on ${bg} >= 4.5:1`, () => {
        const f = t[`color-${fg}`];
        const b = t[`color-${bg}`];
        expect(f, fg).toBeDefined();
        expect(b, bg).toBeDefined();
        expect(contrast(f!, b!)).toBeGreaterThanOrEqual(4.5);
      });
    }
    it('borders and focus ring >= 3:1 against backgrounds (non-text)', () => {
      for (const bg of BACKGROUNDS) {
        expect(contrast(t['color-border']!, t[`color-${bg}`]!)).toBeGreaterThanOrEqual(3);
        expect(contrast(t['color-focus']!, t[`color-${bg}`]!)).toBeGreaterThanOrEqual(3);
      }
    });
  });
}
