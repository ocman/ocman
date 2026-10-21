// @vitest-environment jsdom
import { afterEach, beforeEach, describe, it, expect, vi } from 'vitest';
import { installAuthIntegration, useAuthStore } from './authStore';
import { raiseAuthError, registerAuthErrorHandler } from './api.requests';

// The store is a singleton across tests; reset it between cases so
// one test's state doesn't leak into the next. These are the same
// defaults set at module load.
function resetStore() {
  useAuthStore.setState({
    checking: true,
    authRequired: false,
    authenticated: true,
    error: null,
    submitting: false,
  });
}

function stubFetch(responder: (url: string, init?: RequestInit) => Response | Promise<Response>) {
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : (input as Request).url;
    return Promise.resolve(responder(url, init));
  }));
}

beforeEach(() => {
  resetStore();
  sessionStorage.clear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  sessionStorage.clear();
});

describe('bootstrap', () => {
  it('marks checking false and mirrors /api/auth/me response', async () => {
    stubFetch(() => new Response(
      JSON.stringify({ authRequired: true, authenticated: false }),
      { status: 200 },
    ));
    await useAuthStore.getState().bootstrap();
    const s = useAuthStore.getState();
    expect(s.checking).toBe(false);
    expect(s.authRequired).toBe(true);
    expect(s.authenticated).toBe(false);
  });

  it('handles auth-off servers cleanly', async () => {
    stubFetch(() => new Response(
      JSON.stringify({ authRequired: false, authenticated: true }),
      { status: 200 },
    ));
    await useAuthStore.getState().bootstrap();
    const s = useAuthStore.getState();
    expect(s.authRequired).toBe(false);
    expect(s.authenticated).toBe(true);
  });

  it('keeps checking during backend startup instead of assuming authentication is disabled', async () => {
    stubFetch(() => new Response('nope', { status: 500 }));
    await useAuthStore.getState().bootstrap();
    const s = useAuthStore.getState();
    expect(s.checking).toBe(true);
    expect(s.authRequired).toBe(false);
    expect(s.authenticated).toBe(true);
  });

  it('writes authRequired=true to sessionStorage after a successful auth-on response', async () => {
    stubFetch(() => new Response(
      JSON.stringify({ authRequired: true, authenticated: false }),
      { status: 200 },
    ));
    await useAuthStore.getState().bootstrap();
    expect(sessionStorage.getItem('ocman:authRequired')).toBe('true');
  });

  it('writes authRequired=false to sessionStorage after a successful auth-off response', async () => {
    stubFetch(() => new Response(
      JSON.stringify({ authRequired: false, authenticated: true }),
      { status: 200 },
    ));
    await useAuthStore.getState().bootstrap();
    expect(sessionStorage.getItem('ocman:authRequired')).toBe('false');
  });

  it('does not overwrite cached authentication requirements on a transient failure', async () => {
    sessionStorage.setItem('ocman:authRequired', 'true');
    stubFetch(() => new Response('nope', { status: 500 }));
    await useAuthStore.getState().bootstrap();
    expect(sessionStorage.getItem('ocman:authRequired')).toBe('true');
  });

  it('preserves a confirmed session during a transient backend failure', async () => {
    useAuthStore.setState({ checking: false, authRequired: true, authenticated: true });
    stubFetch(() => new Response('starting', { status: 503 }));
    expect(await useAuthStore.getState().bootstrap()).toBe(false);
    expect(useAuthStore.getState()).toMatchObject({ checking: false, authRequired: true, authenticated: true });
  });

  it('retains compatibility only when a legacy auth endpoint is absent', async () => {
    stubFetch(() => new Response('not found', { status: 404 }));
    expect(await useAuthStore.getState().bootstrap()).toBe(true);
    expect(useAuthStore.getState()).toMatchObject({ checking: false, authRequired: false, authenticated: true });
  });
});

describe('login', () => {
  it('returns true and authenticates on 200', async () => {
    stubFetch(() => new Response(JSON.stringify({ ok: true }), { status: 200 }));
    const ok = await useAuthStore.getState().login('hunter2');
    expect(ok).toBe(true);
    const s = useAuthStore.getState();
    expect(s.authenticated).toBe(true);
    expect(s.submitting).toBe(false);
    expect(s.error).toBeNull();
  });

  it('returns false and records a friendly error on 401', async () => {
    stubFetch(() => new Response('invalid password', { status: 401 }));
    const ok = await useAuthStore.getState().login('wrong');
    expect(ok).toBe(false);
    const s = useAuthStore.getState();
    expect(s.authenticated).toBe(true); // unchanged (was still default)
    expect(s.submitting).toBe(false);
    expect(s.error).toBe('Incorrect password.');
  });

  it('surfaces generic errors on 500', async () => {
    stubFetch(() => new Response('boom', { status: 500 }));
    const ok = await useAuthStore.getState().login('anything');
    expect(ok).toBe(false);
    expect(useAuthStore.getState().error).toBe('boom');
  });

  it('clears stale error on the next attempt', async () => {
    useAuthStore.setState({ error: 'old error' });
    stubFetch(() => new Response(JSON.stringify({ ok: true }), { status: 200 }));
    await useAuthStore.getState().login('hunter2');
    expect(useAuthStore.getState().error).toBeNull();
  });
});

describe('logout', () => {
  it('flips authenticated off even if the server call succeeds', async () => {
    useAuthStore.setState({ authenticated: true });
    stubFetch(() => new Response(null, { status: 204 }));
    await useAuthStore.getState().logout();
    expect(useAuthStore.getState().authenticated).toBe(false);
  });

  it('flips authenticated off even if the server call fails', async () => {
    useAuthStore.setState({ authenticated: true });
    stubFetch(() => new Response('nope', { status: 500 }));
    await useAuthStore.getState().logout();
    expect(useAuthStore.getState().authenticated).toBe(false);
  });
});

describe('handleAuthError', () => {
  it('still locks a confirmed session on a real unauthorized response after startup', () => {
    const previous = registerAuthErrorHandler(() => {});
    try {
      installAuthIntegration();
      useAuthStore.setState({ checking: false, authRequired: true, authenticated: true });
      raiseAuthError();
      expect(useAuthStore.getState().authenticated).toBe(false);
    } finally {
      registerAuthErrorHandler(previous);
    }
  });
  it('does nothing before bootstrap reveals auth is required', () => {
    useAuthStore.setState({ authRequired: false, authenticated: true });
    useAuthStore.getState().handleAuthError();
    expect(useAuthStore.getState().authenticated).toBe(true);
  });

  it('flips authenticated off once auth is known to be required', () => {
    useAuthStore.setState({ authRequired: true, authenticated: true });
    useAuthStore.getState().handleAuthError();
    expect(useAuthStore.getState().authenticated).toBe(false);
  });

  it('is idempotent — second call is a no-op', () => {
    useAuthStore.setState({ authRequired: true, authenticated: false });
    useAuthStore.getState().handleAuthError();
    expect(useAuthStore.getState().authenticated).toBe(false);
  });
});
