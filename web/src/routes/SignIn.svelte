<script lang="ts">
  import Card from '../components/Card.svelte';
  import Problem from '../components/Problem.svelte';
  import { apiGet, apiPost, ProblemError } from '../lib/api';
  import { t, type Key } from '../lib/i18n.svelte';
  import { navigate, ROUTES } from '../lib/router';
  import { session } from '../lib/session.svelte';
  import { status } from '../lib/status.svelte';
  import type { AppConfig } from '../lib/types';
  import { describeWebAuthnError, login, register, webauthnSupported } from '../lib/webauthn';

  interface Props {
    next?: string;
  }
  let { next }: Props = $props();

  const supported = webauthnSupported();
  let busy = $state<'login' | 'register' | 'demo' | null>(null);

  // Demo sign-in exists only when the server runs with BANK_DEV_TEST_LOGIN=1
  // (./dev demo, e2e). It skips passkeys, so it is never offered otherwise.
  const DEMO_USERS: { handle: string; label: Key }[] = [
    { handle: 'alice', label: 'signin.demo.alice' },
    { handle: 'bob', label: 'signin.demo.bob' },
    { handle: 'carol', label: 'signin.demo.carol' },
    { handle: 'dave', label: 'signin.demo.dave' },
  ];
  let devLogin = $state(false);
  apiGet<AppConfig>('/api/config')
    .then((c) => (devLogin = c.dev_test_login === true))
    .catch(() => (devLogin = false));

  async function onDemo(h: string) {
    error = null;
    busy = 'demo';
    try {
      await apiPost('/api/auth/dev-login', { handle: h });
      await done(t('signin.signedInDemo', { handle: h }));
    } catch (e) {
      error = toError(e);
    } finally {
      busy = null;
    }
  }
  let error = $state<unknown>(null);
  let handle = $state('');
  let displayName = $state('');
  let handleError = $state('');

  const HANDLE = /^[a-z0-9][a-z0-9._-]{2,31}$/;

  function destination(): string {
    return next && (ROUTES as readonly string[]).includes(next) && next !== '/sign-in' ? next : '/';
  }

  async function done(message: string) {
    await session.refresh();
    status.announce(message);
    navigate(destination());
  }

  function toError(e: unknown): unknown {
    return e instanceof ProblemError ? e : new Error(describeWebAuthnError(e));
  }

  async function onLogin() {
    error = null;
    busy = 'login';
    try {
      await login();
      await done(t('signin.signedIn'));
    } catch (e) {
      error = toError(e);
    } finally {
      busy = null;
    }
  }

  async function onRegister(e: SubmitEvent) {
    e.preventDefault();
    error = null;
    handleError = HANDLE.test(handle.trim()) ? '' : t('signin.handleError');
    if (handleError || !displayName.trim()) return;
    busy = 'register';
    try {
      await register(handle.trim(), displayName.trim());
      await done(t('signin.created'));
    } catch (err) {
      error = toError(err);
    } finally {
      busy = null;
    }
  }
</script>

<h1>{t('title.signIn')}</h1>
<p class="muted">{t('signin.lead')}</p>

{#if !supported}
  <Problem error={new Error(t('signin.unsupported'))} />
{/if}

<div class="stack">
  {#if error}<Problem {error} />{/if}

  <Card title={t('signin.useTitle')} id="login">
    <p>{t('signin.useBody')}</p>
    <button type="button" onclick={onLogin} disabled={!supported || busy !== null} aria-busy={busy === 'login'}>
      {busy === 'login' ? t('signin.waiting') : t('signin.withPasskey')}
    </button>
  </Card>

  {#if devLogin}
    <Card title={t('signin.demoTitle')} id="demo">
      <p class="muted">{t('signin.demoBody')}</p>
      <div class="row">
        {#each DEMO_USERS as u (u.handle)}
          <button type="button" class="secondary" onclick={() => onDemo(u.handle)} disabled={busy !== null}>
            {t('signin.demoAs', { who: t(u.label) })}
          </button>
        {/each}
      </div>
    </Card>
  {/if}

  <Card title={t('signin.registerTitle')} id="register">
    <form onsubmit={onRegister} novalidate>
      <div class="field">
        <label for="handle">{t('signin.handle')}</label>
        <input
          id="handle"
          name="handle"
          autocomplete="username webauthn"
          autocapitalize="none"
          spellcheck="false"
          dir="ltr"
          required
          bind:value={handle}
          aria-invalid={handleError ? 'true' : undefined}
          aria-describedby="handle-hint{handleError ? ' handle-error' : ''}"
        />
        <p class="hint" id="handle-hint">{t('signin.handleHint')}</p>
        {#if handleError}<p class="field-error" id="handle-error">{handleError}</p>{/if}
      </div>
      <div class="field">
        <label for="display-name">{t('signin.displayName')}</label>
        <input id="display-name" name="display_name" autocomplete="name" required bind:value={displayName} />
      </div>
      <button type="submit" class="secondary" disabled={!supported || busy !== null} aria-busy={busy === 'register'}>
        {busy === 'register' ? t('signin.creating') : t('signin.create')}
      </button>
    </form>
  </Card>
</div>
