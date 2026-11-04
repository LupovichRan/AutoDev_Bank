<script lang="ts">
  import Card from '../components/Card.svelte';
  import Problem from '../components/Problem.svelte';
  import { apiGet, apiPost, ProblemError } from '../lib/api';
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
  const DEMO_USERS = [
    { handle: 'alice', label: 'Alice (customer)' },
    { handle: 'bob', label: 'Bob (customer)' },
    { handle: 'carol', label: 'Carol (staff approver)' },
    { handle: 'dave', label: 'Dave (staff initiator)' },
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
      await done(`Signed in as ${h} (demo).`);
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
      await done('Signed in.');
    } catch (e) {
      error = toError(e);
    } finally {
      busy = null;
    }
  }

  async function onRegister(e: SubmitEvent) {
    e.preventDefault();
    error = null;
    handleError = HANDLE.test(handle.trim()) ? '' : '3–32 characters: lowercase letters, digits, dot, dash or underscore.';
    if (handleError || !displayName.trim()) return;
    busy = 'register';
    try {
      await register(handle.trim(), displayName.trim());
      await done('Account created and signed in.');
    } catch (err) {
      error = toError(err);
    } finally {
      busy = null;
    }
  }
</script>

<h1>Sign in</h1>
<p class="muted">AutoDev Bank uses passkeys only. No passwords, nothing to phish.</p>

{#if !supported}
  <Problem error={new Error('This browser does not support passkeys (WebAuthn). Try a current browser over https or localhost.')} />
{/if}

<div class="stack">
  {#if error}<Problem {error} />{/if}

  <Card title="Use your passkey" id="login">
    <p>Sign in with the passkey saved on this device or your phone.</p>
    <button type="button" onclick={onLogin} disabled={!supported || busy !== null} aria-busy={busy === 'login'}>
      {busy === 'login' ? 'Waiting for passkey…' : 'Sign in with passkey'}
    </button>
  </Card>

  {#if devLogin}
    <Card title="Demo sign-in (dev only)" id="demo">
      <p class="muted">This server runs with BANK_DEV_TEST_LOGIN=1, so you can sign in as a seeded demo user without a passkey. Never enable it in production.</p>
      <div class="row">
        {#each DEMO_USERS as u (u.handle)}
          <button type="button" class="secondary" onclick={() => onDemo(u.handle)} disabled={busy !== null}>
            Demo sign-in as {u.label}
          </button>
        {/each}
      </div>
    </Card>
  {/if}

  <Card title="New here? Create a passkey" id="register">
    <form onsubmit={onRegister} novalidate>
      <div class="field">
        <label for="handle">Handle</label>
        <input
          id="handle"
          name="handle"
          autocomplete="username webauthn"
          autocapitalize="none"
          spellcheck="false"
          required
          bind:value={handle}
          aria-invalid={handleError ? 'true' : undefined}
          aria-describedby="handle-hint{handleError ? ' handle-error' : ''}"
        />
        <p class="hint" id="handle-hint">A unique name, e.g. alice.</p>
        {#if handleError}<p class="field-error" id="handle-error">{handleError}</p>{/if}
      </div>
      <div class="field">
        <label for="display-name">Display name</label>
        <input id="display-name" name="display_name" autocomplete="name" required bind:value={displayName} />
      </div>
      <button type="submit" class="secondary" disabled={!supported || busy !== null} aria-busy={busy === 'register'}>
        {busy === 'register' ? 'Creating passkey…' : 'Create account'}
      </button>
    </form>
  </Card>
</div>
