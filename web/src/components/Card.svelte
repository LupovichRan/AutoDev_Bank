<script lang="ts">
  import type { Snippet } from 'svelte';

  interface Props {
    title?: string;
    /** Heading level for the title (sections inside a screen use 2). */
    level?: 2 | 3;
    id?: string;
    actions?: Snippet;
    children: Snippet;
  }
  let { title, level = 2, id, actions, children }: Props = $props();
  const headingId = $derived(id ? `${id}-title` : undefined);
</script>

<section class="card" {id} aria-labelledby={title ? headingId : undefined}>
  {#if title || actions}
    <div class="head">
      {#if title}
        <svelte:element this={`h${level}`} id={headingId}>{title}</svelte:element>
      {/if}
      {#if actions}<div class="actions">{@render actions()}</div>{/if}
    </div>
  {/if}
  {@render children()}
</section>

<style>
  .card {
    background: var(--color-surface);
    border: 1px solid var(--color-border-subtle);
    border-radius: var(--radius-lg);
    padding: var(--space-4);
    box-shadow: var(--shadow);
    min-width: 0;
  }
  .head {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    justify-content: space-between;
    gap: var(--space-2);
    margin-bottom: var(--space-3);
  }
  .head :global(h2),
  .head :global(h3) {
    margin: 0;
  }
</style>
