import { afterEach, describe, expect, it, vi } from 'vitest';
import { apiGet, apiPost, apiPostWithStatus, NetworkError, ProblemError } from './api';

afterEach(() => vi.unstubAllGlobals());

function stubFetch(res: Response | Error) {
  const fn = vi.fn(async () => {
    if (res instanceof Error) throw res;
    return res;
  });
  vi.stubGlobal('fetch', fn);
  return fn;
}

describe('api', () => {
  it('GET sends credentials and parses JSON', async () => {
    const f = stubFetch(new Response(JSON.stringify({ ok: true }), { headers: { 'content-type': 'application/json' } }));
    await expect(apiGet('/api/me')).resolves.toEqual({ ok: true });
    const init = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(init[1].credentials).toBe('include');
    expect(init[1].method).toBe('GET');
  });

  it('POST sends JSON body and extra headers', async () => {
    const f = stubFetch(new Response('{"x":1}', { status: 201 }));
    const r = await apiPostWithStatus('/api/transfers', { a: 1 }, { 'Idempotency-Key': 'k' });
    expect(r).toEqual({ status: 201, data: { x: 1 } });
    const [, init] = f.mock.calls[0] as unknown as [string, RequestInit];
    expect(init.body).toBe('{"a":1}');
    expect((init.headers as Record<string, string>)['Idempotency-Key']).toBe('k');
    expect((init.headers as Record<string, string>)['Content-Type']).toBe('application/json');
  });

  it('throws ProblemError from problem+json', async () => {
    stubFetch(
      new Response(JSON.stringify({ type: 'about:blank', title: 'Forbidden', status: 403, detail: 'step_up_required' }), {
        status: 403,
        headers: { 'content-type': 'application/problem+json' },
      }),
    );
    const err = await apiPost('/api/transfers', {}).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(ProblemError);
    expect((err as ProblemError).status).toBe(403);
    expect((err as ProblemError).detail).toBe('step_up_required');
  });

  it('builds a ProblemError for non-JSON errors', async () => {
    stubFetch(new Response('boom', { status: 502, statusText: 'Bad Gateway' }));
    const err = (await apiGet('/api/x').catch((e: unknown) => e)) as ProblemError;
    expect(err.status).toBe(502);
    expect(err.title).toBe('Bad Gateway');
  });

  it('wraps fetch failures in NetworkError', async () => {
    stubFetch(new TypeError('Failed to fetch'));
    await expect(apiGet('/api/x')).rejects.toBeInstanceOf(NetworkError);
  });

  it('handles empty 204 bodies', async () => {
    stubFetch(new Response(null, { status: 204 }));
    await expect(apiPost('/api/auth/logout')).resolves.toBeUndefined();
  });
});
