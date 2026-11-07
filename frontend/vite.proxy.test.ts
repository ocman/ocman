import { createServer as createHttpServer, get, type ServerResponse } from 'node:http';
import { once } from 'node:events';
import type { AddressInfo } from 'node:net';
import { createServer, type ProxyOptions } from 'vite';
import { expect, it, vi } from 'vitest';
import config from './vite.config';

it.each(['server', 'preview'] as const)('%s reports an unavailable upstream instead of an empty success', (mode) => {
  const handlers = new Map<string, (...args: unknown[]) => void>();
  const proxy = { on: vi.fn((event, handler) => { handlers.set(event, handler); }) };
  const options = config[mode]!.proxy!['/api'] as ProxyOptions;
  options.configure!(proxy as never, options);
  const response = { headersSent: false, writableEnded: false, writeHead: vi.fn(), end: vi.fn(), destroy: vi.fn() };
  handlers.get('error')!(new Error('ECONNREFUSED'), {}, response);
  expect(response.writeHead).toHaveBeenCalledWith(503, expect.objectContaining({ 'Content-Type': 'text/plain' }));
  expect(response.end).toHaveBeenCalledWith('ocman backend unavailable\n');
});

it('destroys interrupted streams and WebSockets without rewriting sent headers', () => {
  const handlers = new Map<string, (...args: unknown[]) => void>();
  const proxy = { on: vi.fn((event, handler) => { handlers.set(event, handler); }) };
  const options = config.server!.proxy!['/api'] as ProxyOptions;
  options.configure!(proxy as never, options);
  const error = handlers.get('error')!;
  const streaming = { headersSent: true, writableEnded: false, writeHead: vi.fn(), destroy: vi.fn() };
  error(new Error('reset'), {}, streaming);
  expect(streaming.destroy).toHaveBeenCalledOnce();
  expect(streaming.writeHead).not.toHaveBeenCalled();
  const socket = { destroy: vi.fn() };
  error(new Error('reset'), {}, socket);
  expect(socket.destroy).toHaveBeenCalledOnce();
  const ended = { writableEnded: true, writeHead: vi.fn(), end: vi.fn() };
  error(new Error('reset'), {}, ended);
  expect(ended.end).not.toHaveBeenCalled();
});

it.each(['server', 'preview'] as const)('%s keeps the Tailscale upstream on loopback port 8228', (mode) => {
  expect(config[mode]).toMatchObject({ host: '127.0.0.1', port: 8228, strictPort: true });
});

it('returns 503 while the backend is down and recovers without restarting the proxy', async () => {
  const backend = createHttpServer((_req, res) => { res.end('[]'); });
  backend.listen(0, '127.0.0.1');
  await once(backend, 'listening');
  const port = (backend.address() as AddressInfo).port;
  await new Promise<void>((resolve) => backend.close(() => resolve()));
  const options = config.server!.proxy!['/api'] as ProxyOptions;
  const vite = await createServer({ configFile: false, logLevel: 'silent', server: {
    host: '127.0.0.1', port: 0, hmr: false,
    proxy: { '/api': { ...options, target: `http://127.0.0.1:${port}` } },
  } });
  try {
    await vite.listen();
    const url = `http://127.0.0.1:${(vite.httpServer!.address() as AddressInfo).port}/api/session/test/queue`;
    const unavailable = await fetch(url);
    expect(unavailable.status).toBe(503);
    expect(await unavailable.text()).toBe('ocman backend unavailable\n');
    backend.listen(port, '127.0.0.1');
    await once(backend, 'listening');
    const recovered = await fetch(url);
    expect(recovered.status).toBe(200);
    expect(await recovered.json()).toEqual([]);
  } finally {
    backend.closeAllConnections();
    await vite.close();
    if (backend.listening) await new Promise<void>((resolve) => backend.close(() => resolve()));
  }
});

it.each(['/api/events', '/api/session/test/events', '/mcp'])('disconnects %s when the backend restarts, then streams again', async (path) => {
  let upstream: ServerResponse;
  const backend = createHttpServer((_req, res) => {
    upstream = res;
    res.writeHead(200, { 'Content-Type': 'text/event-stream' });
    res.write('data: connected\n\n');
  });
  backend.listen(0, '127.0.0.1');
  await once(backend, 'listening');
  const target = `http://127.0.0.1:${(backend.address() as AddressInfo).port}`;
  const prefix = path.startsWith('/api') ? '/api' : '/mcp';
  const proxy = config.server!.proxy![prefix] as ProxyOptions;
  // Dev and preview must use the same recovery handler.
  expect((config.preview!.proxy![prefix] as ProxyOptions).configure).toBe(proxy.configure);
  const vite = await createServer({
    configFile: false,
    logLevel: 'silent',
    server: {
      host: '127.0.0.1', port: 0, hmr: false,
      proxy: { [prefix]: { ...proxy, target } },
    },
  });
  await vite.listen();
  const url = `http://127.0.0.1:${(vite.httpServer!.address() as AddressInfo).port}${path}`;
  const requests: ReturnType<typeof get>[] = [];
  const connect = async () => {
    const request = get(url);
    requests.push(request);
    const [response] = await once(request, 'response');
    // A reset is expected when the upstream process disappears.
    response.on('error', () => {});
    const [chunk] = await once(response, 'data');
    expect(chunk.toString()).toBe('data: connected\n\n');
    return response;
  };
  try {
    const response = await connect();
    let closed = false;
    response.on('close', () => { closed = true; });
    // Simulate Air killing a backend with an already-streaming response.
    upstream!.destroy();
    await expect.poll(() => closed, { timeout: 1000 }).toBe(true);
    const reconnected = await connect();
    reconnected.destroy();
  } finally {
    for (const request of requests) request.destroy();
    backend.closeAllConnections();
    await vite.close();
    await new Promise<void>((resolve) => backend.close(() => resolve()));
  }
});
