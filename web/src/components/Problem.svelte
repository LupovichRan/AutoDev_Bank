<script lang="ts">
  import { NetworkError, ProblemError } from '../lib/api';

  interface Props {
    error: unknown;
    /** Optional retry action shown as a button. */
    onretry?: () => void;
  }
  let { error, onretry }: Props = $props();

  const FRIENDLY: Record<string, string> = {
    step_up_required: 'Please confirm with your passkey to continue.',
    insufficient_funds: 'There is not enough available balance for this payment.',
  };

  const title = $derived.by(() => {
    if (error instanceof ProblemError) return error.title || `Error ${error.status}`;
    if (error instanceof NetworkError) return 'You are offline';
    return 'Something went wrong';
  });
  const detail = $derived.by(() => {
    if (error instanceof ProblemError) return FRIENDLY[error.detail] ?? error.detail;
    return error instanceof Error ? error.message : String(error);
  });
</script>

<div class="problem" role="alert">
  <p class="title">{title}</p>
  {#if detail && detail !== title}<p class="detail">{detail}</p>{/if}
  {#if onretry}<button type="button" class="secondary small" onclick={onretry}>Try again</button>{/if}
</div>

<style>
  .problem {
    border: 2px solid var(--color-negative);
    border-radius: var(--radius-md);
    padding: var(--space-3) var(--space-4);
    background: var(--color-surface-raised);
  }
  .title {
    color: var(--color-negative);
    font-weight: 700;
    margin: 0;
  }
  .detail {
    margin: var(--space-1) 0 0;
  }
  button {
    margin-top: var(--space-2);
  }
</style>
