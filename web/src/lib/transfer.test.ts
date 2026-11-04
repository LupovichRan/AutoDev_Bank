import { afterEach, describe, expect, it, vi } from 'vitest';
import { NetworkError, ProblemError } from './api';
import { canonicalTransferJSON, payloadHash } from './payload';
import { newAttempt, submitTransfer, toOutcome } from './transfer';

afterEach(() => vi.unstubAllGlobals());

const body = { from_account_id: 'a', to_account_id: 'b', amount: 1000, currency: 'EUR', description: 'x' };

function json(status: number, data: unknown, problem = false) {
  return new Response(JSON.stringify(data), {
    status,
    headers: { 'content-type': problem ? 'application/problem+json' : 'application/json' },
  });
}

describe('submitTransfer', () => {
  it('steps up with the payload hash before sending, with an idempotency key', async () => {
    const order: string[] = [];
    const fetchMock = vi.fn(async (_url: string, init: RequestInit) => {
      order.push('fetch');
      expect((init.headers as Record<string, string>)['Idempotency-Key']).toMatch(/^[0-9a-f-]{36}$/);
      expect(init.body).toBe(JSON.stringify(body));
      return json(201, { transaction: { id: 'tx1', status: 'posted' } });
    });
    vi.stubGlobal('fetch', fetchMock);
    const attempt = await newAttempt(body);
    expect(attempt.hash).toBe(await payloadHash(body));
    const stepUp = vi.fn(async (h: string) => {
      order.push(`stepup:${h}`);
    });
    const out = await submitTransfer(attempt, stepUp);
    expect(out).toEqual({ kind: 'sent', transactionId: 'tx1', status: 'posted' });
    expect(order).toEqual([`stepup:${attempt.hash}`, 'fetch']);
    expect(canonicalTransferJSON(body)).toContain('"amount":1000');
  });

  it('retries a dropped request with the same key and without a second step-up', async () => {
    const keys: string[] = [];
    let calls = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async (_u: string, init: RequestInit) => {
        keys.push((init.headers as Record<string, string>)['Idempotency-Key']!);
        if (calls++ === 0) throw new TypeError('Failed to fetch');
        return json(202, { approval: { id: 'ap1', status: 'pending', required_approvals: 2, amount: 1000, currency: 'EUR' } });
      }),
    );
    const attempt = await newAttempt(body);
    const stepUp = vi.fn(async () => {});
    await expect(submitTransfer(attempt, stepUp)).rejects.toBeInstanceOf(NetworkError);
    const out = await submitTransfer(attempt, stepUp);
    expect(out).toEqual({ kind: 'pending', approvalId: 'ap1', requiredApprovals: 2, status: 'pending' });
    expect(keys).toHaveLength(2);
    expect(keys[0]).toBe(keys[1]);
    expect(stepUp).toHaveBeenCalledTimes(1);
  });

  it('re-steps-up after step_up_required, keeping the key', async () => {
    let calls = 0;
    vi.stubGlobal(
      'fetch',
      vi.fn(async () =>
        calls++ === 0
          ? json(403, { type: 'about:blank', title: 'Forbidden', status: 403, detail: 'step_up_required' }, true)
          : json(201, { transaction: { id: 'tx2', status: 'posted' } }),
      ),
    );
    const attempt = await newAttempt(body);
    const key = attempt.key;
    const stepUp = vi.fn(async () => {});
    await expect(submitTransfer(attempt, stepUp)).rejects.toBeInstanceOf(ProblemError);
    expect(attempt.steppedUp).toBe(false);
    await submitTransfer(attempt, stepUp);
    expect(stepUp).toHaveBeenCalledTimes(2);
    expect(attempt.key).toBe(key);
  });

  it('does not send when step-up fails', async () => {
    const fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    const attempt = await newAttempt(body);
    await expect(submitTransfer(attempt, async () => Promise.reject(new Error('cancelled')))).rejects.toThrow('cancelled');
    expect(fetchMock).not.toHaveBeenCalled();
  });
});

describe('toOutcome', () => {
  it('rejects an empty response', () => {
    expect(() => toOutcome(200, {})).toThrow();
  });
});
