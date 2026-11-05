// Display names for values that come from the server: statuses, kinds,
// roles, audit action classes, open-data providers, publishers and licences,
// and problem-detail codes. Plain module (no runes) so unit tests and the
// Playwright specs can import it; i18n.svelte.ts binds it to the UI language.
// Unknown values always fall back to the raw string. Free text typed by
// people (entry descriptions, account names) is never translated here.
import type { Lang } from './messages';

type Table = Record<Lang, Record<string, string>>;

const LABELS = {
  /** control.approvals.status */
  approvalStatus: {
    he: { pending: 'ממתין', approved: 'אושר', rejected: 'נדחה', executed: 'בוצע', expired: 'פג תוקף' },
    en: { pending: 'pending', approved: 'approved', rejected: 'rejected', executed: 'executed', expired: 'expired' },
  },
  /** ledger.transactions.status */
  txStatus: {
    he: { posted: 'נרשם', pending: 'ממתין', voided: 'בוטל' },
    en: { posted: 'posted', pending: 'pending', voided: 'voided' },
  },
  /** approval decisions */
  decision: {
    he: { approve: 'אישור', approved: 'אישור', reject: 'דחייה', rejected: 'דחייה' },
    en: { approve: 'approval', approved: 'approval', reject: 'rejection', rejected: 'rejection' },
  },
  /**
   * ledger.accounts.kind as a customer sees it: a customer account is
   * modelled as an asset, but on a card it is simply "an account".
   */
  accountKind: {
    he: { asset: 'חשבון', liability: 'התחייבות', equity: 'הון', income: 'הכנסה', expense: 'הוצאה' },
    en: { asset: 'account', liability: 'liability', equity: 'equity', income: 'income', expense: 'expense' },
  },
  role: {
    he: { 'payments.initiator': 'יוזם תשלומים', 'payments.approver': 'מאשר תשלומים', 'staff.ops': 'תפעול' },
    en: { 'payments.initiator': 'payments initiator', 'payments.approver': 'payments approver', 'staff.ops': 'operations' },
  },
  principalKind: {
    he: { customer: 'לקוח', staff: 'עובד', service: 'שירות' },
    en: { customer: 'customer', staff: 'staff', service: 'service' },
  },
  /** Public audit feed: the action class before the first dot. */
  auditAction: {
    he: { auth: 'אימות', transfer: 'העברה', approval: 'אישור', seed: 'נתוני דמו', opendata: 'נתונים פתוחים' },
    en: { auth: 'sign-in', transfer: 'transfer', approval: 'approval', seed: 'demo data', opendata: 'open data' },
  },
  /** Open-data provider ids. UK bank brands stay Latin in both languages. */
  provider: {
    he: {
      boi: 'בנק ישראל',
      ecb: 'הבנק המרכזי האירופי',
      boe: 'הבנק המרכזי של אנגליה',
      frankfurter: 'Frankfurter (שערי ECB)',
      natwest: 'NatWest',
      lloyds: 'Lloyds',
      barclays: 'Barclays',
      hsbc: 'HSBC',
      nationwide: 'Nationwide',
      enablebanking: 'Enable Banking',
    },
    en: {
      boi: 'Bank of Israel',
      ecb: 'European Central Bank',
      boe: 'Bank of England',
      frankfurter: 'Frankfurter (ECB rates)',
      natwest: 'NatWest',
      lloyds: 'Lloyds',
      barclays: 'Barclays',
      hsbc: 'HSBC',
      nationwide: 'Nationwide',
      enablebanking: 'Enable Banking',
    },
  },
  /** Open-data record kinds (provider health list). */
  dataKind: {
    he: {
      fx_rate: 'שערי חליפין',
      policy_rate: 'ריבית בנק מרכזי',
      atm: 'כספומטים',
      branch: 'סניפים',
      product: 'מוצרים',
      own_account: 'חשבונות שלי',
    },
    en: {
      fx_rate: 'exchange rates',
      policy_rate: 'policy rates',
      atm: 'ATMs',
      branch: 'branches',
      product: 'products',
      own_account: 'own accounts',
    },
  },
  /** policy_rate data.name */
  policyRate: {
    he: {
      'Bank Rate': 'ריבית הבנק המרכזי',
      'Bank of Israel interest rate': 'ריבית בנק ישראל',
      'Main refinancing operations - fixed rate': 'ריבית המימון העיקרית',
    },
    en: {},
  },
  /** policy_rate data.bank */
  centralBank: {
    he: {
      'Bank of Israel': 'בנק ישראל',
      'European Central Bank': 'הבנק המרכזי האירופי',
      'Bank of England': 'הבנק המרכזי של אנגליה',
    },
    en: {},
  },
  /** fx_rate data.source */
  fxSource: {
    he: {
      'Bank of Israel': 'בנק ישראל',
      'Frankfurter (ECB reference rates)': 'Frankfurter (שערי ECB)',
      'European Central Bank': 'הבנק המרכזי האירופי',
    },
    en: {},
  },
  /** product data.type */
  productType: {
    he: { personal_current_account: 'חשבון עו״ש פרטי' },
    en: { personal_current_account: 'Personal current account' },
  },
  /**
   * Licence attribution per provider id. The publisher's own wording (shown
   * in English) and the link to its terms remain authoritative.
   */
  licence: {
    he: {
      boi: 'נתונים ציבוריים של בנק ישראל',
      ecb: 'פורטל הנתונים של ה-ECB',
      boe: 'מאגר הנתונים של הבנק המרכזי של אנגליה',
      frankfurter: 'Frankfurter — שערי ייחוס של ה-ECB',
      natwest: 'רישיון פתוח של Open Banking UK',
      lloyds: 'רישיון פתוח של Open Banking UK',
      barclays: 'רישיון פתוח של Open Banking UK',
      hsbc: 'רישיון פתוח של Open Banking UK',
      nationwide: 'רישיון פתוח של Open Banking UK',
      enablebanking: 'Enable Banking AIS (פרטי, החשבונות שלך)',
    },
    en: {},
  },
} satisfies Record<string, Table>;

export type LabelKind = keyof typeof LABELS;

/** The display name of a server value in `lang`; unknown values are returned as sent. */
export function labelFor(kind: LabelKind, value: string | null | undefined, lang: Lang): string {
  if (value == null) return '';
  const table: Record<string, string> = LABELS[kind][lang];
  return Object.hasOwn(table, value) ? table[value]! : value;
}

/** Whether `value` has its own label in `lang` (not just the raw fallback). */
export function hasLabel(kind: LabelKind, value: string, lang: Lang): boolean {
  return Object.hasOwn(LABELS[kind][lang], value);
}

/** Every known value of `kind`, for tests that check both languages agree. */
export function labelValues(kind: LabelKind): { he: string[]; en: string[] } {
  return { he: Object.keys(LABELS[kind].he), en: Object.keys(LABELS[kind].en) };
}

/**
 * The auth and open-data handlers send English sentences instead of codes.
 * detailCode reduces those to a stable code so the UI can show its own
 * text (messages.ts `problem.detail.<code>`); codes pass through unchanged.
 */
const DETAIL_SENTENCES: [RegExp, string][] = [
  [/^sign in required$/, 'sign_in_required'],
  [/^step-up required/, 'step_up_required'],
  [/^invalid handle$/, 'invalid_handle'],
  [/^handle must be /, 'invalid_handle'],
  [/^handle already registered/, 'handle_taken'],
  [/^this passkey is already registered$/, 'passkey_exists'],
  [/^challenge not found/, 'challenge_not_found'],
  [/^passkey verification failed$/, 'passkey_verification_failed'],
  [/^payload_hash must be /, 'invalid_payload_hash'],
  [/^display_name is too long$/, 'display_name_too_long'],
  [/^unknown principal$/, 'unknown_principal'],
  [/^not found$/, 'not_found'],
  [/^internal error$/, 'internal_error'],
  [/^invalid JSON body$/, 'invalid_json'],
  [/^missing role /, 'not_authorized'],
  [/^too many authentication attempts/, 'rate_limited'],
  [/^rate limited$/, 'rate_limited'],
  [/^a refresh is already running/, 'refresh_running'],
  [/^no refresher is configured$/, 'refresh_unavailable'],
  [/^the refresh could not be run$/, 'refresh_failed'],
  [/^the (open data cache|refresh log) could not be read$/, 'opendata_unavailable'],
];

export function detailCode(detail: string): string {
  for (const [re, code] of DETAIL_SENTENCES) if (re.test(detail)) return code;
  return detail;
}
