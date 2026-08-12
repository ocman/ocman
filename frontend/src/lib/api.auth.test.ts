import { afterEach, beforeEach, describe, it, expect, vi } from 'vitest';
import { api, AuthError, BackendUnavailableError, fetchJSON, postJSON, registerAuthErrorHandler } from './api';

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

describe('api.sendMessage', () => {
  it('passes platform as a query parameter to avoid remote session misrouting', async () => {
    const captured: string[] = [];
    stubFetch((url) => {
      captured.push(url);
      return new Response('', { status: 200 });
    });

    await api.sendMessage('s1', 'hello', undefined, undefined, undefined, undefined, 'r-box:opencode');

    expect(captured).toEqual(['/api/session/s1/message?platform=r-box%3Aopencode']);
  });
});

describe('backend-down error classification', () => {
  it('fetchJSON maps a network failure (TypeError) to BackendUnavailableError', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Load failed'))));
    const err = await fetchJSON('/api/thing').catch((e) => e);
    expect(err).toBeInstanceOf(BackendUnavailableError);
    expect((err as Error).message).toMatch(/backend is not responding/i);
  });

  it('fetchJSON maps a non-JSON 200 body (SyntaxError) to BackendUnavailableError', async () => {
    // Safari words this SyntaxError as "The string did not match the
    // expected pattern." — the bug this classification exists for.
    stubFetch(() => new Response('<!doctype html><html></html>', { status: 200 }));
    await expect(fetchJSON('/api/thing')).rejects.toBeInstanceOf(BackendUnavailableError);
  });

  it('postJSON maps a network failure to BackendUnavailableError', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Failed to fetch'))));
    await expect(postJSON('/api/thing', {})).rejects.toBeInstanceOf(BackendUnavailableError);
  });

  it('postJSON maps a non-JSON 200 body to BackendUnavailableError', async () => {
    stubFetch(() => new Response('not json', { status: 200 }));
    await expect(postJSON('/api/thing', {})).rejects.toBeInstanceOf(BackendUnavailableError);
  });

  it('does not remap aborts', async () => {
    const abortErr = new DOMException('aborted', 'AbortError');
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(abortErr)));
    const err = await fetchJSON('/api/thing').catch((e) => e);
    expect(err).toBe(abortErr);
  });

  it('keeps server error bodies intact on non-2xx', async () => {
    stubFetch(() => new Response('boom', { status: 500 }));
    const err = await fetchJSON('/api/thing').catch((e) => e);
    expect(err).not.toBeInstanceOf(BackendUnavailableError);
    expect((err as Error).message).toBe('boom');
  });

  it('api.createSession maps a network failure to BackendUnavailableError', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Load failed'))));
    await expect(api.createSession('/some/dir')).rejects.toBeInstanceOf(BackendUnavailableError);
  });

  it('api.sendMessage maps a network failure to BackendUnavailableError', async () => {
    vi.stubGlobal('fetch', vi.fn(() => Promise.reject(new TypeError('Load failed'))));
    await expect(api.sendMessage('s1', 'hi')).rejects.toBeInstanceOf(BackendUnavailableError);
  });
});

// Every endpoint that reads the raw Response instead of going through
// fetchJSON / postJSON must still route a 401 into the global auth
// fan-out, otherwise an expired cookie shows a generic error instead of
// the lockscreen (see authStore.ts).
describe('raw-response endpoints raise AuthError on 401', () => {
  const cases: Array<[string, () => Promise<unknown>]> = [
    ['createSession', () => api.createSession('/some/dir')],
    ['sendMessage', () => api.sendMessage('s1', 'hi')],
    ['queuedMessages', () => api.queuedMessages('s1')],
    ['deleteQueuedMessage', () => api.deleteQueuedMessage('s1', 'q1')],
    ['moveQueuedMessage', () => api.moveQueuedMessage('s1', 'q1', 1)],
    ['uploadComposerAttachment', () => api.uploadComposerAttachment('s1', new File(['x'], 'x.png'))],
    ['term.createWindow', () => api.term.createWindow('/dir')],
    ['term.killWindow', () => api.term.killWindow('/dir', 'w1')],
    ['transcribe', () => api.transcribe(new Blob(['x'], { type: 'audio/webm' }))],
    ['workflows.validate', () => api.workflows.validate('name: x')],
    ['workflows.publish', () => api.workflows.publish('name: x')],
  ];

  for (const [name, call] of cases) {
    it(`${name} throws AuthError and notifies the handler`, async () => {
      stubFetch(() => new Response('unauthorized', { status: 401 }));
      const handler = vi.fn();
      registerAuthErrorHandler(handler);
      await expect(call()).rejects.toBeInstanceOf(AuthError);
      expect(handler).toHaveBeenCalledTimes(1);
    });
  }

  it('keeps createSession 503 tagged as unreachable', async () => {
    stubFetch(() => new Response('no instance', { status: 503 }));
    const err = await api.createSession('/dir').catch((e) => e);
    expect(err).not.toBeInstanceOf(AuthError);
    expect((err as Error & { code?: string }).code).toBe('unreachable');
  });

  it('keeps sendMessage 409 tagged as busy', async () => {
    stubFetch(() => new Response('busy', { status: 409 }));
    const err = await api.sendMessage('s1', 'hi').catch((e) => e);
    expect(err).not.toBeInstanceOf(AuthError);
    expect((err as Error & { code?: string }).code).toBe('busy');
  });

  it('keeps deleteQueuedMessage tolerant of 404', async () => {
    stubFetch(() => new Response('gone', { status: 404 }));
    await expect(api.deleteQueuedMessage('s1', 'q1')).resolves.toBeUndefined();
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
