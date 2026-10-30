import { readFileSync } from 'node:fs';
import { createServer, type Server } from 'node:http';
import { createHash } from 'node:crypto';
import type { AddressInfo, Socket } from 'node:net';
import { test, expect, MOCK_SESSION, type Page } from './fixtures';

// Go tests pin these headers to the production handlers.
const headers = JSON.parse(readFileSync(new URL('./browser-security/headers.json', import.meta.url), 'utf8')) as { app: string; externalImage: string };
const artifactHeaders = JSON.parse(readFileSync(new URL('./artifact-html/headers.json', import.meta.url), 'utf8')) as { inert: string };
const imageHTML = readFileSync(new URL('../../internal/previews/external_image.html', import.meta.url), 'utf8');
const externalURL = 'http://external-image.invalid/report.svg';

async function appPolicy(page: Page) {
  await page.route(/\/session\/[^/?]+(?:\?.*)?$/, async route => {
    if (route.request().resourceType() !== 'document') return route.fallback();
    const response = await route.fetch();
    await route.fulfill({ response, headers: { ...response.headers(),
      'Content-Security-Policy': headers.app.replaceAll('{{host}}', new URL(route.request().url()).host),
    } });
  });
}

test('external Markdown images require consent and load only in an opaque script-free frame', async ({ mockedPage: page }) => {
  const contacts: { url: string; referer?: string }[] = [];
  await appPolicy(page);
  await page.route(externalURL, route => {
    contacts.push({ url: route.request().url(), referer: route.request().headers().referer });
    return route.fulfill({ contentType: 'image/svg+xml', body: '<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20"><rect width="20" height="20" fill="blue"/></svg>' });
  });
  await page.route('/api/previews/external-image?*', route => {
    const url = new URL(route.request().url()).searchParams.get('url')!;
    return route.fulfill({ body: imageHTML.replace('{{.}}', url), headers: {
      'Content-Type': 'text/html; charset=utf-8',
      'Content-Security-Policy': headers.externalImage.replace('{{origin}}', new URL(url).origin),
      'Referrer-Policy': 'no-referrer', 'X-Frame-Options': 'SAMEORIGIN',
    } });
  });
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
    session: { ...MOCK_SESSION, status: 'done' },
    messages: [{ id: 'a', sessionId: MOCK_SESSION.id, timeCreated: 2000, data: { role: 'assistant', finish: 'stop', time: { created: 2000, completed: 2001 } } }],
    parts: [{ id: 'p', messageId: 'a', sessionId: MOCK_SESSION.id, data: { type: 'text', text: `![Report](${externalURL})\n\n![Local](/favicon.svg)` } }], totalMessages: 1,
  } }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await expect(page.getByRole('button', { name: 'Load external image from external-image.invalid' })).toBeVisible();
  await expect(page.getByRole('img', { name: 'Local', exact: true })).toBeVisible();
  expect(contacts).toEqual([]);
  await expect(page.getByTitle('External image: Report')).toHaveCount(0);
  await page.getByRole('button', { name: 'Load external image from external-image.invalid' }).click();
  const iframe = page.getByTitle('External image: Report');
  await expect(iframe).toHaveAttribute('sandbox', '');
  await expect(iframe).toHaveAttribute('referrerpolicy', 'no-referrer');
  const frame = page.frameLocator('iframe[title="External image: Report"]');
  await expect.poll(() => frame.getByRole('img', { name: 'External image' }).evaluate((image: HTMLImageElement) => image.complete && image.naturalWidth > 0)).toBe(true);
  expect(contacts).toEqual([{ url: externalURL, referer: undefined }]);
  const isolation = await frame.locator('body').evaluate(() => {
    let parentBlocked = false, storageBlocked = false;
    try { void parent.document.body; } catch { parentBlocked = true; }
    try { void localStorage.length; } catch { storageBlocked = true; }
    return { origin: self.origin, parentBlocked, storageBlocked, scripts: document.scripts.length };
  });
  expect(isolation).toEqual({ origin: 'null', parentBlocked: true, storageBlocked: true, scripts: 0 });
  await page.getByRole('button', { name: 'Stop loading external image' }).click();
  await expect(iframe).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole('button', { name: 'Load external image from external-image.invalid' })).toBeVisible();
  expect(contacts).toHaveLength(1);
});

async function localServer(policy: boolean) {
  const stats = { requests: 0, upgrades: 0 };
  const sockets = new Set<Socket>();
  const server = createServer((request, response) => {
    stats.requests++;
    response.setHeader('Access-Control-Allow-Origin', '*');
    if (policy) response.setHeader('Content-Security-Policy', headers.app.replaceAll('{{host}}', request.headers.host!));
    response.setHeader('Content-Type', policy ? 'text/html' : 'image/svg+xml');
    response.end(policy ? '<!doctype html><title>Policy test</title><p>Ready</p>' : '<svg xmlns="http://www.w3.org/2000/svg" width="1" height="1"/>');
  });
  server.on('connection', socket => { sockets.add(socket); socket.on('close', () => sockets.delete(socket)); });
  server.on('upgrade', (request, socket) => {
    stats.upgrades++;
    const accept = createHash('sha1').update(request.headers['sec-websocket-key'] + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').digest('base64');
    socket.write(`HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: ${accept}\r\n\r\n`);
    socket.end(Buffer.from([0x81, 2, 0x6f, 0x6b])); // a real native WebSocket text frame: "ok"
  });
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve));
  return { server, sockets, stats, url: `http://127.0.0.1:${(server.address() as AddressInfo).port}` };
}

async function close(server: Server, sockets: Set<Socket>) {
  for (const socket of sockets) socket.destroy();
  await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve()));
}

test('main policy blocks foreign images, fetches and sockets while allowing the owning WebSocket', async ({ mockedPage: page }) => {
  const app = await localServer(true);
  const foreign = await localServer(false);
  try {
    await fetch(foreign.url);
    expect(foreign.stats.requests).toBe(1);
    foreign.stats.requests = 0;
    await page.goto(app.url);
    const result = await page.evaluate(async ({ app, foreign }) => {
      const image = await new Promise<string>(resolve => {
        const image = new Image();
        image.onload = () => resolve('loaded'); image.onerror = () => resolve('blocked');
        image.src = `${foreign}/image.svg`;
      });
      const network = await fetch(`${foreign}/fetch`).then(() => 'loaded', () => 'blocked');
      const socket = (url: string) => new Promise<string>(resolve => {
        try {
          const ws = new WebSocket(url.replace('http:', 'ws:') + '/api/term/ws');
          ws.onmessage = event => { resolve(String(event.data)); ws.close(); };
          ws.onerror = () => resolve('blocked');
        } catch { resolve('blocked'); }
      });
      return { image, network, localSocket: await socket(app), foreignSocket: await socket(foreign) };
    }, { app: app.url, foreign: foreign.url });
    expect(result).toEqual({ image: 'blocked', network: 'blocked', localSocket: 'ok', foreignSocket: 'blocked' });
    expect(app.stats.upgrades).toBe(1);
    expect(foreign.stats).toEqual({ requests: 0, upgrades: 0 });
  } finally {
    await close(app.server, app.sockets);
    await close(foreign.server, foreign.sockets);
  }
});

test('static HTML previews cannot load external subresources even without scripts', async ({ mockedPage: page }) => {
  const outbound: string[] = [];
  await page.route('http://external-image.invalid/**', route => { outbound.push(route.request().url()); return route.fulfill({ body: '' }); });
  await page.route('**/static-security-preview', route => route.fulfill({ headers: {
    'Content-Type': 'text/html', 'Content-Security-Policy': artifactHeaders.inert,
  }, body: `<style>@import url('${externalURL}/style');body{background-image:url('${externalURL}/background')}</style><img src="${externalURL}/pixel"><iframe src="${externalURL}/frame"></iframe><p>Static preview</p>` }));
  await page.goto('/static-security-preview');
  await expect(page.getByText('Static preview')).toBeVisible();
  await expect.poll(() => page.locator('img').evaluate((image: HTMLImageElement) => image.complete)).toBe(true);
  expect(await page.locator('img').evaluate((image: HTMLImageElement) => image.naturalWidth)).toBe(0);
  expect(outbound).toEqual([]);
});
