import { describe, expect, it } from 'vitest';
import { safeUrl } from './safeUrl';

describe('safeUrl', () => {
  it('allows http and https absolute urls', () => {
    expect(safeUrl('https://example.com/a?b=1')).toBe('https://example.com/a?b=1');
    expect(safeUrl('http://example.com/')).toBe('http://example.com/');
  });
  it('rejects javascript:, data:, relative, and non-strings', () => {
    expect(safeUrl('javascript:alert(1)')).toBeNull();
    expect(safeUrl(' JavaScript:alert(1)')).toBeNull();
    expect(safeUrl('data:text/html,<script>1</script>')).toBeNull();
    expect(safeUrl('/relative/path')).toBeNull();
    expect(safeUrl('//evil.example/x')).toBeNull();
    expect(safeUrl('ftp://example.com')).toBeNull();
    expect(safeUrl('')).toBeNull();
    expect(safeUrl(undefined)).toBeNull();
    expect(safeUrl(42)).toBeNull();
  });
});
