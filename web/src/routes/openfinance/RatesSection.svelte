<script lang="ts">
  import Card from '../../components/Card.svelte';
  import Problem from '../../components/Problem.svelte';
  import { apiGet } from '../../lib/api';
  import { formatDate, formatPercent } from '../../lib/format';
  import type { OpenDataList, OpenDataRecord, PolicyRateData } from '../../lib/types';
  import Badges from './Badges.svelte';

  let records = $state<OpenDataRecord<PolicyRateData>[] | null>(null);
  let error = $state<unknown>(null);

  async function load() {
    error = null;
    try {
      records = (await apiGet<OpenDataList<PolicyRateData>>('/api/opendata/rates')).records;
    } catch (e) {
      error = e;
    }
  }
  load();
</script>

<Card title="Central bank policy rates" id="rates">
  {#if error}
    <Problem {error} onretry={load} />
  {:else if records === null}
    <p aria-busy="true">Loading policy rates…</p>
  {:else if records.length === 0}
    <p class="muted">No policy rates cached yet.</p>
  {:else}
    <Badges {records} />
    <div class="table-wrap">
      <table>
        <caption class="visually-hidden">Policy rates by central bank</caption>
        <thead><tr><th scope="col">Central bank</th><th scope="col">Rate</th><th scope="col" class="num">Value</th><th scope="col">Effective</th></tr></thead>
        <tbody>
          {#each records as r (r.key)}
            <tr><th scope="row">{r.data.bank}</th><td>{r.data.name}</td><td class="num">{formatPercent(r.data.rate)}</td><td>{formatDate(r.data.effective)}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Card>
