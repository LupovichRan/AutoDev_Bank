// The app's default language is Hebrew, so the specs find elements by their
// Hebrew accessible names, taken from the same dictionary the app renders.
import { he, translate, type Key, type Params } from '../src/lib/messages';

/** The Hebrew text for `key`, interpolated like the app's t(). */
export function L(key: Key, params?: Params): string {
  return translate(he, key, params);
}

export function escapeRe(s: string): string {
  return s.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

/** Matches text that starts with the part of `key` before its first placeholder. */
export function startsWith(key: Key): RegExp {
  return new RegExp(`^${escapeRe(he[key].split('{')[0]!)}`);
}
