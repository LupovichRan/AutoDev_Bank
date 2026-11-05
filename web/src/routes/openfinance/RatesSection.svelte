<script lang="ts">
  import Card from '../../components/Card.svelte';
  import Problem from '../../components/Problem.svelte';
  import { apiGet } from '../../lib/api';
  import { formatDate, formatPercent } from '../../lib/format';
  import { lbl, t } from '../../lib/i18n.svelte';
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

<Card title={t('rates.title')} id="rates">
  {#if error}
    <Problem {error} onretry={load} />
  {:else if records === null}
    <p aria-busy="true">{t('rates.loading')}</p>
  {:else if records.length === 0}
    <p class="muted">{t('rates.none')}</p>
  {:else}
    <Badges {records} />
    <div class="table-wrap">
      <table>
        <caption class="visually-hidden">{t('rates.caption')}</caption>
        <thead><tr><th scope="col">{t('rates.colBank')}</th><th scope="col">{t('rates.colName')}</th><th scope="col" class="num">{t('rates.colValue')}</th><th scope="col">{t('rates.colEffective')}</th></tr></thead>
        <tbody>
          {#each records as r (r.key)}
            <tr><th scope="row">{lbl('centralBank', r.data.bank)}</th><td>{lbl('policyRate', r.data.name)}</td><td class="num">{formatPercent(r.data.rate)}</td><td>{formatDate(r.data.effective)}</td></tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Card>
