<script lang="ts">
  import { NetworkError, ProblemError } from '../lib/api';
  import { t, tDetail } from '../lib/i18n.svelte';
  import { isKey } from '../lib/messages';

  interface Props {
    error: unknown;
    /** Optional retry action shown as a button. */
    onretry?: () => void;
  }
  let { error, onretry }: Props = $props();

  // Server titles and details are English; known statuses, detail codes and
  // auth sentences are shown in the UI language, anything else as sent.
  const title = $derived.by(() => {
    if (error instanceof ProblemError) {
      const k = `problem.status.${error.status}`;
      if (isKey(k)) return t(k);
      return error.title || t('problem.errorStatus', { status: error.status });
    }
    if (error instanceof NetworkError) return t('problem.offline');
    return t('problem.generic');
  });
  const detail = $derived.by(() => {
    if (error instanceof ProblemError) {
      return tDetail(error.detail);
    }
    if (error instanceof NetworkError) return t('problem.offlineDetail');
    return error instanceof Error ? error.message : String(error);
  });
</script>

<div class="problem" role="alert">
  <p class="title">{title}</p>
  {#if detail && detail !== title}<p class="detail">{detail}</p>{/if}
  {#if onretry}<button type="button" class="secondary small" onclick={onretry}>{t('problem.retry')}</button>{/if}
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
