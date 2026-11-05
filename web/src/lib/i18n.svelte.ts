// In-house i18n: Hebrew first (right to left), English second. No library.
// `t()` reads the reactive `i18n.lang`, so templates re-render on a switch.
import { detailCode, labelFor, type LabelKind } from './labels';
import { DICTS, en, he, isKey, localeFor, translate, type Key, type Lang, type Params } from './messages';

export { en, he, type Key, type Lang, type Params };

const STORAGE_KEY = 'autodev.lang';
export const DEFAULT_LANG: Lang = 'he';

function stored(): Lang {
  try {
    const v = typeof localStorage !== 'undefined' ? localStorage.getItem(STORAGE_KEY) : null;
    if (v === 'he' || v === 'en') return v;
  } catch {
    // storage unavailable (private mode, WebView without DOM storage)
  }
  return DEFAULT_LANG;
}

function applyToDocument(lang: Lang): void {
  if (typeof document === 'undefined') return;
  document.documentElement.lang = lang;
  document.documentElement.dir = lang === 'he' ? 'rtl' : 'ltr';
}

class I18n {
  lang = $state<Lang>(stored());
  readonly dir = $derived<'rtl' | 'ltr'>(this.lang === 'he' ? 'rtl' : 'ltr');
  readonly locale = $derived(localeFor(this.lang));

  constructor() {
    applyToDocument(this.lang);
  }

  set(lang: Lang): void {
    this.lang = lang;
    try {
      localStorage.setItem(STORAGE_KEY, lang);
    } catch {
      // storage unavailable: the choice lasts for this page only
    }
    applyToDocument(lang);
  }
}

export const i18n = new I18n();

export function t(key: Key, params?: Params): string {
  return translate(DICTS[i18n.lang], key, params);
}

/** Display name of a server value (status, kind, provider…) in the UI language; unknown values show as sent. */
export function lbl(kind: LabelKind, value: string | null | undefined): string {
  return labelFor(kind, value, i18n.lang);
}

/** A problem detail (code or server sentence) in the UI language; unknown details show as sent. */
export function tDetail(detail: string): string {
  const k = `problem.detail.${detailCode(detail)}`;
  return isKey(k) ? t(k) : detail;
}

/** Locale for Intl formatting of the current language ("he-IL" or "en"). */
export function currentLocale(): string {
  return i18n.locale;
}
