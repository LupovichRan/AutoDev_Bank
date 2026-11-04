<script lang="ts">
  import FxSection from './openfinance/FxSection.svelte';
  import NearbySection from './openfinance/NearbySection.svelte';
  import ProductsSection from './openfinance/ProductsSection.svelte';
  import RatesSection from './openfinance/RatesSection.svelte';
  import StatusSection from './openfinance/StatusSection.svelte';
</script>

<h1>Open Finance</h1>
<p class="muted">
  Live public data from central banks and UK Open Banking, cached by this bank's server. Each section names its
  source, when it was fetched and its licence.
</p>

<nav aria-label="Sections" class="jump">
  <a href="#fx" onclick={(e) => jump(e, 'fx')}>Exchange rates</a>
  <a href="#rates" onclick={(e) => jump(e, 'rates')}>Policy rates</a>
  <a href="#products" onclick={(e) => jump(e, 'products')}>Current accounts</a>
  <a href="#nearby" onclick={(e) => jump(e, 'nearby')}>Nearby</a>
</nav>

<div class="stack">
  <FxSection />
  <RatesSection />
  <ProductsSection />
  <NearbySection />
  <StatusSection />
</div>

<script module lang="ts">
  // In-page links would clobber the hash route, so scroll instead.
  function jump(e: MouseEvent, id: string) {
    e.preventDefault();
    const el = document.getElementById(id);
    if (!el) return;
    el.scrollIntoView({ behavior: 'smooth', block: 'start' });
    const heading = el.querySelector<HTMLElement>('h2');
    if (heading) {
      heading.tabIndex = -1;
      heading.focus({ preventScroll: true });
    }
  }
</script>

<style>
  .jump {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-2) var(--space-4);
    margin-bottom: var(--space-4);
  }
  .jump a {
    display: inline-flex;
    align-items: center;
    min-height: 44px;
  }
</style>
