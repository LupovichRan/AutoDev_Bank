<script lang="ts">
  import Amount from '../components/Amount.svelte';
  import Card from '../components/Card.svelte';
  import Problem from '../components/Problem.svelte';
  import { apiGet, NetworkError, ProblemError } from '../lib/api';
  import { formatMinor, parseToMinor } from '../lib/money';
  import { connectivity } from '../lib/online.svelte';
  import type { TransferBody } from '../lib/payload';
  import { href } from '../lib/router';
  import { status } from '../lib/status.svelte';
  import { isStepUpRequired, newAttempt, sameBody, submitTransfer, type Attempt, type Outcome } from '../lib/transfer';
  import type { Account, Me } from '../lib/types';
  import { describeWebAuthnError, stepUp } from '../lib/webauthn';

  interface Props {
    me: Me;
    params: Record<string, string>;
  }
  let { me, params }: Props = $props();

  let accounts = $state<Account[] | null>(null);
  let loadError = $state<unknown>(null);

  // Form state
  let fromId = $state('');
  let toMode = $state<'own' | 'id'>('own');
  let toOwnId = $state('');
  let toIdText = $state('');
  let amountText = $state('');
  let description = $state('');
  let errors = $state<Record<string, string>>({});

  // Submission state
  let phase = $state<'idle' | 'stepup' | 'sending'>('idle');
  let attempt: Attempt | null = null;
  let submitError = $state<unknown>(null);
  let canRetry = $state(false);
  let outcome = $state<Outcome | null>(null);
  let sentSummary = $state('');

  const from = $derived(accounts?.find((a) => a.id === fromId) ?? null);
  const others = $derived(accounts?.filter((a) => a.id !== fromId) ?? []);
  const toId = $derived(toMode === 'own' ? toOwnId : toIdText.trim());
  const toName = $derived(toMode === 'own' ? (accounts?.find((a) => a.id === toOwnId)?.name ?? '') : toIdText.trim());
  const previewMinor = $derived.by(() => {
    if (!from) return null;
    try {
      const v = parseToMinor(amountText, from.currency);
      return v > 0 ? v : null;
    } catch {
      return null;
    }
  });

  async function load() {
    loadError = null;
    try {
      accounts = (await apiGet<{ accounts: Account[] }>('/api/accounts')).accounts;
      const pre = params['from'];
      fromId = accounts.find((a) => a.id === pre)?.id ?? accounts[0]?.id ?? '';
      const to = params['to'];
      if (to) {
        if (accounts.some((a) => a.id === to && a.id !== fromId)) {
          toMode = 'own';
          toOwnId = to;
        } else {
          toMode = 'id';
          toIdText = to;
        }
      }
      if (toMode === 'own' && !toOwnId) toOwnId = accounts.find((a) => a.id !== fromId)?.id ?? '';
      if (toMode === 'own' && accounts.length < 2) toMode = 'id';
    } catch (e) {
      loadError = e;
    }
  }
  load();

  // Keep the "to own account" choice valid when "from" changes.
  $effect(() => {
    if (toMode === 'own' && (toOwnId === fromId || !others.some((a) => a.id === toOwnId))) {
      toOwnId = others[0]?.id ?? '';
    }
  });

  function validate(): TransferBody | null {
    const e: Record<string, string> = {};
    let amount = 0;
    if (!from) e['from'] = 'Choose an account to pay from.';
    if (!toId) e['to'] = 'Enter or choose the account to pay.';
    else if (toId === fromId) e['to'] = 'Choose a different account from the one you pay from.';
    if (from) {
      try {
        amount = parseToMinor(amountText, from.currency);
        if (amount <= 0) e['amount'] = 'Enter an amount greater than zero.';
      } catch (err) {
        e['amount'] = err instanceof RangeError && /decimal/.test(err.message)
          ? `${from.currency} amounts have at most 2 decimal places.`
          : 'Enter an amount like 12.50.';
      }
    }
    if (description.length > 140) e['description'] = 'Keep the description to 140 characters.';
    errors = e;
    if (Object.keys(e).length || !from) return null;
    return { from_account_id: from.id, to_account_id: toId, amount, currency: from.currency, description: description.trim() };
  }

  async function run() {
    if (!attempt) return;
    submitError = null;
    canRetry = false;
    try {
      phase = attempt.steppedUp ? 'sending' : 'stepup';
      const current = attempt;
      const result = await submitTransfer(current, async (hash) => {
        try {
          await stepUp(hash);
        } catch (e) {
          throw e instanceof ProblemError || e instanceof NetworkError ? e : new Error(describeWebAuthnError(e));
        }
        phase = 'sending';
      });
      outcome = result;
      sentSummary = `${formatMinor(current.body.amount, current.body.currency)} to ${toName}`;
      attempt = null;
      status.announce(
        result.kind === 'sent'
          ? `Sent ${sentSummary}.`
          : `Awaiting approval: ${result.requiredApprovals} approver${result.requiredApprovals === 1 ? '' : 's'} required.`,
      );
    } catch (e) {
      submitError = e;
      // Same key on retry: the server de-duplicates, so retrying cannot pay twice.
      canRetry = e instanceof NetworkError || isStepUpRequired(e);
      status.announce('Payment not sent.');
    } finally {
      phase = 'idle';
    }
  }

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    const body = validate();
    if (!body) {
      status.announce('Please fix the highlighted fields.');
      return;
    }
    // Reuse the pending attempt (and its key) if the user re-submits the same payment.
    if (!attempt || !sameBody(attempt.body, body)) attempt = await newAttempt(body);
    await run();
  }

  function reset() {
    outcome = null;
    attempt = null;
    submitError = null;
    canRetry = false;
    amountText = '';
    description = '';
  }

  const busy = $derived(phase !== 'idle');
</script>

<h1>Pay</h1>

{#if loadError}
  <Problem error={loadError} onretry={load} />
{:else if accounts === null}
  <p aria-busy="true">Loading accounts…</p>
{:else if outcome}
  <Card title={outcome.kind === 'sent' ? 'Sent' : 'Awaiting approval'} id="outcome">
    {#if outcome.kind === 'sent'}
      <p class="ok big">Sent {sentSummary}.</p>
      <p class="muted">Transaction <span class="mono">{outcome.transactionId}</span> · {outcome.status}</p>
    {:else}
      <p class="warn big">
        Awaiting approval ({outcome.requiredApprovals} approver{outcome.requiredApprovals === 1 ? '' : 's'} required).
      </p>
      <p>{sentSummary} will be sent once approved. Track it in <a href={href('/approvals')}>Approvals</a>.</p>
      <p class="muted">Approval <span class="mono">{outcome.approvalId}</span></p>
    {/if}
    <div class="row">
      <button type="button" onclick={reset}>Make another payment</button>
      <a class="button secondary" href={href('/')}>Back to Home</a>
    </div>
  </Card>
{:else if accounts.length === 0}
  <p>You need an account before you can pay.</p>
{:else}
  <form onsubmit={onSubmit} novalidate aria-describedby="pay-note">
    <p id="pay-note" class="muted">Every payment is confirmed with your passkey. The confirmation is bound to exactly these details.</p>

    <div class="field">
      <label for="from">From</label>
      <select id="from" bind:value={fromId} disabled={busy} aria-invalid={errors['from'] ? 'true' : undefined}>
        {#each accounts as a (a.id)}
          <option value={a.id}>{a.name} ({a.code}) · {formatMinor(a.available, a.currency)} available</option>
        {/each}
      </select>
      {#if from}<p class="hint">Available: <Amount amount={from.available} currency={from.currency} /></p>{/if}
    </div>

    <fieldset class="field" disabled={busy}>
      <legend>To</legend>
      <div class="row choice">
        <label class="inline"><input type="radio" bind:group={toMode} value="own" disabled={others.length === 0} /> One of my accounts</label>
        <label class="inline"><input type="radio" bind:group={toMode} value="id" /> Another account id</label>
      </div>
      {#if toMode === 'own'}
        <label for="to-own" class="visually-hidden">Destination account</label>
        <select id="to-own" bind:value={toOwnId} aria-invalid={errors['to'] ? 'true' : undefined} aria-describedby={errors['to'] ? 'to-error' : undefined}>
          {#each others as a (a.id)}
            <option value={a.id}>{a.name} ({a.code})</option>
          {/each}
        </select>
      {:else}
        <label for="to-id">Destination account id</label>
        <input
          id="to-id"
          bind:value={toIdText}
          autocomplete="off"
          autocapitalize="none"
          spellcheck="false"
          aria-invalid={errors['to'] ? 'true' : undefined}
          aria-describedby={errors['to'] ? 'to-error' : undefined}
        />
      {/if}
      {#if errors['to']}<p class="field-error" id="to-error">{errors['to']}</p>{/if}
    </fieldset>

    <div class="field">
      <label for="amount">Amount{from ? ` (${from.currency})` : ''}</label>
      <input
        id="amount"
        inputmode="decimal"
        autocomplete="off"
        placeholder="0.00"
        bind:value={amountText}
        disabled={busy}
        aria-invalid={errors['amount'] ? 'true' : undefined}
        aria-describedby={errors['amount'] ? 'amount-error' : undefined}
      />
      {#if errors['amount']}<p class="field-error" id="amount-error">{errors['amount']}</p>{/if}
    </div>

    <div class="field">
      <label for="description">Description <span class="muted">(optional)</span></label>
      <input
        id="description"
        bind:value={description}
        maxlength="140"
        disabled={busy}
        aria-invalid={errors['description'] ? 'true' : undefined}
        aria-describedby={errors['description'] ? 'description-error' : undefined}
      />
      {#if errors['description']}<p class="field-error" id="description-error">{errors['description']}</p>{/if}
    </div>

    {#if previewMinor !== null && from && toId}
      <p class="summary">You are paying <strong><Amount amount={previewMinor} currency={from.currency} /></strong> to <strong>{toName}</strong>.</p>
    {/if}

    {#if submitError}<Problem error={submitError} />{/if}

    <div class="row">
      <button type="submit" disabled={busy || !connectivity.online} aria-busy={busy}>
        {#if phase === 'stepup'}Confirm with your passkey…{:else if phase === 'sending'}Sending…{:else if canRetry}Retry payment{:else}Confirm and pay{/if}
      </button>
      {#if !connectivity.online}<span class="muted">Payments need a connection.</span>{/if}
    </div>
    <p class="hint">Signed in as {me.display_name}.</p>
  </form>
{/if}

<style>
  form {
    max-width: 36rem;
  }
  fieldset {
    border: 0;
    padding: 0;
    margin: 0 0 var(--space-4);
    min-width: 0;
  }
  legend {
    font-weight: 600;
    padding: 0;
    margin-bottom: var(--space-1);
  }
  .choice {
    margin-bottom: var(--space-2);
  }
  label.inline {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    font-weight: 400;
    min-height: 44px;
    margin: 0 var(--space-3) 0 0;
  }
  label.inline input {
    width: 20px;
    height: 20px;
    min-height: 0;
  }
  .summary {
    background: var(--color-surface);
    border-radius: var(--radius-md);
    padding: var(--space-3);
  }
  .big {
    font-size: var(--text-lg);
    font-weight: 700;
  }
</style>
