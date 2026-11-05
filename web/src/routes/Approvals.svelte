<script lang="ts">
  import Amount from '../components/Amount.svelte';
  import Card from '../components/Card.svelte';
  import Problem from '../components/Problem.svelte';
  import { apiGet, apiPost, ProblemError } from '../lib/api';
  import { formatDateTime } from '../lib/format';
  import { lbl, t } from '../lib/i18n.svelte';
  import { approvePayloadHash } from '../lib/payload';
  import { status } from '../lib/status.svelte';
  import type { Approval, Me } from '../lib/types';
  import { describeWebAuthnError, stepUp } from '../lib/webauthn';

  interface Props {
    me: Me;
    onchange?: (pendingInbox: number) => void;
  }
  let { me, onchange }: Props = $props();

  let inbox = $state<Approval[] | null>(null);
  let mine = $state<Approval[]>([]);
  let error = $state<unknown>(null);
  let actionError = $state<unknown>(null);
  let deciding = $state<string | null>(null);

  // Reject dialog
  let dialogEl: HTMLDialogElement | undefined = $state();
  let rejecting = $state<Approval | null>(null);
  let reason = $state('');
  let reasonError = $state('');
  let returnFocus: HTMLElement | null = null;

  async function load() {
    error = null;
    try {
      const r = await apiGet<{ inbox: Approval[]; mine: Approval[] }>('/api/approvals');
      inbox = r.inbox;
      mine = r.mine;
      onchange?.(r.inbox.filter((a) => a.status === 'pending').length);
    } catch (e) {
      error = e;
    }
  }
  load();

  /** Defense in depth: the server refuses too, but never offer it. */
  function canDecide(a: Approval): boolean {
    return (
      a.status === 'pending' &&
      a.initiator.principal_id !== me.principal_id &&
      !a.decisions.some((d) => d.approver_id === me.principal_id)
    );
  }

  function approvedCount(a: Approval): number {
    return a.decisions.filter((d) => d.decision === 'approve' || d.decision === 'approved').length;
  }

  function coolingOff(a: Approval): string | null {
    if (!a.not_before) return null;
    const t = Date.parse(a.not_before);
    return Number.isFinite(t) && t > Date.now() ? formatDateTime(a.not_before) : null;
  }

  async function decide(a: Approval, approve: boolean, why: string) {
    actionError = null;
    deciding = a.id;
    try {
      if (approve) {
        // Approving moves money: confirm with a passkey bound to this approval.
        try {
          await stepUp(await approvePayloadHash(a.id));
        } catch (e) {
          throw e instanceof ProblemError ? e : new Error(describeWebAuthnError(e));
        }
      }
      await apiPost(`/api/approvals/${encodeURIComponent(a.id)}/decide`, { approve, reason: why });
      status.announce(approve ? t('approvals.approved') : t('approvals.rejected'));
      await load();
    } catch (e) {
      actionError = e;
      status.announce(t('approvals.notRecorded'));
    } finally {
      deciding = null;
    }
  }

  async function cancel(a: Approval) {
    actionError = null;
    deciding = a.id;
    try {
      await apiPost(`/api/approvals/${encodeURIComponent(a.id)}/cancel`, {});
      status.announce(t('approvals.cancelled'));
      await load();
    } catch (e) {
      actionError = e;
      status.announce(t('approvals.notCancelled'));
    } finally {
      deciding = null;
    }
  }

  function openReject(a: Approval, ev: MouseEvent) {
    returnFocus = ev.currentTarget as HTMLElement;
    rejecting = a;
    reason = '';
    reasonError = '';
    dialogEl?.showModal();
  }

  function closeReject() {
    dialogEl?.close();
  }

  function onDialogClose() {
    rejecting = null;
    returnFocus?.focus();
  }

  async function confirmReject(e: SubmitEvent) {
    e.preventDefault();
    if (!rejecting) return;
    if (!reason.trim()) {
      reasonError = t('approvals.reasonRequired');
      return;
    }
    const target = rejecting;
    closeReject();
    await decide(target, false, reason.trim());
  }
</script>

<h1>{t('title.approvals')}</h1>
<p class="muted">{t('approvals.policy')}</p>

{#snippet item(a: Approval, actionable: boolean)}
  <li class="item">
    <div class="top">
      <strong><Amount amount={a.amount} currency={a.currency} /></strong>
      <span class="pill" class:warn={a.status === 'pending'} class:ok={a.status === 'executed' || a.status === 'approved'} class:bad={a.status === 'rejected'}>{lbl('approvalStatus', a.status)}</span>
    </div>
    <p class="desc">{a.subject.description || t('approvals.noDescription')}</p>
    <dl class="meta">
      <div><dt>{t('approvals.from')}</dt><dd class="mono"><bdi>{a.subject.from_account_id}</bdi></dd></div>
      <div><dt>{t('approvals.to')}</dt><dd class="mono"><bdi>{a.subject.to_account_id}</bdi></dd></div>
      <div><dt>{t('approvals.initiatedBy')}</dt><dd>{a.initiator.principal_id === me.principal_id ? t('approvals.you') : a.initiator.display_name}</dd></div>
      <div><dt>{t('approvals.created')}</dt><dd><time datetime={a.created_at}>{formatDateTime(a.created_at)}</time></dd></div>
      <div><dt>{t('approvals.count')}</dt><dd>{t('approvals.countValue', { done: approvedCount(a), required: a.required_approvals })}</dd></div>
      {#if coolingOff(a)}<div><dt>{t('approvals.notBefore')}</dt><dd>{coolingOff(a)}</dd></div>{/if}
      {#if a.status === 'pending' && a.expires_at}<div><dt>{t('approvals.expires')}</dt><dd><time datetime={a.expires_at}>{formatDateTime(a.expires_at)}</time></dd></div>{/if}
    </dl>
    {#if actionable && canDecide(a)}
      <div class="row">
        <button type="button" onclick={() => decide(a, true, '')} disabled={deciding !== null} aria-busy={deciding === a.id}>
          {t('approvals.approve')}<span class="visually-hidden"> {a.subject.description}</span>
        </button>
        <button type="button" class="danger" onclick={(ev) => openReject(a, ev)} disabled={deciding !== null}>
          {t('approvals.reject')}<span class="visually-hidden"> {a.subject.description}</span>
        </button>
      </div>
    {:else if actionable && a.initiator.principal_id === me.principal_id}
      <p class="hint">{t('approvals.ownHint')}</p>
    {:else if !actionable && a.status === 'pending' && a.initiator.principal_id === me.principal_id}
      <div class="row">
        <button type="button" class="secondary" onclick={() => cancel(a)} disabled={deciding !== null} aria-busy={deciding === a.id}>
          {t('approvals.cancel')}<span class="visually-hidden"> {a.subject.description}</span>
        </button>
      </div>
    {/if}
  </li>
{/snippet}

{#if error}
  <Problem {error} onretry={load} />
{:else if inbox === null}
  <p aria-busy="true">{t('approvals.loading')}</p>
{:else}
  <div class="stack">
    {#if actionError}<Problem error={actionError} />{/if}
    <Card title={t('approvals.inbox')} id="inbox">
      {#if inbox.length === 0}
        <p class="muted">{t('approvals.nothing')}</p>
      {:else}
        <ul class="list-plain">
          {#each inbox as a (a.id)}{@render item(a, true)}{/each}
        </ul>
      {/if}
    </Card>
    <Card title={t('approvals.mine')} id="mine">
      {#if mine.length === 0}
        <p class="muted">{t('approvals.mineEmpty')}</p>
      {:else}
        <ul class="list-plain">
          {#each mine as a (a.id)}{@render item(a, false)}{/each}
        </ul>
      {/if}
    </Card>
  </div>
{/if}

<dialog bind:this={dialogEl} onclose={onDialogClose} aria-labelledby="reject-title">
  {#if rejecting}
    <form onsubmit={confirmReject} novalidate>
      <h2 id="reject-title">{t('approvals.rejectTitle')}</h2>
      <p><Amount amount={rejecting.amount} currency={rejecting.currency} /> — {rejecting.subject.description || t('approvals.noDescription')}</p>
      <div class="field">
        <label for="reject-reason">{t('approvals.reason')}</label>
        <textarea
          id="reject-reason"
          bind:value={reason}
          required
          maxlength="500"
          aria-invalid={reasonError ? 'true' : undefined}
          aria-describedby={reasonError ? 'reject-reason-error' : undefined}
        ></textarea>
        {#if reasonError}<p class="field-error" id="reject-reason-error">{reasonError}</p>{/if}
      </div>
      <div class="row">
        <button type="submit" class="danger">{t('approvals.reject')}</button>
        <button type="button" class="secondary" onclick={closeReject}>{t('approvals.dialogCancel')}</button>
      </div>
    </form>
  {/if}
</dialog>

<style>
  .item {
    padding: var(--space-3) 0;
    border-bottom: 1px solid var(--color-border-subtle);
  }
  .item:last-child {
    border-bottom: 0;
  }
  .top {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: var(--space-2);
    font-size: var(--text-lg);
  }
  .desc {
    margin: var(--space-1) 0 var(--space-2);
    overflow-wrap: anywhere;
  }
  .meta {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 160px), 1fr));
    gap: var(--space-2);
    margin: 0 0 var(--space-3);
    font-size: var(--text-sm);
  }
  dt {
    color: var(--color-text-muted);
  }
  dd {
    margin: 0;
  }
</style>
