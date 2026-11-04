// Thin fetch wrapper. Session lives in an HttpOnly cookie; errors are
// RFC 9457 problem details and surface as ProblemError.

export interface Problem {
  type: string;
  title: string;
  status: number;
  detail: string;
}

export class ProblemError extends Error implements Problem {
  readonly type: string;
  readonly title: string;
  readonly status: number;
  readonly detail: string;

  constructor(p: Problem) {
    super(p.detail || p.title || `HTTP ${p.status}`);
    this.name = 'ProblemError';
    this.type = p.type;
    this.title = p.title;
    this.status = p.status;
    this.detail = p.detail;
  }
}

/** The request never got an HTTP response (offline, DNS, connection reset). */
export class NetworkError extends Error {
  constructor(cause: unknown) {
    super('Network unavailable. Check your connection and try again.');
    this.name = 'NetworkError';
    this.cause = cause;
  }
}

type UnauthorizedHandler = (path: string) => void;
let onUnauthorized: UnauthorizedHandler | null = null;

/** Called on any 401 (except /api/me, which the session store handles itself). */
export function setUnauthorizedHandler(h: UnauthorizedHandler | null): void {
  onUnauthorized = h;
}

async function toProblem(res: Response): Promise<ProblemError> {
  let body: Partial<Problem> = {};
  const ct = res.headers.get('content-type') ?? '';
  if (ct.includes('json')) {
    try {
      body = (await res.json()) as Partial<Problem>;
    } catch {
      body = {};
    }
  }
  return new ProblemError({
    type: body.type ?? 'about:blank',
    title: body.title ?? (res.statusText || 'Request failed'),
    status: body.status ?? res.status,
    detail: body.detail ?? '',
  });
}

async function request<T>(
  method: 'GET' | 'POST',
  path: string,
  body?: unknown,
  extraHeaders?: Record<string, string>,
): Promise<{ status: number; data: T }> {
  const headers: Record<string, string> = { Accept: 'application/json, application/problem+json', ...extraHeaders };
  const init: RequestInit = { method, credentials: 'include', headers };
  if (body !== undefined) {
    init.body = JSON.stringify(body);
    headers['Content-Type'] = 'application/json';
  }
  let res: Response;
  try {
    res = await fetch(path, init);
  } catch (e) {
    throw new NetworkError(e);
  }
  if (!res.ok) {
    const problem = await toProblem(res);
    if (res.status === 401 && path !== '/api/me') onUnauthorized?.(path);
    throw problem;
  }
  const text = res.status === 204 ? '' : await res.text();
  return { status: res.status, data: (text ? JSON.parse(text) : undefined) as T };
}

export async function apiGet<T>(path: string): Promise<T> {
  return (await request<T>('GET', path)).data;
}

export async function apiPost<T>(path: string, body: unknown = {}, headers?: Record<string, string>): Promise<T> {
  return (await request<T>('POST', path, body, headers)).data;
}

/** Like apiPost, but also returns the HTTP status (201 vs 202 matters for transfers). */
export function apiPostWithStatus<T>(
  path: string,
  body: unknown,
  headers?: Record<string, string>,
): Promise<{ status: number; data: T }> {
  return request<T>('POST', path, body, headers);
}
