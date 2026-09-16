import { createServer as createHttpServer, get, type ServerResponse } from 'node:http';
import { once } from 'node:events';
import type { AddressInfo } from 'node:net';
import { createServer, type ProxyOptions } from 'vite';
import { expect, it } from 'vitest';
import config from './vite.config';

it.each(['server', 'preview'] as const)('%s keeps the Tailscale upstream on loopback port 8228', (mode) => {
  expect(config[mode]).toMatchObject({ host: '127.0.0.1', port: 8228, strictPort: true });
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
