<script lang="ts">
  import { t, type Key } from '../lib/i18n.svelte';
  import { href } from '../lib/router';

  interface Props {
    current: string;
    signedIn: boolean;
    inboxCount?: number;
  }
  let { current, signedIn, inboxCount = 0 }: Props = $props();

  interface Item {
    path: string;
    label: Key;
    icon: string;
  }

  // 24x24 stroke icons (decorative; the label is the accessible name).
  const ICONS: Record<string, string> = {
    home: 'M3 11l9-8 9 8M5 10v10h5v-6h4v6h5V10',
    pay: 'M4 12h14M13 6l6 6-6 6',
    approvals: 'M4 12l5 5L20 6',
    open: 'M3 3v18h18M7 15l4-4 3 3 6-6',
    transparency: 'M12 3l8 4v5c0 5-3.5 8-8 9-4.5-1-8-4-8-9V7z',
    signin: 'M15 3h4a2 2 0 012 2v14a2 2 0 01-2 2h-4M10 17l5-5-5-5M15 12H3',
  };

  const items = $derived<Item[]>(
    signedIn
      ? [
          { path: '/', label: 'title.home', icon: ICONS.home! },
          { path: '/pay', label: 'title.pay', icon: ICONS.pay! },
          { path: '/approvals', label: 'title.approvals', icon: ICONS.approvals! },
          { path: '/open-finance', label: 'title.openFinance', icon: ICONS.open! },
          { path: '/transparency', label: 'title.transparency', icon: ICONS.transparency! },
        ]
      : [
          { path: '/sign-in', label: 'title.signIn', icon: ICONS.signin! },
          { path: '/open-finance', label: 'title.openFinance', icon: ICONS.open! },
          { path: '/transparency', label: 'title.transparency', icon: ICONS.transparency! },
        ],
  );
</script>

<nav aria-label={t('nav.main')}>
  <ul>
    {#each items as item (item.path)}
      <li>
        <a href={href(item.path)} aria-current={current === item.path ? 'page' : undefined}>
          <svg viewBox="0 0 24 24" width="24" height="24" aria-hidden="true" focusable="false">
            <path d={item.icon} fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" />
          </svg>
          <span class="label">{t(item.label)}</span>
          {#if item.path === '/approvals' && inboxCount > 0}
            <span class="count">{inboxCount}<span class="visually-hidden">{t('nav.waiting')}</span></span>
          {/if}
        </a>
      </li>
    {/each}
  </ul>
</nav>

<style>
  nav {
    position: fixed;
    inset: auto 0 0 0;
    z-index: 10;
    background: var(--color-surface-raised);
    border-top: 1px solid var(--color-border-subtle);
    padding-bottom: env(safe-area-inset-bottom);
  }
  ul {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
  }
  li {
    flex: 1 1 0;
    min-width: 0;
  }
  a {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 2px;
    min-height: calc(var(--tab-height) + 12px);
    padding: var(--space-1);
    color: var(--color-text-muted);
    text-decoration: none;
    font-size: var(--text-xs);
    font-weight: 600;
    text-align: center;
  }
  a[aria-current='page'] {
    color: var(--color-primary);
  }
  a[aria-current='page']::before {
    content: '';
    position: absolute;
    inset-block-start: 0;
    inset-inline: 25%;
    height: 3px;
    border-radius: 0 0 3px 3px;
    background: var(--color-primary);
  }
  .label {
    line-height: 1.15;
    max-width: 100%;
    overflow-wrap: anywhere;
  }
  .count {
    position: absolute;
    inset-block-start: 2px;
    inset-inline-end: calc(50% - 22px);
    min-width: 18px;
    padding: 0 4px;
    border-radius: var(--radius-pill);
    background: var(--color-negative);
    color: var(--color-bg);
    font-size: 11px;
    line-height: 18px;
  }

  @media (min-width: 900px) {
    nav {
      position: sticky;
      top: 0;
      inset: auto;
      align-self: start;
      max-height: 100dvh;
      overflow-y: auto;
      width: 220px;
      border-top: 0;
      border-inline-end: 1px solid var(--color-border-subtle);
      padding: var(--space-4) var(--space-2);
    }
    ul {
      flex-direction: column;
      gap: var(--space-1);
    }
    a {
      flex-direction: row;
      justify-content: flex-start;
      gap: var(--space-3);
      min-height: var(--tab-height);
      padding-block: var(--space-2);
      padding-inline: var(--space-3);
      border-radius: var(--radius-md);
      font-size: var(--text-md);
      text-align: start;
    }
    a[aria-current='page'] {
      background: var(--color-surface);
    }
    a[aria-current='page']::before {
      inset-block: 8px;
      inset-inline: 0 auto;
      width: 3px;
      height: auto;
      border-radius: 0;
      border-start-end-radius: 3px;
      border-end-end-radius: 3px;
    }
    .count {
      position: static;
      margin-inline-start: auto;
    }
  }
</style>
