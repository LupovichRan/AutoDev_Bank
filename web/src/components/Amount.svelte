<script lang="ts">
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
  const text = $derived((signed && positive ? '+' : '') + formatMinor(amount, currency));
</script>

<span class="amount" class:large class:ok={signed && positive} class:bad={signed && negative}>{text}</span>

<style>
  .amount {
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
  }
  .large {
    font-size: var(--text-3xl);
    font-weight: 700;
  }
</style>
