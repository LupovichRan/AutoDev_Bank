// Wire types for the /api contract. Amounts are integer minor units.

export interface Me {
  principal_id: string;
  display_name: string;
  handle: string;
  kind: string;
  roles: string[];
}

export interface Holder {
  principal_id: string;
  display_name: string;
}

export interface Account {
  id: string;
  code: string;
  name: string;
  currency: string;
  kind: string;
  balance: number;
  available: number;
  pending_debits: number;
  pending_credits: number;
  holders: Holder[];
}

export interface Entry {
  id: string;
  transaction_id: string;
  amount: number;
  currency: string;
  balance_after: number;
  description: string;
  status: string;
  effective_date: string;
  created_at: string;
}

export interface Decision {
  approver_id: string;
  decision: string;
  at: string;
}

export interface Approval {
  id: string;
  kind: string;
  amount: number;
  currency: string;
  status: string;
  initiator: Holder;
  required_approvals: number;
  decisions: Decision[];
  subject: { from_account_id: string; to_account_id: string; description: string };
  created_at: string;
  expires_at: string;
  not_before?: string | null;
}

export interface TransferResult {
  transaction?: { id: string; status: string; [k: string]: unknown };
  approval?: { id: string; status: string; required_approvals: number; amount: number; currency: string; [k: string]: unknown };
}

export interface OpenDataRecord<D> {
  kind: string;
  key: string;
  provider: string;
  fetched_at: string;
  source_url: string;
  license: string;
  license_url?: string;
  data: D;
}

export interface OpenDataList<D> {
  records: OpenDataRecord<D>[];
  count: number;
}

export interface FxData {
  base: string;
  quote: string;
  rate: number;
  date: string;
  source: string;
}

export interface PolicyRateData {
  bank: string;
  name: string;
  rate: number;
  effective: string;
}

export interface PlaceData {
  bank: string;
  name: string;
  lat: number;
  lon: number;
  address: string;
  postcode: string;
  access24h: boolean;
  distance_km: number;
}

export interface ProductData {
  bank: string;
  brand: string;
  name: string;
  type: string;
  overdraft_ear: number | null;
  monthly_fee: number | null;
  url: string;
}

export interface ProviderStatus {
  provider: string;
  kind: string;
  ok: boolean;
  error: string;
  fetched_at: string;
  duration_ms: number;
}

export interface AuditVerify {
  ok: boolean;
  length: number;
  head_hash: string;
  first_bad_seq: number | null;
}

/** Public view of an audit event: no actor or subject; hour precision; action class only. */
export interface AuditEvent {
  seq: number;
  at: string;
  action: string;
  hash_prefix: string;
}

/** GET /api/config */
export interface AppConfig {
  dev_test_login: boolean;
  origin: string;
}

export interface FeeSchedule {
  currency_fx_markup_bp: number;
  monthly_fee: number; // minor units of `currency`
  currency?: string; // defaults to ILS
  income_share_percent: number;
  rules: { name: string; description: string }[];
}
