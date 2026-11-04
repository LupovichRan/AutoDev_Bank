<script lang="ts">
  import Card from '../components/Card.svelte';
  import Problem from '../components/Problem.svelte';
  import { apiGet } from '../lib/api';
  import { formatDateTime, formatNumber, formatPercent, truncateMiddle } from '../lib/format';
  import { t } from '../lib/i18n.svelte';
  import { formatMinor } from '../lib/money';
  import { status } from '../lib/status.svelte';
  import type { AuditEvent, AuditVerify, FeeSchedule } from '../lib/types';

  const REPO = 'https://github.com/LupovichRan/AutoDev_Bank';

  let verify = $state<AuditVerify | null>(null);
  let verifyError = $state<unknown>(null);
  let events = $state<AuditEvent[] | null>(null);
  let eventsError = $state<unknown>(null);
  let fees = $state<FeeSchedule | null>(null);
  let feesError = $state<unknown>(null);
  let copied = $state(false);

  async function loadVerify() {
    verifyError = null;
    verify = null;
    try {
      verify = await apiGet<AuditVerify>('/api/transparency/audit/verify');
    } catch (e) {
      verifyError = e;
    }
  }
  async function loadEvents() {
    eventsError = null;
    try {
      events = (await apiGet<{ events: AuditEvent[] }>('/api/transparency/audit/recent')).events;
    } catch (e) {
      eventsError = e;
    }
  }
  async function loadFees() {
    feesError = null;
    try {
      fees = await apiGet<FeeSchedule>('/api/transparency/fees');
    } catch (e) {
      feesError = e;
    }
  }
  loadVerify();
  loadEvents();
  loadFees();

  async function copyHash() {
    if (!verify) return;
    try {
      await navigator.clipboard.writeText(verify.head_hash);
      copied = true;
      status.announce(t('tr.copied'));
      setTimeout(() => (copied = false), 2000);
    } catch {
      status.announce(t('tr.copyFailed'));
    }
  }
</script>

<h1>{t('title.transparency')}</h1>
<p>
  {t('tr.lead')}
  <a href={REPO} rel="noopener noreferrer" target="_blank"><bdi>{REPO.replace('https://', '')}</bdi></a>.
</p>

<div class="stack">
  <Card title={t('tr.audit')} id="audit">
    {#if verifyError}
      <Problem error={verifyError} onretry={loadVerify} />
    {:else if verify === null}
      <p aria-busy="true">{t('tr.verifying')}</p>
    {:else}
      <p class="verdict" class:ok={verify.ok} class:bad={!verify.ok}>
        {verify.ok ? t('tr.ok') : t('tr.fail', { seq: verify.first_bad_seq ?? '?' })}
      </p>
      <dl class="facts">
        <div><dt>{t('tr.events')}</dt><dd>{formatNumber(verify.length)}</dd></div>
        <div>
          <dt>{t('tr.headHash')}</dt>
          <dd class="hash">
            <code class="mono" title={verify.head_hash}><bdi>{truncateMiddle(verify.head_hash, 10)}</bdi></code>
            <button type="button" class="secondary small" onclick={copyHash}>
              {copied ? t('tr.copiedButton') : t('tr.copy')}<span class="visually-hidden">{t('tr.fullHash')}</span>
            </button>
          </dd>
        </div>
      </dl>
      <button type="button" class="secondary" onclick={loadVerify}>{t('tr.verifyAgain')}</button>
    {/if}
  </Card>

  <Card title={t('tr.recent')} id="events">
    <p class="muted">{t('tr.privacy')}</p>
    {#if eventsError}
      <Problem error={eventsError} onretry={loadEvents} />
    {:else if events === null}
      <p aria-busy="true">{t('tr.loadingEvents')}</p>
    {:else if events.length === 0}
      <p class="muted">{t('tr.noEvents')}</p>
    {:else}
      <div class="table-wrap">
        <table>
          <caption class="visually-hidden">{t('tr.caption')}</caption>
          <thead><tr><th scope="col" class="num">#</th><th scope="col">{t('tr.colHour')}</th><th scope="col">{t('tr.colKind')}</th><th scope="col">{t('tr.colHash')}</th></tr></thead>
          <tbody>
            {#each events as ev (ev.seq)}
              <tr>
                <td class="num">{ev.seq}</td>
                <td><time datetime={ev.at}>{formatDateTime(ev.at)}</time></td>
                <td>{ev.action}</td>
                <td class="mono"><bdi>{ev.hash_prefix}…</bdi></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Card>

  <Card title={t('tr.fees')} id="fees">
    {#if feesError}
      <Problem error={feesError} onretry={loadFees} />
    {:else if fees === null}
      <p aria-busy="true">{t('tr.loadingFees')}</p>
    {:else}
      <dl class="facts">
        <div><dt>{t('tr.monthlyFee')}</dt><dd>{fees.monthly_fee === 0 ? t('tr.none') : formatMinor(fees.monthly_fee, fees.currency ?? 'ILS')}</dd></div>
        <div><dt>{t('tr.fxMarkup')}</dt><dd>{fees.currency_fx_markup_bp === 0 ? t('tr.noneBp') : t('tr.bp', { bp: fees.currency_fx_markup_bp })}</dd></div>
        <div><dt>{t('tr.incomeShare')}</dt><dd>{formatPercent(fees.income_share_percent)}</dd></div>
      </dl>
      {#if fees.rules.length}
        <div class="table-wrap">
          <table>
            <caption>{t('tr.rulesCaption')}</caption>
            <thead><tr><th scope="col">{t('tr.rule')}</th><th scope="col">{t('tr.whatItDoes')}</th></tr></thead>
            <tbody>
              {#each fees.rules as r (r.name)}
                <tr><th scope="row" class="mono"><bdi>{r.name}</bdi></th><td lang="en">{r.description}</td></tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    {/if}
  </Card>
</div>

<style>
  .verdict {
    font-size: var(--text-lg);
    font-weight: 700;
  }
  .facts {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 220px), 1fr));
    gap: var(--space-3);
    margin: 0 0 var(--space-4);
  }
  dt {
    font-size: var(--text-sm);
    color: var(--color-text-muted);
  }
  dd {
    margin: 0;
    font-weight: 600;
  }
  .hash {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--space-2);
  }
  th[scope='row'] {
    color: var(--color-text);
  }
</style>
