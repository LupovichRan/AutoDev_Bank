// Only absolute http(s) URLs may be bound to href; anything else (javascript:,
// data:, relative, malformed) is rejected so callers render plain text.
export function safeUrl(u: unknown): string | null {
  if (typeof u !== 'string') return null;
  const s = u.trim();
  if (!s) return null;
  try {
    const parsed = new URL(s);
    return parsed.protocol === 'http:' || parsed.protocol === 'https:' ? parsed.href : null;
  } catch {
    return null;
  }
}
