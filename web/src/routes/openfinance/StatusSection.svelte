<script lang="ts">
  import Card from '../../components/Card.svelte';
  import { apiGet } from '../../lib/api';
  import { relativeTime } from '../../lib/format';
  import { lbl, t } from '../../lib/i18n.svelte';
  import type { ProviderStatus } from '../../lib/types';

  let providers = $state<ProviderStatus[] | null>(null);
  let failed = $state(false);

  apiGet<{ providers: ProviderStatus[] }>('/api/opendata/status')
    .then((r) => (providers = r.providers))
    .catch(() => (failed = true));
</script>

<Card title={t('health.title')} id="provider-status">
  {#if failed}
    <p class="muted">{t('health.unavailable')}</p>
  {:else if providers === null}
    <p aria-busy="true">{t('app.loading')}</p>
  {:else}
    <ul class="list-plain">
      {#each providers as p (p.provider + p.kind)}
        <li class="row">
          <span class="pill" class:ok={p.ok} class:bad={!p.ok}>{p.ok ? t('health.ok') : t('health.failing')}</span>
          <span><strong><bdi>{lbl('provider', p.provider)}</bdi></strong> <span class="muted">(<bdi>{lbl('dataKind', p.kind)}</bdi>)</span></span>
          <span class="muted">{t('health.last', { time: relativeTime(p.fetched_at) })}{p.duration_ms ? `, ${p.duration_ms} ms` : ''}</span>
          {#if !p.ok && p.error}<span class="bad" lang="en" dir="ltr">{p.error}</span>{/if}
        </li>
      {/each}
    </ul>
  {/if}
</Card>

<style>
  li {
    padding: var(--space-1) 0;
    font-size: var(--text-sm);
  }
</style>
