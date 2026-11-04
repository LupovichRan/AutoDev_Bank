<script lang="ts">
  import { i18n } from '../lib/i18n.svelte';
  import { formatMinor } from '../lib/money';

  interface Props {
    amount: number | bigint;
    currency: string;
    /** Show "+" for credits and colour by direction (entries lists). */
    signed?: boolean;
    large?: boolean;
  }
  let { amount, currency, signed = false, large = false }: Props = $props();

  const positive = $derived(amount > 0);
  const negative = $derived(amount < 0);
  // The locale places the sign and the currency symbol (he-IL: "‏1,234.56 ‏₪").
  const text = $derived(formatMinor(amount, currency, i18n.locale, { signed }));
</script>

<!-- Isolated so the number and symbol keep the order Intl gives them for the locale. -->
<span class="amount" dir={i18n.dir} class:large class:ok={signed && positive} class:bad={signed && negative}>{text}</span>

<style>
  .amount {
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
    unicode-bidi: isolate;
  }
  .large {
    font-size: var(--text-3xl);
    font-weight: 700;
  }
</style>
