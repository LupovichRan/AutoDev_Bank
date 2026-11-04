// Money helpers. Every amount in this app is an integer count of minor units
// (cents, agorot, pence); decimals exist only at the edges: formatting for
// display and parsing what the user typed. No floating point arithmetic.

const FIXED_EXPONENTS: Record<string, number> = { ILS: 2, EUR: 2, GBP: 2, USD: 2 };

function defaultLocale(): string {
  if (typeof navigator !== 'undefined' && navigator.language) return navigator.language;
  return 'en-GB';
}

/** Number of minor-unit digits for an ISO 4217 currency (2 for ILS/EUR/GBP/USD). */
export function minorExponent(currency: string): number {
  const code = currency.toUpperCase();
  const fixed = FIXED_EXPONENTS[code];
  if (fixed !== undefined) return fixed;
  const opts = new Intl.NumberFormat('en', { style: 'currency', currency: code }).resolvedOptions();
  return opts.maximumFractionDigits ?? 2;
}

/** Exact decimal string ("-12.34") for an integer number of minor units. */
export function minorToDecimalString(amount: bigint | number, currency: string): string {
  if (typeof amount === 'number' && !Number.isSafeInteger(amount)) {
    throw new RangeError(`amount must be a safe integer of minor units, got ${amount}`);
  }
  const exp = minorExponent(currency);
  const n = BigInt(amount);
  const neg = n < 0n;
  const digits = (neg ? -n : n).toString().padStart(exp + 1, '0');
  const int = exp === 0 ? digits : digits.slice(0, -exp);
  const frac = exp === 0 ? '' : `.${digits.slice(-exp)}`;
  return `${neg ? '-' : ''}${int}${frac}`;
}

/** Format integer minor units as a localized currency string. */
export function formatMinor(amount: bigint | number, currency: string, locale?: string): string {
  const exp = minorExponent(currency);
  const fmt = new Intl.NumberFormat(locale ?? defaultLocale(), {
    style: 'currency',
    currency: currency.toUpperCase(),
    minimumFractionDigits: exp,
    maximumFractionDigits: exp,
  });
  // Intl accepts exact decimal strings, so precision survives beyond 2^53.
  return fmt.format(minorToDecimalString(amount, currency) as Intl.StringNumericLiteral);
}

const DECIMAL = /^(-)?(\d*)(?:\.(\d+))?$/;
const GROUPED = /^-?\d{1,3}(,\d{3})+(\.\d+)?$/;

/**
 * Parse a user-typed decimal ("12.5", "1,234.56") into integer minor units.
 * Throws on anything ambiguous: exponents, misplaced separators, or more
 * decimals than the currency has.
 */
export function parseToMinor(input: string, currency: string): number {
  let s = input.trim();
  if (s.includes(',')) {
    if (!GROUPED.test(s)) throw new SyntaxError(`invalid amount: "${input}"`);
    s = s.replaceAll(',', '');
  }
  const m = DECIMAL.exec(s);
  if (!m) throw new SyntaxError(`invalid amount: "${input}"`);
  const [, sign, int = '', frac] = m;
  if (int === '' && (frac === undefined || frac === '')) throw new SyntaxError(`invalid amount: "${input}"`);
  const exp = minorExponent(currency);
  if (frac !== undefined && frac.length > exp) {
    throw new RangeError(`${currency} allows at most ${exp} decimal places`);
  }
  const minor = BigInt((int || '0') + (frac ?? '').padEnd(exp, '0'));
  const value = sign ? -minor : minor;
  if (value > BigInt(Number.MAX_SAFE_INTEGER) || value < BigInt(Number.MIN_SAFE_INTEGER)) {
    throw new RangeError('amount is too large');
  }
  return Number(value);
}
