<script lang="ts">
  import { safeUrl } from '../lib/safeUrl';
  import { formatDateTime, relativeTime } from '../lib/format';
  import { i18n, t } from '../lib/i18n.svelte';

  interface Props {
    provider: string;
    fetchedAt: string;
    sourceUrl?: string;
    license?: string;
    licenseUrl?: string;
  }
  let { provider, fetchedAt, sourceUrl, license, licenseUrl }: Props = $props();

  const source = $derived(safeUrl(sourceUrl));
  const terms = $derived(safeUrl(licenseUrl));

  // Re-render the relative time every minute.
  let now = $state(Date.now());
  $effect(() => {
    const t = setInterval(() => (now = Date.now()), 60_000);
    return () => clearInterval(t);
  });
</script>

<p class="badge">
  <span class="pill ok">{t('live.data')}</span>
  <bdi>{provider}</bdi>
  <span aria-hidden="true">·</span>
  <time datetime={fetchedAt} title={formatDateTime(fetchedAt, i18n.locale)}>{t('live.fetched', { time: relativeTime(fetchedAt, now, i18n.locale) })}</time>
  {#if source}
    <span aria-hidden="true">·</span>
    <a href={source} rel="noopener noreferrer" target="_blank">{t('live.source')}<span class="visually-hidden">{t('live.sourceFor', { provider })}</span></a>
  {/if}
  {#if license}
    <span aria-hidden="true">·</span>
    <span>{t('live.licence', { license })}</span>
  {/if}
  {#if terms}
    <span aria-hidden="true">·</span>
    <a href={terms} rel="noopener noreferrer" target="_blank">{t('live.terms')}<span class="visually-hidden">{t('live.termsOf', { provider })}</span></a>
  {/if}
</p>

<style>
  .badge {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-1) var(--space-2);
    align-items: center;
    font-size: var(--text-sm);
    color: var(--color-text-muted);
    margin: 0 0 var(--space-3);
  }
  a {
    display: inline-block;
    min-height: 24px;
    line-height: 24px;
  }
</style>
