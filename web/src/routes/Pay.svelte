<script lang="ts">
  import Amount from '../components/Amount.svelte';
  import Card from '../components/Card.svelte';
  import Problem from '../components/Problem.svelte';
  import { apiGet, NetworkError, ProblemError } from '../lib/api';
  import { SubmitFlow } from './pay/submit.svelte';
  import { t, tValue } from '../lib/i18n.svelte';
  import { formatMinor, parseToMinor, minorExponent } from '../lib/money';
  import { connectivity } from '../lib/online.svelte';
  import type { TransferBody } from '../lib/payload';
  import { href } from '../lib/router';
  import { status } from '../lib/status.svelte';
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

  // Submission state lives in a unit-tested module with a synchronous double-submit guard.
  const flow = new SubmitFlow({
    stepUp: async (hash) => {
      try {
        await stepUp(hash);
      } catch (e) {
        throw e instanceof ProblemError || e instanceof NetworkError ? e : new Error(describeWebAuthnError(e));
      }
    },
  });
  const phase = $derived(flow.phase);
  const submitError = $derived(flow.submitError);
  const canRetry = $derived(flow.canRetry);
  const outcome = $derived(flow.outcome);
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
    if (!from) e['from'] = t('pay.chooseFrom');
    if (!toId) e['to'] = t('pay.enterTo');
    else if (toId === fromId) e['to'] = t('pay.differentTo');
    if (from) {
      try {
        amount = parseToMinor(amountText, from.currency);
        if (amount <= 0) e['amount'] = t('pay.amountPositive');
      } catch (err) {
        e['amount'] = err instanceof RangeError && /decimal/.test(err.message)
          ? t('pay.amountDecimals', { currency: from.currency, n: minorExponent(from.currency) })
          : t('pay.amountFormat');
      }
    }
    if (description.length > 140) e['description'] = t('pay.descriptionLong');
    errors = e;
    if (Object.keys(e).length || !from) return null;
    return { from_account_id: from.id, to_account_id: toId, amount, currency: from.currency, description: description.trim() };
  }

  async function onSubmit(e: SubmitEvent) {
    e.preventDefault();
    if (flow.busy) return;
    const body = validate();
    if (!body) {
      status.announce(t('pay.fixFields'));
      return;
    }
    const name = toName;
    await flow.submit(
      body,
      (result, sent) => {
        sentSummary = t('pay.summary', { amount: formatMinor(sent.amount, sent.currency), name });
        status.announce(result.kind === 'sent' ? t('pay.sentMessage', { summary: sentSummary }) : awaitingText(result.requiredApprovals));
      },
      () => status.announce(t('pay.notSent')),
    );
  }

  function awaitingText(n: number): string {
    return n === 1 ? t('pay.awaitingOne') : t('pay.awaitingMany', { n });
  }

  function reset() {
    flow.reset();
    amountText = '';
    description = '';
  }

  const busy = $derived(phase !== 'idle');
</script>

<h1>{t('title.pay')}</h1>

{#if loadError}
  <Problem error={loadError} onretry={load} />
{:else if accounts === null}
  <p aria-busy="true">{t('home.loadingAccounts')}</p>
{:else if outcome}
  <div data-testid="pay-outcome">
  <Card title={outcome.kind === 'sent' ? t('pay.sent') : t('pay.awaiting')} id="outcome">
    {#if outcome.kind === 'sent'}
      <p class="ok big">{t('pay.sentMessage', { summary: sentSummary })}</p>
      <p class="muted">{t('pay.transaction')} <bdi class="mono">{outcome.transactionId}</bdi> · {tValue('status', outcome.status)}</p>
    {:else}
      <p class="warn big">{awaitingText(outcome.requiredApprovals)}</p>
      <p>{t('pay.willBeSent', { summary: sentSummary })} <a href={href('/approvals')}>{t('title.approvals')}</a>.</p>
      <p class="muted">{t('pay.approval')} <bdi class="mono">{outcome.approvalId}</bdi></p>
    {/if}
    <div class="row">
      <button type="button" onclick={reset}>{t('pay.another')}</button>
      <a class="button secondary" href={href('/')}>{t('pay.backHome')}</a>
    </div>
  </Card>
  </div>
{:else if accounts.length === 0}
  <p>{t('pay.needAccount')}</p>
{:else}
  <form onsubmit={onSubmit} novalidate aria-describedby="pay-note">
    <p id="pay-note" class="muted">{t('pay.note')}</p>

    <div class="field">
      <label for="from">{t('pay.from')}</label>
      <select id="from" bind:value={fromId} disabled={busy} aria-invalid={errors['from'] ? 'true' : undefined}>
        {#each accounts as a (a.id)}
          <option value={a.id}>{t('pay.optionAvailable', { name: a.name, code: a.code, amount: formatMinor(a.available, a.currency) })}</option>
        {/each}
      </select>
      {#if from}<p class="hint">{t('pay.availableHint')} <Amount amount={from.available} currency={from.currency} /></p>{/if}
    </div>

    <fieldset class="field" disabled={busy}>
      <legend>{t('pay.to')}</legend>
      <div class="row choice">
        <label class="inline"><input type="radio" bind:group={toMode} value="own" disabled={others.length === 0} /> {t('pay.toOwn')}</label>
        <label class="inline"><input type="radio" bind:group={toMode} value="id" /> {t('pay.toOther')}</label>
      </div>
      {#if toMode === 'own'}
        <label for="to-own" class="visually-hidden">{t('pay.destinationOwn')}</label>
        <select id="to-own" bind:value={toOwnId} aria-invalid={errors['to'] ? 'true' : undefined} aria-describedby={errors['to'] ? 'to-error' : undefined}>
          {#each others as a (a.id)}
            <option value={a.id}>{a.name} ({a.code})</option>
          {/each}
        </select>
      {:else}
        <label for="to-id">{t('pay.destinationId')}</label>
        <input
          id="to-id"
          bind:value={toIdText}
          autocomplete="off"
          autocapitalize="none"
          spellcheck="false"
          dir="ltr"
          aria-invalid={errors['to'] ? 'true' : undefined}
          aria-describedby={errors['to'] ? 'to-error' : undefined}
        />
      {/if}
      {#if errors['to']}<p class="field-error" id="to-error">{errors['to']}</p>{/if}
    </fieldset>

    <div class="field">
      <label for="amount">{t('pay.amount')}{from ? ` (${from.currency})` : ''}</label>
      <input
        id="amount"
        inputmode="decimal"
        autocomplete="off"
        placeholder="0.00"
        dir="ltr"
        bind:value={amountText}
        disabled={busy}
        aria-invalid={errors['amount'] ? 'true' : undefined}
        aria-describedby={errors['amount'] ? 'amount-error' : undefined}
      />
      {#if errors['amount']}<p class="field-error" id="amount-error">{errors['amount']}</p>{/if}
    </div>

    <div class="field">
      <label for="description">{t('pay.description')} <span class="muted">{t('pay.optional')}</span></label>
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
      <p class="summary">{t('pay.payingLead')} <strong><Amount amount={previewMinor} currency={from.currency} /></strong> {t('pay.payingTo')} <strong><bdi>{toName}</bdi></strong>.</p>
    {/if}

    {#if submitError}<Problem error={submitError} />{/if}

    <div class="row">
      <button type="submit" disabled={busy || !connectivity.online} aria-busy={busy}>
        {#if phase === 'stepup'}{t('pay.confirming')}{:else if phase === 'sending'}{t('pay.sending')}{:else if canRetry}{t('pay.retry')}{:else}{t('pay.confirm')}{/if}
      </button>
      {#if !connectivity.online}<span class="muted">{t('pay.needsConnection')}</span>{/if}
    </div>
    <p class="hint">{t('pay.signedInAs', { name: me.display_name })}</p>
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
    margin: 0;
    margin-inline-end: var(--space-3);
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
