<script lang="ts">
  import Amount from '../components/Amount.svelte';
  import Card from '../components/Card.svelte';
  import Problem from '../components/Problem.svelte';
  import { apiGet } from '../lib/api';
  import { formatDate } from '../lib/format';
  import { lbl, t } from '../lib/i18n.svelte';
  import { href } from '../lib/router';
  import type { Account, Entry, Me } from '../lib/types';

  interface Props {
    me: Me;
  }
  let { me }: Props = $props();

  let accounts = $state<Account[] | null>(null);
  let error = $state<unknown>(null);
  let selectedId = $state<string | null>(null);
  let entries = $state<Entry[] | null>(null);
  let entriesError = $state<unknown>(null);

  const selected = $derived(accounts?.find((a) => a.id === selectedId) ?? null);

  async function load() {
    error = null;
    try {
      accounts = (await apiGet<{ accounts: Account[] }>('/api/accounts')).accounts;
      if (!selectedId || !accounts.some((a) => a.id === selectedId)) selectedId = accounts[0]?.id ?? null;
    } catch (e) {
      error = e;
    }
  }

  async function loadEntries(id: string) {
    entries = null;
    entriesError = null;
    try {
      const r = await apiGet<{ entries: Entry[] }>(`/api/accounts/${encodeURIComponent(id)}/entries?limit=50`);
      if (selectedId === id) entries = r.entries;
    } catch (e) {
      if (selectedId === id) entriesError = e;
    }
  }

  load();

  $effect(() => {
    if (selectedId) loadEntries(selectedId);
  });
</script>

<h1>{t('home.hello', { name: me.display_name })}</h1>

{#if error}
  <Problem {error} onretry={load} />
{:else if accounts === null}
  <p aria-busy="true">{t('home.loadingAccounts')}</p>
{:else if accounts.length === 0}
  <p>{t('home.noAccounts')}</p>
{:else}
  <section aria-labelledby="accounts-title">
    <h2 id="accounts-title" class="visually-hidden">{t('home.yourAccounts')}</h2>
    <ul class="list-plain accounts">
      {#each accounts as a (a.id)}
        <li>
          <button
            type="button"
            class="account"
            aria-pressed={a.id === selectedId}
            onclick={() => (selectedId = a.id)}
          >
            <span class="acc-name">{a.name}</span>
            <span class="acc-meta"><bdi class="code">{a.code}</bdi> · {lbl('accountKind', a.kind)}{a.holders.length > 1 ? ` · ${t('home.joint')}` : ''}</span>
            <Amount amount={a.balance} currency={a.currency} large />
            <span class="acc-meta">{t('home.available')} <Amount amount={a.available} currency={a.currency} /></span>
          </button>
        </li>
      {/each}
    </ul>
  </section>

  {#if selected}
    <div class="stack detail">
      <Card title={selected.name} id="account-detail">
        {#snippet actions()}
          <a class="button small-link" href={href('/pay', { from: selected.id })}>{t('home.payFrom')}</a>
        {/snippet}
        <dl class="facts">
          <div><dt>{t('home.balance')}</dt><dd><Amount amount={selected.balance} currency={selected.currency} /></dd></div>
          <div><dt>{t('home.available')}</dt><dd><Amount amount={selected.available} currency={selected.currency} /></dd></div>
          <div><dt>{t('home.pendingOut')}</dt><dd><Amount amount={selected.pending_debits} currency={selected.currency} /></dd></div>
          <div><dt>{t('home.pendingIn')}</dt><dd><Amount amount={selected.pending_credits} currency={selected.currency} /></dd></div>
          <div>
            <dt>{t('home.holders')}</dt>
            <dd>{selected.holders.map((h) => h.display_name).join(', ')}</dd>
          </div>
          <div><dt>{t('home.accountId')}</dt><dd class="mono"><bdi>{selected.id}</bdi></dd></div>
        </dl>
      </Card>

      <Card title={t('home.recent')} id="entries">
        {#if entriesError}
          <Problem error={entriesError} onretry={() => selectedId && loadEntries(selectedId)} />
        {:else if entries === null}
          <p aria-busy="true">{t('home.loadingActivity')}</p>
        {:else if entries.length === 0}
          <p class="muted">{t('home.noActivity')}</p>
        {:else}
          <ul class="list-plain entries">
            {#each entries as e (e.id)}
              <li>
                <div class="entry-main">
                  <span>{e.description || t('home.transfer')}</span>
                  <Amount amount={e.amount} currency={e.currency} signed />
                </div>
                <div class="entry-meta muted">
                  <time datetime={e.effective_date}>{formatDate(e.effective_date)}</time>
                  {#if e.status !== 'posted'}<span class="pill warn">{lbl('txStatus', e.status)}</span>{/if}
                  <span>{t('home.balance')} <Amount amount={e.balance_after} currency={e.currency} /></span>
                </div>
              </li>
            {/each}
          </ul>
        {/if}
      </Card>
    </div>
  {/if}
{/if}

<style>
  .accounts {
    display: grid;
    gap: var(--space-3);
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 260px), 1fr));
  }
  .account {
    width: 100%;
    flex-direction: column;
    align-items: flex-start;
    gap: var(--space-1);
    padding: var(--space-4);
    text-align: start;
    font-weight: 400;
    background: var(--color-surface);
    color: var(--color-text);
    border: 2px solid var(--color-border-subtle);
    border-radius: var(--radius-lg);
  }
  .account[aria-pressed='true'] {
    border-color: var(--color-primary);
  }
  .acc-name {
    font-weight: 700;
  }
  .acc-meta {
    font-size: var(--text-sm);
    color: var(--color-text-muted);
    max-width: 100%;
    overflow-wrap: anywhere;
  }
  .detail {
    margin-top: var(--space-5);
  }
  .small-link {
    min-height: 36px;
    font-size: var(--text-sm);
  }
  .facts {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 180px), 1fr));
    gap: var(--space-3);
    margin: 0;
  }
  dt {
    font-size: var(--text-sm);
    color: var(--color-text-muted);
  }
  dd {
    margin: 0;
    font-weight: 600;
  }
  .entries li {
    padding: var(--space-3) 0;
    border-bottom: 1px solid var(--color-border-subtle);
  }
  .entries li:last-child {
    border-bottom: 0;
  }
  .entry-main {
    display: flex;
    justify-content: space-between;
    gap: var(--space-3);
    font-weight: 600;
  }
  .entry-main > span:first-child {
    min-width: 0;
    overflow-wrap: anywhere;
  }
  .entry-meta {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1) var(--space-3);
    font-size: var(--text-sm);
  }
</style>
