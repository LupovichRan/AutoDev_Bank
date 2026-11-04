<script lang="ts">
  import Card from '../components/Card.svelte';
  import Problem from '../components/Problem.svelte';
  import { apiGet } from '../lib/api';
  import { formatDateTime, truncateMiddle } from '../lib/format';
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
      status.announce('Head hash copied.');
      setTimeout(() => (copied = false), 2000);
    } catch {
      status.announce('Copy failed. Select the hash text instead.');
    }
  }
</script>

<h1>Transparency</h1>
<p>
  This bank's code, fee logic and controls are open source. Read and verify them at
  <a href={REPO} rel="noopener noreferrer" target="_blank">{REPO.replace('https://', '')}</a>.
</p>

<div class="stack">
  <Card title="Audit chain" id="audit">
    {#if verifyError}
      <Problem error={verifyError} onretry={loadVerify} />
    {:else if verify === null}
      <p aria-busy="true">Verifying the audit chain…</p>
    {:else}
      <p class="verdict" class:ok={verify.ok} class:bad={!verify.ok}>
        {verify.ok ? 'OK — every audit event links to the one before it.' : `FAIL — the chain breaks at event #${verify.first_bad_seq ?? '?'}.`}
      </p>
      <dl class="facts">
        <div><dt>Events in chain</dt><dd>{verify.length.toLocaleString()}</dd></div>
        <div>
          <dt>Head hash</dt>
          <dd class="hash">
            <code class="mono" title={verify.head_hash}>{truncateMiddle(verify.head_hash, 10)}</code>
            <button type="button" class="secondary small" onclick={copyHash}>
              {copied ? 'Copied' : 'Copy'}<span class="visually-hidden"> full head hash</span>
            </button>
          </dd>
        </div>
      </dl>
      <button type="button" class="secondary" onclick={loadVerify}>Verify again</button>
    {/if}
  </Card>

  <Card title="Recent audit events" id="events">
    <p class="muted">Who did what is private: only the event number, the hour, the kind of event and the start of its hash are published.</p>
    {#if eventsError}
      <Problem error={eventsError} onretry={loadEvents} />
    {:else if events === null}
      <p aria-busy="true">Loading events…</p>
    {:else if events.length === 0}
      <p class="muted">No events yet.</p>
    {:else}
      <div class="table-wrap">
        <table>
          <caption class="visually-hidden">Most recent audit events</caption>
          <thead><tr><th scope="col" class="num">#</th><th scope="col">Hour</th><th scope="col">Kind</th><th scope="col">Hash</th></tr></thead>
          <tbody>
            {#each events as ev (ev.seq)}
              <tr>
                <td class="num">{ev.seq}</td>
                <td><time datetime={ev.at}>{formatDateTime(ev.at)}</time></td>
                <td>{ev.action}</td>
                <td class="mono">{ev.hash_prefix}…</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  </Card>

  <Card title="Published fee logic" id="fees">
    {#if feesError}
      <Problem error={feesError} onretry={loadFees} />
    {:else if fees === null}
      <p aria-busy="true">Loading fees…</p>
    {:else}
      <dl class="facts">
        <div><dt>Monthly fee</dt><dd>{fees.monthly_fee === 0 ? 'None' : formatMinor(fees.monthly_fee, fees.currency ?? 'ILS')}</dd></div>
        <div><dt>FX markup</dt><dd>{fees.currency_fx_markup_bp === 0 ? 'None (0 bp)' : `${fees.currency_fx_markup_bp} bp`}</dd></div>
        <div><dt>Income shared with customers</dt><dd>{fees.income_share_percent}%</dd></div>
      </dl>
      {#if fees.rules.length}
        <div class="table-wrap">
          <table>
            <caption>Rules as enforced by the code</caption>
            <thead><tr><th scope="col">Rule</th><th scope="col">What it does</th></tr></thead>
            <tbody>
              {#each fees.rules as r (r.name)}
                <tr><th scope="row" class="mono">{r.name}</th><td>{r.description}</td></tr>
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
