// Dynamic linking: the step-up challenge is bound to the exact transfer the
// user confirmed. The server computes the same hash over the same canonical
// JSON, so any field changed after step-up invalidates the authorisation.

export interface TransferBody {
  from_account_id: string;
  to_account_id: string;
  amount: number;
  currency: string;
  description: string;
}

const KEYS = ['amount', 'currency', 'description', 'from_account_id', 'to_account_id'] as const;

/** Compact JSON with keys in lexicographic order; only the five signed fields. */
export function canonicalTransferJSON(body: TransferBody): string {
  const ordered: Record<string, unknown> = {};
  for (const k of KEYS) ordered[k] = body[k];
  return JSON.stringify(ordered);
}

export function toHex(buf: ArrayBuffer): string {
  return Array.from(new Uint8Array(buf), (b) => b.toString(16).padStart(2, '0')).join('');
}

/** Lowercase hex SHA-256 of a UTF-8 string. */
export async function sha256Hex(s: string): Promise<string> {
  return toHex(await crypto.subtle.digest('SHA-256', new TextEncoder().encode(s)));
}

/** The step-up payload hash that authorises approving an approval: sha256("approve:" + id). */
export function approvePayloadHash(approvalId: string): Promise<string> {
  return sha256Hex(`approve:${approvalId}`);
}

/** Lowercase hex SHA-256 of the canonical transfer JSON. */
export async function payloadHash(body: TransferBody): Promise<string> {
  const bytes = new TextEncoder().encode(canonicalTransferJSON(body));
  return toHex(await crypto.subtle.digest('SHA-256', bytes));
}
