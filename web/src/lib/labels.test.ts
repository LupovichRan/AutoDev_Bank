import { describe, expect, it } from 'vitest';
import { detailCode, hasLabel, labelFor, labelValues, type LabelKind } from './labels';
import { en, isKey } from './messages';

describe('labelFor', () => {
  it('shows approval and transaction statuses in Hebrew', () => {
    const approval = ['pending', 'approved', 'rejected', 'executed', 'expired'].map((v) => labelFor('approvalStatus', v, 'he'));
    expect(approval).toEqual(['ממתין', 'אושר', 'נדחה', 'בוצע', 'פג תוקף']);
    const tx = ['posted', 'pending', 'voided'].map((v) => labelFor('txStatus', v, 'he'));
    expect(tx).toEqual(['נרשם', 'ממתין', 'בוטל']);
    expect(labelFor('decision', 'approve', 'he')).toBe('אישור');
    expect(labelFor('decision', 'reject', 'he')).toBe('דחייה');
  });

  it('calls a customer asset account "חשבון", not "נכס"', () => {
    expect(labelFor('accountKind', 'asset', 'he')).toBe('חשבון');
    expect(labelFor('accountKind', 'asset', 'en')).toBe('account');
    expect(labelFor('accountKind', 'equity', 'he')).toBe('הון');
  });

  it('names roles, principal kinds and audit action classes', () => {
    expect(labelFor('role', 'payments.initiator', 'he')).toBe('יוזם תשלומים');
    expect(labelFor('role', 'payments.approver', 'he')).toBe('מאשר תשלומים');
    expect(labelFor('role', 'staff.ops', 'he')).toBe('תפעול');
    expect(['customer', 'staff', 'service'].map((v) => labelFor('principalKind', v, 'he'))).toEqual(['לקוח', 'עובד', 'שירות']);
    expect(['auth', 'transfer', 'approval', 'seed', 'opendata'].map((v) => labelFor('auditAction', v, 'he'))).toEqual([
      'אימות',
      'העברה',
      'אישור',
      'נתוני דמו',
      'נתונים פתוחים',
    ]);
  });

  it('names open-data providers, keeping UK bank brands Latin', () => {
    expect(labelFor('provider', 'boi', 'he')).toBe('בנק ישראל');
    expect(labelFor('provider', 'ecb', 'he')).toBe('הבנק המרכזי האירופי');
    expect(labelFor('provider', 'boe', 'he')).toBe('הבנק המרכזי של אנגליה');
    expect(labelFor('provider', 'frankfurter', 'he')).toBe('Frankfurter (שערי ECB)');
    for (const [id, brand] of [
      ['natwest', 'NatWest'],
      ['lloyds', 'Lloyds'],
      ['barclays', 'Barclays'],
      ['hsbc', 'HSBC'],
      ['nationwide', 'Nationwide'],
    ]) {
      expect(labelFor('provider', id!, 'he')).toBe(brand);
      expect(labelFor('provider', id!, 'en')).toBe(brand);
    }
  });

  it('translates publisher strings in Hebrew and keeps them in English', () => {
    expect(labelFor('policyRate', 'Bank Rate', 'he')).toBe('ריבית הבנק המרכזי');
    expect(labelFor('policyRate', 'Bank of Israel interest rate', 'he')).toBe('ריבית בנק ישראל');
    expect(labelFor('policyRate', 'Main refinancing operations - fixed rate', 'he')).toBe('ריבית המימון העיקרית');
    expect(labelFor('policyRate', 'Bank Rate', 'en')).toBe('Bank Rate');
    expect(labelFor('centralBank', 'Bank of England', 'he')).toBe('הבנק המרכזי של אנגליה');
    expect(labelFor('fxSource', 'Bank of Israel', 'he')).toBe('בנק ישראל');
    expect(labelFor('fxSource', 'Frankfurter (ECB reference rates)', 'he')).toBe('Frankfurter (שערי ECB)');
    expect(labelFor('fxSource', 'European Central Bank', 'he')).toBe('הבנק המרכזי האירופי');
    expect(labelFor('fxSource', 'European Central Bank', 'en')).toBe('European Central Bank');
    expect(labelFor('productType', 'personal_current_account', 'he')).toBe('חשבון עו״ש פרטי');
    expect(labelFor('licence', 'natwest', 'he')).toBe('רישיון פתוח של Open Banking UK');
    expect(labelFor('licence', 'boi', 'he')).toBe('נתונים ציבוריים של בנק ישראל');
    expect(hasLabel('licence', 'boi', 'en')).toBe(false);
  });

  it('falls back to the raw value', () => {
    expect(labelFor('approvalStatus', 'on_hold', 'he')).toBe('on_hold');
    expect(labelFor('provider', 'newbank', 'he')).toBe('newbank');
    expect(labelFor('auditAction', 'toString', 'he')).toBe('toString');
    expect(labelFor('txStatus', undefined, 'he')).toBe('');
  });

  it('has an English label for every Hebrew enum value it translates', () => {
    const enums: LabelKind[] = ['approvalStatus', 'txStatus', 'decision', 'accountKind', 'role', 'principalKind', 'auditAction', 'provider', 'dataKind', 'productType'];
    for (const k of enums) {
      const v = labelValues(k);
      expect(v.en.sort(), k).toEqual(v.he.sort());
    }
  });
});

describe('detailCode', () => {
  it('reduces auth and open-data sentences to codes with a message', () => {
    const sentences = [
      'invalid handle',
      'handle already registered; sign in to add a passkey',
      'challenge not found or expired; start again',
      'passkey verification failed',
      'too many authentication attempts; try again later',
      'rate limited',
      'step-up required: confirm with your passkey first',
      'sign in required',
      'missing role staff.ops',
      'a refresh is already running; try again when it finishes',
    ];
    for (const s of sentences) {
      const code = detailCode(s);
      expect(code, s).not.toBe(s);
      expect(isKey(`problem.detail.${code}`), code).toBe(true);
    }
  });

  it('passes codes through, and every API code has a message', () => {
    const codes = [
      'step_up_required', 'sign_in_required', 'self_approval', 'not_authorized', 'not_pending', 'not_before',
      'already_decided', 'separation_of_duties', 'invalid_destination', 'not_found', 'idempotency_conflict',
      'insufficient_funds', 'currency_mismatch', 'period_closed', 'invalid_request', 'approve_required',
      'body_too_large', 'idempotency_key_required', 'invalid_day', 'invalid_json', 'invalid_limit', 'invalid_reason',
      'method_not_allowed', 'rate_limited', 'unsupported_media_type', 'invalid_account', 'invalid_amount',
      'invalid_currency', 'invalid_description', 'internal_error', 'invalid_lang', 'handle_taken', 'invalid_handle',
    ];
    for (const c of codes) {
      expect(detailCode(c)).toBe(c);
      expect(Object.hasOwn(en, `problem.detail.${c}`), c).toBe(true);
    }
  });
});
