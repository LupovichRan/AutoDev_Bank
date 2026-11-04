<script lang="ts">
  import Card from '../../components/Card.svelte';
  import { apiGet } from '../../lib/api';
  import { relativeTime } from '../../lib/format';
  import type { ProviderStatus } from '../../lib/types';

  let providers = $state<ProviderStatus[] | null>(null);
  let failed = $state(false);

  apiGet<{ providers: ProviderStatus[] }>('/api/opendata/status')
    .then((r) => (providers = r.providers))
    .catch(() => (failed = true));
</script>

<Card title="Data provider health" id="provider-status">
  {#if failed}
    <p class="muted">Provider status is unavailable right now.</p>
  {:else if providers === null}
    <p aria-busy="true">Loading…</p>
  {:else}
    <ul class="list-plain">
      {#each providers as p (p.provider + p.kind)}
        <li class="row">
          <span class="pill" class:ok={p.ok} class:bad={!p.ok}>{p.ok ? 'OK' : 'Failing'}</span>
          <span><strong>{p.provider}</strong> <span class="muted">({p.kind})</span></span>
          <span class="muted">last fetch {relativeTime(p.fetched_at)}{p.duration_ms ? `, ${p.duration_ms} ms` : ''}</span>
          {#if !p.ok && p.error}<span class="bad">{p.error}</span>{/if}
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
