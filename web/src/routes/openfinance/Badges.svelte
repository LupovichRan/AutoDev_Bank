<script lang="ts">
  import LiveBadge from '../../components/LiveBadge.svelte';
  import { providersOf } from '../../lib/opendata';
  import type { OpenDataRecord } from '../../lib/types';

  let { records }: { records: OpenDataRecord<unknown>[] } = $props();
  const providers = $derived(providersOf(records));
</script>

{#each providers as p (p.provider)}
  <LiveBadge provider={p.provider} fetchedAt={p.fetchedAt} sourceUrl={p.sourceUrl} license={p.license} />
{/each}
