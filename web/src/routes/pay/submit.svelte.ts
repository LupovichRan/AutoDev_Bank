// Submit flow for a payment, kept out of the component so it is unit-testable.
// A synchronous guard (`#busy`) is taken before any await, so a double click
// cannot mint two Attempts (two idempotency keys) for one intended payment.
import { NetworkError } from '../../lib/api';
import type { TransferBody } from '../../lib/payload';
import { isStepUpRequired, newAttempt, sameBody, submitTransfer, type Attempt, type Outcome } from '../../lib/transfer';

export interface SubmitDeps {
  newAttempt: (body: TransferBody) => Promise<Attempt>;
  /** Passkey ceremony for the payload hash. */
  stepUp: (hash: string) => Promise<unknown>;
  submit: (attempt: Attempt, stepUp: (hash: string) => Promise<unknown>) => Promise<Outcome>;
}

export class SubmitFlow {
  phase = $state<'idle' | 'stepup' | 'sending'>('idle');
  submitError = $state<unknown>(null);
  canRetry = $state(false);
  outcome = $state<Outcome | null>(null);
  sentBody = $state<TransferBody | null>(null);

  #attempt: Attempt | null = null;
  #busy = false;
  #deps: Pick<SubmitDeps, 'stepUp'> & Partial<SubmitDeps>;

  constructor(deps: Pick<SubmitDeps, 'stepUp'> & Partial<SubmitDeps>) {
    this.#deps = deps;
  }

  get busy(): boolean {
    return this.phase !== 'idle';
  }

  /** Returns false if ignored because a submit is already in flight. */
  async submit(
    body: TransferBody,
    onDone?: (o: Outcome, b: TransferBody) => void,
    onFail?: (e: unknown) => void,
  ): Promise<boolean> {
    if (this.#busy) return false; // synchronous: nothing awaited before this
    this.#busy = true;
    this.phase = 'stepup';
    this.submitError = null;
    this.canRetry = false;
    try {
      // Reuse the pending attempt (and its key) if the user re-submits the same payment.
      if (!this.#attempt || !sameBody(this.#attempt.body, body)) {
        this.#attempt = await (this.#deps.newAttempt ?? newAttempt)(body);
      }
      const current = this.#attempt;
      if (current.steppedUp) this.phase = 'sending';
      const result = await (this.#deps.submit ?? submitTransfer)(current, async (hash) => {
        await this.#deps.stepUp(hash);
        this.phase = 'sending';
      });
      this.outcome = result;
      this.sentBody = current.body;
      this.#attempt = null;
      onDone?.(result, current.body);
    } catch (e) {
      this.submitError = e;
      // Same key on retry: the server de-duplicates, so retrying cannot pay twice.
      this.canRetry = e instanceof NetworkError || isStepUpRequired(e);
      onFail?.(e);
    } finally {
      this.phase = 'idle';
      this.#busy = false;
    }
    return true;
  }

  reset(): void {
    if (this.#busy) return;
    this.outcome = null;
    this.sentBody = null;
    this.#attempt = null;
    this.submitError = null;
    this.canRetry = false;
  }
}
