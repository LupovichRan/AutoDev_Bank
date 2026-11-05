<script lang="ts">
  import Card from '../../components/Card.svelte';
  import Problem from '../../components/Problem.svelte';
  import { apiGet } from '../../lib/api';
  import { formatPercent } from '../../lib/format';
  import { lbl, t } from '../../lib/i18n.svelte';
  import { safeUrl } from '../../lib/safeUrl';
  import type { OpenDataList, OpenDataRecord, ProductData } from '../../lib/types';
  import Badges from './Badges.svelte';

  let records = $state<OpenDataRecord<ProductData>[] | null>(null);
  let error = $state<unknown>(null);
  let bank = $state('');

  async function load() {
    error = null;
    try {
      records = (await apiGet<OpenDataList<ProductData>>('/api/opendata/products')).records;
    } catch (e) {
      error = e;
    }
  }
  load();

  const banks = $derived([...new Set((records ?? []).map((r) => r.data.bank))].sort());
  const shown = $derived((records ?? []).filter((r) => !bank || r.data.bank === bank));
  const gbp = new Intl.NumberFormat('en-GB', { style: 'currency', currency: 'GBP' });
  const fee = (v: number | null) => (v === null || v === undefined ? '—' : gbp.format(v));
  // GBP fees keep the en-GB form (£1.50) in both languages: they are quoted UK prices.
</script>

<Card title={t('products.title')} id="products">
  {#if error}
    <Problem {error} onretry={load} />
  {:else if records === null}
    <p aria-busy="true">{t('products.loading')}</p>
  {:else if records.length === 0}
    <p class="muted">{t('products.none')}</p>
  {:else}
    <Badges {records} />
    <div class="field filter">
      <label for="bank-filter">{t('products.filter')}</label>
      <select id="bank-filter" bind:value={bank}>
        <option value="">{t('products.all', { n: records.length })}</option>
        {#each banks as b (b)}<option value={b}>{b}</option>{/each}
      </select>
    </div>
    <div class="table-wrap">
      <table>
        <caption>{t('products.caption', { n: shown.length })}</caption>
        <thead>
          <tr>
            <th scope="col">{t('products.colBank')}</th>
            <th scope="col">{t('products.colProduct')}</th>
            <th scope="col">{t('products.colType')}</th>
            <th scope="col" class="num">{t('products.colEar')}</th>
            <th scope="col" class="num">{t('products.colFee')}</th>
          </tr>
        </thead>
        <tbody>
          {#each shown as r (r.key)}
            <tr>
              <td>{r.data.brand || r.data.bank}</td>
              <th scope="row">
                {#if safeUrl(r.data.url)}<a href={safeUrl(r.data.url)} rel="noopener noreferrer" target="_blank">{r.data.name}</a>{:else}{r.data.name}{/if}
              </th>
              <td>{lbl('productType', r.data.type)}</td>
              <td class="num">{formatPercent(r.data.overdraft_ear)}</td>
              <td class="num">{fee(r.data.monthly_fee)}</td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {/if}
</Card>

<style>
  .filter {
    max-width: 20rem;
  }
  th[scope='row'] {
    color: var(--color-text);
    font-weight: 600;
  }
</style>
