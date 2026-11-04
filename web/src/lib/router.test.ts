import { describe, expect, it } from 'vitest';
import { get } from 'svelte/store';
import { createRouter, parseHash, ROUTES, type HashHost } from './router';

class FakeHost implements HashHost {
  hash = '';
  private listeners: Array<() => void> = [];
  get location() {
    return { hash: this.hash };
  }
  setHash(h: string) {
    this.hash = h.startsWith('#') ? h : `#${h}`;
    this.listeners.forEach((l) => l());
  }
  addEventListener(_t: 'hashchange', l: () => void) {
    this.listeners.push(l);
  }
  removeEventListener(_t: 'hashchange', l: () => void) {
    this.listeners = this.listeners.filter((x) => x !== l);
  }
  get listenerCount() {
    return this.listeners.length;
  }
}

describe('parseHash', () => {
  it('maps empty hash to home', () => {
    expect(parseHash('')).toEqual({ path: '/', params: {}, known: true });
    expect(parseHash('#')).toEqual({ path: '/', params: {}, known: true });
    expect(parseHash('#/')).toEqual({ path: '/', params: {}, known: true });
  });

  it('recognises every screen', () => {
    for (const p of ['/', '/pay', '/approvals', '/open-finance', '/transparency', '/sign-in']) {
      expect(parseHash(`#${p}`).path).toBe(p);
      expect(parseHash(`#${p}`).known).toBe(true);
    }
    expect(ROUTES).toHaveLength(6);
  });

  it('parses query parameters', () => {
    expect(parseHash('#/pay?to=acc_2&amount=10.5')).toEqual({
      path: '/pay',
      params: { to: 'acc_2', amount: '10.5' },
      known: true,
    });
  });

  it('tolerates a trailing slash and missing leading slash', () => {
    expect(parseHash('#/pay/').path).toBe('/pay');
    expect(parseHash('#pay').path).toBe('/pay');
  });

  it('flags unknown paths', () => {
    expect(parseHash('#/nope')).toEqual({ path: '/nope', params: {}, known: false });
  });
});

describe('createRouter', () => {
  it('reflects the current hash and follows hashchange', () => {
    const host = new FakeHost();
    host.hash = '#/pay';
    const r = createRouter(host);
    const seen: string[] = [];
    const unsub = r.route.subscribe((v) => seen.push(v.path));
    host.setHash('#/approvals');
    expect(get(r.route).path).toBe('/approvals');
    expect(seen).toEqual(['/pay', '/approvals']);
    unsub();
    expect(host.listenerCount).toBe(0);
  });

  it('navigate sets the hash', () => {
    const host = new FakeHost();
    const r = createRouter(host, (h) => host.setHash(h));
    const unsub = r.route.subscribe(() => {});
    r.navigate('/transparency');
    expect(host.hash).toBe('#/transparency');
    expect(get(r.route).path).toBe('/transparency');
    r.navigate('open-finance');
    expect(host.hash).toBe('#/open-finance');
    unsub();
  });

  it('href builds a hash link', () => {
    const r = createRouter(new FakeHost());
    expect(r.href('/pay')).toBe('#/pay');
    expect(r.href('/pay', { to: 'a b' })).toBe('#/pay?to=a+b');
  });
});
