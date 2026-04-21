import { afterEach, beforeEach, describe, it, expect, vi } from 'vitest';
import { AuthError, fetchJSON, postJSON, registerAuthErrorHandler } from './api';

// Each test installs its own fetch stub. We restore between tests so
// state doesn't leak.
function stubFetch(responder: (url: string, init?: RequestInit) => Response | Promise<Response>) {
  vi.stubGlobal('fetch', vi.fn((input: RequestInfo, init?: RequestInit) => {
    const url = typeof input === 'string' ? input : (input as Request).url;
    return Promise.resolve(responder(url, init));
  }));
}

beforeEach(() => {
  // Reset the handler to a no-op before every test.
  registerAuthErrorHandler(() => {});
});

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('fetchJSON', () => {
  it('returns parsed JSON on 200', async () => {
    stubFetch(() => new Response(JSON.stringify({ ok: true }), { status: 200 }));
    const result = await fetchJSON<{ ok: boolean }>('/api/thing');
    expect(result).toEqual({ ok: true });
  });

  it('throws AuthError on 401', async () => {
    stubFetch(() => new Response('unauthorized', { status: 401 }));
    await expect(fetchJSON('/api/thing')).rejects.toBeInstanceOf(AuthError);
  });

  it('throws plain Error on other non-2xx', async () => {
    stubFetch(() => new Response('boom', { status: 500 }));
    try {
      await fetchJSON('/api/thing');
      throw new Error('expected fetchJSON to throw');
    } catch (err) {
      expect(err).toBeInstanceOf(Error);
      expect(err).not.toBeInstanceOf(AuthError);
      expect((err as Error).message).toBe('boom');
    }
  });

  it('invokes the registered handler exactly once per 401', async () => {
    stubFetch(() => new Response('nope', { status: 401 }));
    const handler = vi.fn();
    registerAuthErrorHandler(handler);
    await expect(fetchJSON('/api/a')).rejects.toBeInstanceOf(AuthError);
    await expect(fetchJSON('/api/b')).rejects.toBeInstanceOf(AuthError);
    expect(handler).toHaveBeenCalledTimes(2);
    expect(handler.mock.calls[0][0]).toBeInstanceOf(AuthError);
  });
});

describe('postJSON', () => {
  it('serializes body and sends Content-Type', async () => {
    const captured: Array<{ url: string; init?: RequestInit }> = [];
    stubFetch((url, init) => {
      captured.push({ url, init });
      return new Response(JSON.stringify({ ok: true }), { status: 200 });
    });
    await postJSON('/api/thing', { hello: 'world' });
    expect(captured).toHaveLength(1);
    expect(captured[0].url).toBe('/api/thing');
    expect(captured[0].init?.method).toBe('POST');
    expect((captured[0].init?.headers as Record<string, string>)['Content-Type']).toBe('application/json');
    expect(captured[0].init?.body).toBe(JSON.stringify({ hello: 'world' }));
  });

  it('returns undefined on 204 No Content', async () => {
    stubFetch(() => new Response(null, { status: 204 }));
    const result = await postJSON('/api/logout', {});
    expect(result).toBeUndefined();
  });

  it('returns undefined when parseJSON is false even on 200', async () => {
    stubFetch(() => new Response('{"ignored":true}', { status: 200 }));
    const result = await postJSON('/api/logout', {}, { parseJSON: false });
    expect(result).toBeUndefined();
  });

  it('parses JSON on 200 by default', async () => {
    stubFetch(() => new Response(JSON.stringify({ token: 'xyz' }), { status: 200 }));
    const result = await postJSON<{ token: string }>('/api/login', { password: 'p' });
    expect(result).toEqual({ token: 'xyz' });
  });

  it('throws AuthError on 401', async () => {
    stubFetch(() => new Response('unauthorized', { status: 401 }));
    await expect(postJSON('/api/thing', {})).rejects.toBeInstanceOf(AuthError);
  });

  it('surfaces server body in Error message on non-2xx, non-401', async () => {
    stubFetch(() => new Response('bad request', { status: 400 }));
    try {
      await postJSON('/api/thing', {});
      throw new Error('expected throw');
    } catch (err) {
      expect(err).toBeInstanceOf(Error);
      expect(err).not.toBeInstanceOf(AuthError);
      expect((err as Error).message).toBe('bad request');
    }
  });
});

describe('registerAuthErrorHandler', () => {
  it('returns the previous handler so callers can restore', () => {
    const h1 = vi.fn();
    const h2 = vi.fn();
    const prev1 = registerAuthErrorHandler(h1);
    const prev2 = registerAuthErrorHandler(h2);
    expect(prev2).toBe(h1);
    // Restore.
    registerAuthErrorHandler(prev1);
  });
});
