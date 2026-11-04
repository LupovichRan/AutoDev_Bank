<script lang="ts">
  import { t } from '../lib/i18n.svelte';
  import FxSection from './openfinance/FxSection.svelte';
  import NearbySection from './openfinance/NearbySection.svelte';
  import ProductsSection from './openfinance/ProductsSection.svelte';
  import RatesSection from './openfinance/RatesSection.svelte';
  import StatusSection from './openfinance/StatusSection.svelte';
</script>

<h1>{t('title.openFinance')}</h1>
<p class="muted">{t('of.lead')}</p>

<nav aria-label={t('of.sections')} class="jump">
  <a href="#fx" onclick={(e) => jump(e, 'fx')}>{t('of.jumpFx')}</a>
  <a href="#rates" onclick={(e) => jump(e, 'rates')}>{t('of.jumpRates')}</a>
  <a href="#products" onclick={(e) => jump(e, 'products')}>{t('of.jumpProducts')}</a>
  <a href="#nearby" onclick={(e) => jump(e, 'nearby')}>{t('of.jumpNearby')}</a>
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
