<script lang="ts">
  import { tick } from 'svelte';
  import Nav from './components/Nav.svelte';
  import { apiGet } from './lib/api';
  import { connectivity } from './lib/online.svelte';
  import { href, navigate, route } from './lib/router';
  import { session } from './lib/session.svelte';
  import { status } from './lib/status.svelte';
  import type { Approval } from './lib/types';
  import Approvals from './routes/Approvals.svelte';
  import Home from './routes/Home.svelte';
  import OpenFinance from './routes/OpenFinance.svelte';
  import Pay from './routes/Pay.svelte';
  import SignIn from './routes/SignIn.svelte';
  import Transparency from './routes/Transparency.svelte';

  const PUBLIC = new Set(['/sign-in', '/open-finance', '/transparency']);
  const TITLES: Record<string, string> = {
    '/': 'Home',
    '/pay': 'Pay',
    '/approvals': 'Approvals',
    '/open-finance': 'Open Finance',
    '/transparency': 'Transparency',
    '/sign-in': 'Sign in',
  };

  let mainEl: HTMLElement | undefined = $state();
  let inboxCount = $state(0);
  let firstRender = true;

  session.refresh();

  const path = $derived($route.path);
  const needsAuth = $derived(!PUBLIC.has(path));

  // Guard: signed-out users are sent to sign in, then back.
  $effect(() => {
    if (session.ready && !session.me && needsAuth && $route.known) {
      navigate('/sign-in', { next: path });
    }
  });

  // Title + focus management for screen readers on every navigation.
  $effect(() => {
    document.title = `${TITLES[path] ?? 'Not found'} · AutoDev Bank`;
    if (firstRender) {
      firstRender = false;
      return;
    }
    tick().then(() => mainEl?.focus());
  });

  // Keep the Approvals badge roughly current.
  $effect(() => {
    void path;
    if (!session.me) {
      inboxCount = 0;
      return;
    }
    apiGet<{ inbox: Approval[] }>('/api/approvals')
      .then((r) => (inboxCount = r.inbox.filter((a) => a.status === 'pending').length))
      .catch(() => {});
  });

  async function signOut() {
    await session.logout().catch(() => {});
    status.announce('Signed out.');
    navigate('/sign-in');
  }
</script>

<a class="skip-link" href="#main" onclick={(e) => { e.preventDefault(); mainEl?.focus(); }}>Skip to content</a>

<div class="shell">
  <header>
    <a class="brand" href={href(session.me ? '/' : '/sign-in')}>
      <img src="/icons/icon.svg" alt="" width="28" height="28" />
      <span>AutoDev Bank</span>
    </a>
    {#if session.me}
      <div class="who">
        <span class="name">{session.me.display_name}</span>
        <button type="button" class="secondary small" onclick={signOut}>Sign out</button>
      </div>
    {/if}
  </header>

  <Nav current={path} signedIn={!!session.me} {inboxCount} />

  <main id="main" tabindex="-1" bind:this={mainEl}>
    {#if !connectivity.online}
      <p class="offline" role="status">You are offline — showing cached data. Payments need a connection.</p>
    {/if}

    {#if !$route.known}
      <h1>Page not found</h1>
      <p><a href={href('/')}>Go to Home</a></p>
    {:else if path === '/sign-in'}
      <SignIn next={$route.params['next']} />
    {:else if path === '/open-finance'}
      <OpenFinance />
    {:else if path === '/transparency'}
      <Transparency />
    {:else if !session.ready}
      <p aria-busy="true">Loading…</p>
    {:else if session.me}
      {#if path === '/'}
        <Home me={session.me} />
      {:else if path === '/pay'}
        <Pay me={session.me} params={$route.params} />
      {:else if path === '/approvals'}
        <Approvals me={session.me} onchange={(n) => (inboxCount = n)} />
      {/if}
    {/if}
  </main>
</div>

<div class="visually-hidden" aria-live="polite" aria-atomic="true">{status.message}</div>

<style>
  .shell {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    grid-template-areas: 'header' 'main';
    min-height: 100dvh;
  }
  header {
    grid-area: header;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-3);
    padding: var(--space-2) var(--gutter);
    padding-top: max(var(--space-2), env(safe-area-inset-top));
    border-bottom: 1px solid var(--color-border-subtle);
    background: var(--color-bg);
  }
  .brand {
    display: inline-flex;
    align-items: center;
    gap: var(--space-2);
    min-height: 44px;
    font-weight: 700;
    font-size: var(--text-lg);
    color: var(--color-text);
    text-decoration: none;
    white-space: nowrap;
  }
  .who {
    display: flex;
    align-items: center;
    gap: var(--space-2);
    min-width: 0;
  }
  .name {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    color: var(--color-text-muted);
    font-size: var(--text-sm);
  }
  @media (max-width: 479px) {
    .name {
      display: none;
    }
  }
  main {
    grid-area: main;
    width: 100%;
    max-width: var(--content-max);
    margin: 0 auto;
    padding: var(--space-4) var(--gutter) calc(var(--tab-height) + 40px);
    min-width: 0;
  }
  main:focus {
    outline: none;
  }
  .offline {
    background: var(--color-banner-bg);
    color: var(--color-banner-text);
    border-radius: var(--radius-md);
    padding: var(--space-2) var(--space-3);
    font-weight: 600;
  }

  @media (min-width: 900px) {
    .shell {
      grid-template-columns: 220px minmax(0, 1fr);
      grid-template-areas: 'header header' 'nav main';
      grid-template-rows: auto 1fr;
    }
    .shell > :global(nav) {
      grid-area: nav;
    }
    main {
      padding-bottom: var(--space-7);
    }
  }
</style>
