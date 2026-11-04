// Who is signed in. `me` is null when signed out; `ready` flips once the
// first /api/me answer (or failure) is in.
import { apiGet, apiPost, NetworkError, ProblemError, setUnauthorizedHandler } from './api';
import { navigate, parseHash } from './router';
import type { Me } from './types';

class Session {
  me = $state<Me | null>(null);
  ready = $state(false);

  async refresh(): Promise<Me | null> {
    try {
      this.me = await apiGet<Me>('/api/me');
      remember(this.me);
    } catch (e) {
      if (e instanceof ProblemError && e.status === 401) {
        this.me = null;
        await clearCachedAccountData();
      } else if (e instanceof NetworkError && !this.me) {
        // Offline start: show cached balances for the last known user. Any
        // write still needs the server, which re-checks the session cookie.
        this.me = recall();
      }
    } finally {
      this.ready = true;
    }
    return this.me;
  }

  async logout(): Promise<void> {
    try {
      await apiPost('/api/auth/logout');
    } finally {
      this.me = null;
      await clearCachedAccountData();
    }
  }
}

const ME_KEY = 'autodev.me';

function remember(me: Me): void {
  try {
    localStorage.setItem(ME_KEY, JSON.stringify(me));
  } catch {
    // storage unavailable
  }
}

function recall(): Me | null {
  try {
    const raw = localStorage.getItem(ME_KEY);
    return raw ? (JSON.parse(raw) as Me) : null;
  } catch {
    return null;
  }
}

/** Offline copies of balances must not outlive the session on a shared device. */
async function clearCachedAccountData(): Promise<void> {
  try {
    localStorage.removeItem(ME_KEY);
  } catch {
    // storage unavailable
  }
  try {
    if (typeof caches !== 'undefined') await caches.delete('autodev-api');
  } catch {
    // Cache Storage unavailable (private mode); nothing was cached.
  }
}

export const session = new Session();

/** Any 401 mid-session: drop cached identity and data, then go to sign-in (unless already there). */
export function handleUnauthorized(path = ''): void {
  // A failed login/registration ceremony is not an expired session.
  if (path.startsWith('/api/auth/')) return;
  session.me = null;
  void clearCachedAccountData();
  if (typeof location === 'undefined') return;
  const current = parseHash(location.hash);
  if (current.path === '/sign-in') return;
  navigate('/sign-in', { next: current.path });
}

setUnauthorizedHandler(handleUnauthorized);
