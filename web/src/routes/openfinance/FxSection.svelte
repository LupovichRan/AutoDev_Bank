<script lang="ts">
  import Card from '../../components/Card.svelte';
  import Problem from '../../components/Problem.svelte';
  import { apiGet } from '../../lib/api';
  import { formatDate } from '../../lib/format';
  import { convertViaEur } from '../../lib/opendata';
  import type { FxData, OpenDataList, OpenDataRecord } from '../../lib/types';
  import Badges from './Badges.svelte';

  let records = $state<OpenDataRecord<FxData>[] | null>(null);
  let error = $state<unknown>(null);

  let amountText = $state('100');
  let fromCcy = $state('EUR');
  let toCcy = $state('ILS');

  async function load() {
    error = null;
    try {
      records = (await apiGet<OpenDataList<FxData>>('/api/opendata/fx')).records;
    } catch (e) {
      error = e;
    }
  }
  load();

  const eur = $derived((records ?? []).filter((r) => r.data.base === 'EUR').sort((a, b) => a.data.quote.localeCompare(b.data.quote)));
  // Other bases, e.g. Bank of Israel representative rates against ILS.
  const others = $derived((records ?? []).filter((r) => r.data.base !== 'EUR'));
  const currencies = $derived(['EUR', ...new Set(eur.map((r) => r.data.quote))]);
  const converted = $derived.by(() => {
    const n = Number(amountText.replace(/,/g, ''));
    if (!amountText.trim() || !Number.isFinite(n)) return null;
    return convertViaEur(n, fromCcy, toCcy, eur.map((r) => r.data));
  });
  const fmtRate = (n: number) => new Intl.NumberFormat(undefined, { maximumFractionDigits: 6 }).format(n);
  const fmtMoney = (n: number, c: string) => {
    try {
      return new Intl.NumberFormat(undefined, { style: 'currency', currency: c }).format(n);
    } catch {
      return `${n.toFixed(2)} ${c}`;
    }
  };
</script>

<Card title="Exchange rates" id="fx">
  {#if error}
    <Problem {error} onretry={load} />
  {:else if records === null}
    <p aria-busy="true">Loading rates…</p>
  {:else if records.length === 0}
    <p class="muted">No rates cached yet.</p>
  {:else}
    <Badges {records} />

    <form class="converter" onsubmit={(e) => e.preventDefault()} aria-labelledby="conv-title">
      <h3 id="conv-title">Converter</h3>
      <div class="conv-grid">
        <div>
          <label for="fx-amount">Amount</label>
          <input id="fx-amount" inputmode="decimal" bind:value={amountText} />
        </div>
        <div>
          <label for="fx-from">From</label>
          <select id="fx-from" bind:value={fromCcy}>
            {#each currencies as c (c)}<option>{c}</option>{/each}
          </select>
        </div>
        <div>
          <label for="fx-to">To</label>
          <select id="fx-to" bind:value={toCcy}>
            {#each currencies as c (c)}<option>{c}</option>{/each}
          </select>
        </div>
      </div>
      <p class="result" aria-live="polite">
        {#if converted !== null}
          ≈ <strong>{fmtMoney(converted, toCcy)}</strong>
          <span class="muted">at reference rates. AutoDev Bank adds no FX markup.</span>
        {:else}
          <span class="muted">Enter an amount to convert.</span>
        {/if}
      </p>
    </form>

    <div class="table-wrap">
      <table>
        <caption>Euro reference rates (1 EUR =)</caption>
        <thead><tr><th scope="col">Currency</th><th scope="col" class="num">Rate</th><th scope="col">Date</th><th scope="col">Source</th></tr></thead>
        <tbody>
          {#each eur as r (r.key)}
            <tr><th scope="row">{r.data.quote}</th><td class="num">{fmtRate(r.data.rate)}</td><td>{formatDate(r.data.date)}</td><td>{r.data.source || r.provider}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>

    {#if others.length}
      <div class="table-wrap">
        <table>
          <caption>Other reference rates (e.g. Bank of Israel, ILS)</caption>
          <thead><tr><th scope="col">Pair</th><th scope="col" class="num">Rate</th><th scope="col">Date</th><th scope="col">Source</th></tr></thead>
          <tbody>
            {#each others as r (r.key)}
              <tr><th scope="row">1 {r.data.base} = {r.data.quote}</th><td class="num">{fmtRate(r.data.rate)}</td><td>{formatDate(r.data.date)}</td><td>{r.data.source || r.provider}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    {/if}
  {/if}
</Card>

<style>
  .converter {
    margin-bottom: var(--space-4);
  }
  .conv-grid {
    display: grid;
    gap: var(--space-3);
    grid-template-columns: repeat(auto-fit, minmax(min(100%, 140px), 1fr));
  }
  .result {
    margin-top: var(--space-2);
  }
  .table-wrap + .table-wrap {
    margin-top: var(--space-4);
  }
</style>
