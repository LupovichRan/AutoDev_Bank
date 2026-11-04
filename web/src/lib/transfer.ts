// Transfer submission with step-up and idempotent retry.
//
// One Attempt = one Idempotency-Key = one intended payment. If the network
// drops after the request leaves, re-sending the same Attempt can never pay
// twice: the server replays the stored result for a known key.
import { apiPostWithStatus, ProblemError } from './api';
import { t } from './i18n.svelte';
import { payloadHash, type TransferBody } from './payload';
import type { TransferResult } from './types';

export interface Attempt {
  key: string;
  body: TransferBody;
  hash: string;
  steppedUp: boolean;
}

export type Outcome =
  | { kind: 'sent'; transactionId: string; status: string }
  | { kind: 'pending'; approvalId: string; requiredApprovals: number; status: string };

export async function newAttempt(body: TransferBody): Promise<Attempt> {
  return { key: crypto.randomUUID(), body, hash: await payloadHash(body), steppedUp: false };
}

export function sameBody(a: TransferBody, b: TransferBody): boolean {
  return (
    a.from_account_id === b.from_account_id &&
    a.to_account_id === b.to_account_id &&
    a.amount === b.amount &&
    a.currency === b.currency &&
    a.description === b.description
  );
}

export function isStepUpRequired(e: unknown): boolean {
  return e instanceof ProblemError && e.status === 403 && e.detail === 'step_up_required';
}

export function toOutcome(status: number, data: TransferResult): Outcome {
  if (status === 202 && data.approval) {
    return {
      kind: 'pending',
      approvalId: data.approval.id,
      requiredApprovals: data.approval.required_approvals,
      status: data.approval.status,
    };
  }
  if (data.transaction) return { kind: 'sent', transactionId: data.transaction.id, status: data.transaction.status };
  if (data.approval) {
    return { kind: 'pending', approvalId: data.approval.id, requiredApprovals: data.approval.required_approvals, status: data.approval.status };
  }
  throw new Error(t('transfer.unexpected'));
}

/**
 * Step up (unless this attempt already did) and send. `stepUp` performs the
 * passkey ceremony for the payload hash. Throws NetworkError when the
 * connection drops; the caller keeps the Attempt and calls again to retry
 * with the same key.
 */
export async function submitTransfer(attempt: Attempt, stepUp: (hash: string) => Promise<unknown>): Promise<Outcome> {
  if (!attempt.steppedUp) {
    await stepUp(attempt.hash);
    attempt.steppedUp = true;
  }
  try {
    const { status, data } = await apiPostWithStatus<TransferResult>('/api/transfers', attempt.body, {
      'Idempotency-Key': attempt.key,
    });
    return toOutcome(status, data);
  } catch (e) {
    // Step-up expired (or was never recorded): the next call re-authenticates.
    if (isStepUpRequired(e)) attempt.steppedUp = false;
    throw e;
  }
}
