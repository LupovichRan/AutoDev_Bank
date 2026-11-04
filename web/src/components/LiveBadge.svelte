<script lang="ts">
  import { safeUrl } from '../lib/safeUrl';
  import { formatDateTime, relativeTime } from '../lib/format';

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
  <span class="pill ok">Live data</span>
  <span>{provider}</span>
  <span aria-hidden="true">·</span>
  <time datetime={fetchedAt} title={formatDateTime(fetchedAt)}>fetched {relativeTime(fetchedAt, now)}</time>
  {#if source}
    <span aria-hidden="true">·</span>
    <a href={source} rel="noopener noreferrer" target="_blank">Source<span class="visually-hidden"> for {provider} (opens in a new tab)</span></a>
  {/if}
  {#if license}
    <span aria-hidden="true">·</span>
    <span>Licence: {license}</span>
  {/if}
  {#if terms}
    <span aria-hidden="true">·</span>
    <a href={terms} rel="noopener noreferrer" target="_blank">Terms<span class="visually-hidden"> of {provider} (opens in a new tab)</span></a>
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
