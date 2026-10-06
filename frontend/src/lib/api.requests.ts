import { record as recordPerf, templatePath } from './perfRing';
import { markBackendReachable, markBackendUnreachable, reportNetworkFailure } from './backendStatus';

export class AuthError extends Error {
  constructor(message = 'unauthorized') {
    super(message);
    this.name = 'AuthError';
  }
}

type AuthErrorHandler = (err: AuthError) => void;
let onAuthError: AuthErrorHandler = () => {};

export function registerAuthErrorHandler(handler: AuthErrorHandler): AuthErrorHandler {
  const previous = onAuthError;
  onAuthError = handler;
  return previous;
}

export class BackendUnavailableError extends Error {
  constructor(message = 'Backend is not responding. Check that ocman is running, then reload the page.', cause?: unknown) {
    super(message, { cause });
    this.name = 'BackendUnavailableError';
  }
}

export class APIError extends Error {
  readonly status: number;
  constructor(message: string, status: number) {
    super(message);
    this.name = 'APIError';
    this.status = status;
  }
}

function toBackendError(err: unknown, stage: 'connect' | 'response'): unknown {
  // Cancellation is control flow, not a connection failure.
  if (err instanceof DOMException && err.name === 'AbortError') return err;
  if (err instanceof TypeError || err instanceof SyntaxError ||
      (err instanceof DOMException && ['SyntaxError', 'NetworkError'].includes(err.name))) {
    return new BackendUnavailableError(stage === 'connect'
      ? 'Could not connect to ocman. Check your connection and that ocman is running, then retry.'
      : 'Received an invalid response from ocman. The request could not be confirmed. Reload the page; if this continues, check ocman and any reverse proxy.', err);
  }
  return err;
}

// Forge endpoints can return their own 502/504 envelopes. Share connection
// error handling without interpreting those statuses as an ocman outage.
export async function fetchResponse(input: string, init?: RequestInit): Promise<Response> {
  try {
    return await fetch(input, init);
  } catch (err) {
    const mapped = toBackendError(err, 'connect');
    if (mapped instanceof BackendUnavailableError) reportNetworkFailure(mapped.message);
    throw mapped;
  }
}

export async function apiFetch(input: string, init?: RequestInit): Promise<Response> {
  const resp = await fetchResponse(input, init);
  if (resp.status === 502 || resp.status === 504) markBackendUnreachable(statusLine(resp));
  else markBackendReachable();
  return resp;
}

export async function readJSON<T>(resp: Response): Promise<T> {
  try {
    return await resp.json() as T;
  } catch (err) {
    throw toBackendError(err, 'response');
  }
}

function statusLine(resp: Response): string {
  return `HTTP ${resp.status} ${resp.statusText}`.trim();
}

export function raiseAuthError(message = 'unauthorized'): AuthError {
  const err = new AuthError(message);
  onAuthError(err);
  return err;
}

export async function raiseForUnauthorized(resp: Response): Promise<void> {
  if (resp.status !== 401) return;
  throw raiseAuthError((await resp.text().catch(() => '')) || undefined);
}

async function throwForStatus(resp: Response): Promise<never> {
  const body = await resp.text().catch(() => '');
  if (resp.status === 401) throw raiseAuthError(body || 'unauthorized');
  throw new APIError(envelopeMessage(body) ?? (body || statusLine(resp)), resp.status);
}

function envelopeMessage(body: string): string | undefined {
  try {
    const parsed = JSON.parse(body) as { error?: { message?: unknown } };
    const msg = parsed?.error?.message;
    return typeof msg === 'string' && msg !== '' ? msg : undefined;
  } catch {
    return undefined;
  }
}

export async function fetchJSON<T>(url: string, signal?: AbortSignal): Promise<T> {
  const startedAt = performance.now();
  let status = 0;
  try {
    const resp = await apiFetch(url, signal ? { signal } : undefined);
    status = resp.status;
    if (!resp.ok) await throwForStatus(resp);
    return await readJSON<T>(resp);
  } finally {
    recordPerf({ pathTemplate: templatePath(url), method: 'GET', status,
      durationMs: performance.now() - startedAt, startedAt });
  }
}

export async function postJSON<TResp, TReq = unknown>(
  url: string, body: TReq,
  opts?: { signal?: AbortSignal; parseJSON?: boolean; method?: 'POST' | 'PATCH' | 'PUT' | 'DELETE'; acceptStatus?: number },
): Promise<TResp> {
  const method = opts?.method ?? 'POST';
  const startedAt = performance.now();
  let status = 0;
  try {
    const hasBody = body !== undefined;
    const resp = await apiFetch(url, {
      method, headers: hasBody ? { 'Content-Type': 'application/json' } : undefined,
      body: hasBody ? JSON.stringify(body) : undefined, signal: opts?.signal,
    });
    status = resp.status;
    if (!resp.ok && resp.status !== opts?.acceptStatus) await throwForStatus(resp);
    if (opts?.parseJSON === false || resp.status === 204) return undefined as unknown as TResp;
    return await readJSON<TResp>(resp);
  } finally {
    recordPerf({ pathTemplate: templatePath(url), method, status,
      durationMs: performance.now() - startedAt, startedAt });
  }
}

export function queryString(params?: Record<string, string | number | undefined | null>): string {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params ?? {})) {
    if (v == null || v === '') continue;
    q.set(k, String(v));
  }
  const qs = q.toString();
  return qs ? '?' + qs : '';
}
