// In-house hash router: #/path?query. No dependencies beyond svelte/store.
import { readable, type Readable } from 'svelte/store';

export const ROUTES = ['/', '/pay', '/approvals', '/open-finance', '/transparency', '/sign-in'] as const;
export type RoutePath = (typeof ROUTES)[number];

export interface Route {
  path: string;
  params: Record<string, string>;
  known: boolean;
}

/** The parts of `window` the router needs; injectable for tests. */
export interface HashHost {
  readonly location: { hash: string };
  addEventListener(type: 'hashchange', listener: () => void): void;
  removeEventListener(type: 'hashchange', listener: () => void): void;
}

export function parseHash(hash: string): Route {
  let raw = hash.startsWith('#') ? hash.slice(1) : hash;
  const q = raw.indexOf('?');
  const query = q >= 0 ? raw.slice(q + 1) : '';
  raw = q >= 0 ? raw.slice(0, q) : raw;
  if (!raw.startsWith('/')) raw = `/${raw}`;
  if (raw.length > 1 && raw.endsWith('/')) raw = raw.replace(/\/+$/, '') || '/';
  const params: Record<string, string> = {};
  for (const [k, v] of new URLSearchParams(query)) params[k] = v;
  return { path: raw, params, known: (ROUTES as readonly string[]).includes(raw) };
}

export function createRouter(host: HashHost, setHash?: (hash: string) => void) {
  const route: Readable<Route> = readable(parseHash(host.location.hash), (set) => {
    const onChange = () => set(parseHash(host.location.hash));
    host.addEventListener('hashchange', onChange);
    onChange();
    return () => host.removeEventListener('hashchange', onChange);
  });

  function href(path: string, params?: Record<string, string>): string {
    const p = path.startsWith('/') ? path : `/${path}`;
    const qs = params ? new URLSearchParams(params).toString() : '';
    return `#${p}${qs ? `?${qs}` : ''}`;
  }

  function navigate(path: string, params?: Record<string, string>): void {
    const h = href(path, params);
    if (setHash) setHash(h);
    else host.location.hash = h;
  }

  return { route, navigate, href };
}

const browserHost: HashHost =
  typeof window !== 'undefined'
    ? window
    : { location: { hash: '' }, addEventListener() {}, removeEventListener() {} };

const appRouter = createRouter(browserHost);
export const route = appRouter.route;
export const navigate = appRouter.navigate;
export const href = appRouter.href;
