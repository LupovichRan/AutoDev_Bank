<script lang="ts">
  import { tick } from 'svelte';
  import Nav from './components/Nav.svelte';
  import { apiGet } from './lib/api';
  import { i18n, t, type Key, type Lang } from './lib/i18n.svelte';
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
  const TITLES: Record<string, Key> = {
    '/': 'title.home',
    '/pay': 'title.pay',
    '/approvals': 'title.approvals',
    '/open-finance': 'title.openFinance',
    '/transparency': 'title.transparency',
    '/sign-in': 'title.signIn',
  };
  const LANGS: { lang: Lang; label: string }[] = [
    { lang: 'he', label: 'עברית' },
    { lang: 'en', label: 'English' },
  ];

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
    const key = TITLES[path];
    document.title = `${t(key ?? 'title.notFound')} · AutoDev Bank`;
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
    status.announce(t('app.signedOut'));
    navigate('/sign-in');
  }
</script>

<a class="skip-link" href="#main" onclick={(e) => { e.preventDefault(); mainEl?.focus(); }}>{t('app.skip')}</a>

<div class="shell">
  <header class:signed-in={!!session.me}>
    <a class="brand" href={href(session.me ? '/' : '/sign-in')}>
      <img src="/icons/icon.svg" alt="" width="28" height="28" />
      <span class="brand-name" lang="en" dir="ltr">AutoDev Bank</span>
    </a>
    <div class="who">
      <div class="lang" role="group" aria-label={t('lang.label')}>
        {#each LANGS as l, i (l.lang)}
          {#if i > 0}<span class="sep" aria-hidden="true">|</span>{/if}
          <button
            type="button"
            class="lang-btn"
            lang={l.lang}
            aria-pressed={i18n.lang === l.lang}
            onclick={() => i18n.set(l.lang)}
          >{l.label}</button>
        {/each}
      </div>
      {#if session.me}
        <span class="name">{session.me.display_name}</span>
        <button type="button" class="secondary small" onclick={signOut}>{t('app.signOut')}</button>
      {/if}
    </div>
  </header>

  <Nav current={path} signedIn={!!session.me} {inboxCount} />

  <main id="main" tabindex="-1" bind:this={mainEl}>
    {#if !connectivity.online}
      <p class="offline" role="status">{t('app.offline')}</p>
    {/if}

    {#if !$route.known}
      <h1>{t('app.pageNotFound')}</h1>
      <p><a href={href('/')}>{t('app.goHome')}</a></p>
    {:else if path === '/sign-in'}
      <SignIn next={$route.params['next']} />
    {:else if path === '/open-finance'}
      <OpenFinance />
    {:else if path === '/transparency'}
      <Transparency />
    {:else if !session.ready}
      <p aria-busy="true">{t('app.loading')}</p>
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
    gap: var(--space-2);
    padding-block: var(--space-2);
    padding-inline: var(--gutter);
    padding-top: max(var(--space-2), env(safe-area-inset-top));
    border-bottom: 1px solid var(--color-border-subtle);
    background: var(--color-bg);
    min-width: 0;
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
  .lang {
    display: inline-flex;
    align-items: center;
    flex: none;
  }
  .lang-btn {
    min-height: 44px;
    min-width: 44px;
    padding: 0 var(--space-2);
    background: transparent;
    border-color: transparent;
    color: var(--color-text-muted);
    font-size: var(--text-sm);
    font-weight: 600;
  }
  .lang-btn[aria-pressed='true'] {
    color: var(--color-primary);
    text-decoration: underline;
    text-underline-offset: 4px;
  }
  .sep {
    color: var(--color-border);
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
    /* Phone width with Sign out: the icon stays, the name stays the link's accessible name. */
    .signed-in .brand-name {
      position: absolute;
      width: 1px;
      height: 1px;
      overflow: hidden;
      clip: rect(0 0 0 0);
      white-space: nowrap;
    }
  }
  main {
    grid-area: main;
    width: 100%;
    max-width: var(--content-max);
    margin: 0 auto;
    padding-block: var(--space-4) calc(var(--tab-height) + 40px);
    padding-inline: var(--gutter);
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
      padding-block-end: var(--space-7);
    }
  }
</style>
