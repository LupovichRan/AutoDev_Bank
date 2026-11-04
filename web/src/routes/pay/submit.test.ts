import { describe, expect, it, vi } from 'vitest';
import type { Outcome } from '../../lib/transfer';
import { SubmitFlow } from './submit.svelte';

const body = { from_account_id: 'a', to_account_id: 'b', amount: 1000, currency: 'EUR', description: 'x' };
const sent: Outcome = { kind: 'sent', transactionId: 't1', status: 'posted' };

function deferred<T>() {
  let resolve!: (v: T) => void;
  const promise = new Promise<T>((r) => (resolve = r));
  return { promise, resolve };
}

describe('SubmitFlow', () => {
  it('double submit before the digest resolves mints one key', async () => {
    const digest = deferred<string>();
    const keys: string[] = [];
    const flow = new SubmitFlow({
      newAttempt: async (b) => {
        const key = `k${keys.length}`;
        keys.push(key);
        return { key, body: b, hash: await digest.promise, steppedUp: false };
      },
      stepUp: vi.fn(async () => {}),
      submit: vi.fn(async () => sent),
    });
    const first = flow.submit(body);
    const second = flow.submit(body);
    expect(flow.phase).toBe('stepup');
    digest.resolve('h');
    await Promise.all([first, second]);
    expect(keys).toHaveLength(1);
    expect(flow.outcome).toEqual(sent);
  });

  it('calls step-up exactly once for two submits', async () => {
    const digest = deferred<string>();
    const stepUp = vi.fn(async () => {});
    const flow = new SubmitFlow({
      newAttempt: async (b) => ({ key: 'k', body: b, hash: await digest.promise, steppedUp: false }),
      stepUp,
      submit: async (attempt, step) => {
        await step(attempt.hash);
        return sent;
      },
    });
    const p = [flow.submit(body), flow.submit(body)];
    digest.resolve('h');
    await Promise.all(p);
    expect(stepUp).toHaveBeenCalledTimes(1);
  });

  it('keeps the same key on retry after a failure and resets phase', async () => {
    const minted: string[] = [];
    const used: string[] = [];
    let fail = true;
    const flow = new SubmitFlow({
      newAttempt: async (b) => {
        const key = `k${minted.length}`;
        minted.push(key);
        return { key, body: b, hash: 'h', steppedUp: false };
      },
      stepUp: async () => {},
      submit: async (attempt) => {
        used.push(attempt.key);
        if (fail) throw new Error('boom');
        return sent;
      },
    });
    await flow.submit(body);
    expect(flow.phase).toBe('idle');
    expect(flow.submitError).toBeInstanceOf(Error);
    fail = false;
    await flow.submit(body);
    expect(minted).toEqual(['k0']);
    expect(used).toEqual(['k0', 'k0']);
    expect(flow.outcome).toEqual(sent);
  });
});
