import { readFileSync, writeFileSync } from 'node:fs';
import type { Route } from '@playwright/test';
import { test, expect, type Page } from './fixtures';

// The exact headers the Go server sends; internal/server pins them to this
// file, so these tests exercise the real response policy, not a copy.
const headers = JSON.parse(readFileSync(new URL('./artifact-html/headers.json', import.meta.url), 'utf8')) as { inert: string; interactive: string };
const fixture = (name: string) => readFileSync(new URL(`./artifact-html/${name}`, import.meta.url), 'utf8');
const bodies = [fixture('interactive.html'), fixture('hostile.html'), fixture('selfnav.html')];

const artifact = {
  id: 'art-h', title: 'Design board', directory: '/home/user/projects/myapp', remoteId: 'local', createdAt: '2026-10-01T10:00:00Z',
  items: [
    { kind: 'file', name: 'board.html', mime: 'text/html; charset=utf-8', size: bodies[0].length, url: '/api/artifacts/art-h/files/0' },
    { kind: 'file', name: 'hostile.html', mime: 'text/html; charset=utf-8', size: bodies[1].length, url: '/api/artifacts/art-h/files/1' },
    { kind: 'file', name: 'selfnav.html', mime: 'text/html; charset=utf-8', size: bodies[2].length, url: '/api/artifacts/art-h/files/2' },
  ],
};

/** Serves the artifact like handlers_artifacts.go: inert by default, the preview policy only under /interactive. */
async function serveArtifact(page: Page) {
  await page.route('/api/artifacts/art-h', (route) => route.fulfill({ json: artifact }));
  await page.route(/\/api\/artifacts\/art-h\/files\/(\d)(\/interactive)?(\?.*)?$/, (route) => {
    const url = new URL(route.request().url());
    const [, n, interactive] = /files\/(\d)(\/interactive)?/.exec(url.pathname)!;
    const download = !interactive && url.searchParams.get('download') === '1';
    return route.fulfill({
      body: bodies[Number(n)],
      headers: {
        'Content-Type': 'text/html; charset=utf-8',
        'Content-Security-Policy': interactive ? headers.interactive : headers.inert,
        'X-Frame-Options': 'SAMEORIGIN',
        'X-Content-Type-Options': 'nosniff',
        'Referrer-Policy': 'no-referrer',
        'Content-Disposition': `${download ? 'attachment' : 'inline'}; filename=${artifact.items[Number(n)].name}`,
      },
    });
  });
}

/**
 * Records every request an artifact could use to escape or exfiltrate that
 * actually reached the network layer (CSP-blocked requests never do), plus
 * any popup. checkWatcher proves the recorder is live, so an empty list means
 * something.
 */
async function watchEscapes(page: Page) {
  const escapes: string[] = [];
  const record = (route: Route) => { escapes.push(route.request().url()); return route.fulfill({ body: 'reached' }); };
  await page.route(/\/beacon\/|probe=hostile/, record);
  await page.context().route('http://evil.test/**', record);
  page.context().on('page', (p) => escapes.push(`popup:${p.url()}`));
  page.on('download', (d) => escapes.push(`download:${d.suggestedFilename()}`));
  const checkWatcher = async (from: Page) => {
    await from.evaluate(() => fetch('/beacon/control'));
    expect(escapes.pop()).toMatch(/\/beacon\/control$/);
  };
  return { escapes, checkWatcher };
}

const fileCard = (page: Page, name: string) => page.getByTestId('artifact-file').filter({ has: page.getByText(name, { exact: true }) });
const frameOf = (page: Page, name: string) => fileCard(page, name).frameLocator('[data-testid="artifact-preview-html"]');

// Keep the PWA service worker from answering artifact requests the routes must serve.
test.use({ serviceWorkers: 'block' });

test.beforeEach(async ({ mockedPage: page, baseURL }) => {
  await page.context().addCookies([{ name: 'ocman_secret', value: 'cookie-value', url: baseURL! }]);
  await serveArtifact(page);
});

test('static preview shows pre-rendered HTML and CSS but runs no script', async ({ mockedPage: page }) => {
  await page.goto('/artifacts/art-h');
  const card = fileCard(page, 'board.html');
  await expect(card.getByTestId('artifact-preview-html')).toHaveAttribute('sandbox', '');
  await expect(card.getByText(/^Scripts are disabled in this preview\. Running them keeps the page away from ocman/)).toBeVisible();
  const heading = frameOf(page, 'board.html').getByTestId('screen');
  await expect(heading).toHaveText('Find work (pre-rendered)');
  await expect(heading).toHaveCSS('color', 'rgb(10, 120, 30)');
  await expect(frameOf(page, 'board.html').getByTestId('js-only')).toHaveCount(0);
  await expect(frameOf(page, 'hostile.html').getByTestId('results')).toHaveText('pending');

  // The response CSP alone keeps scripts off: widening the frame's sandbox does not run them.
  await card.getByTestId('artifact-preview-html').evaluate((f: HTMLIFrameElement) => { f.sandbox.value = 'allow-scripts'; f.src += '?page=job'; });
  await expect.poll(async () => {
    const reloaded = page.frames().find((f) => f.url().endsWith('?page=job'));
    return reloaded ? reloaded.getByTestId('screen').textContent() : 'not loaded';
  }).toBe('Find work (pre-rendered)');
  await expect(frameOf(page, 'board.html').getByTestId('js-only')).toHaveCount(0);
});

test('opting in runs the inline script and the page selector changes the screen', async ({ mockedPage: page }) => {
  await page.goto('/artifacts/art-h');
  const card = fileCard(page, 'board.html');
  await card.getByRole('button', { name: 'Run scripts' }).click();
  await expect(card.getByTestId('artifact-preview-html')).toHaveAttribute('sandbox', 'allow-scripts');
  await expect(card.getByTestId('artifact-preview-html')).toHaveAttribute('src', '/api/artifacts/art-h/files/0/interactive');
  const frame = frameOf(page, 'board.html');
  await expect(frame.getByTestId('screen')).toHaveText('Find work (rendered by script)');
  await expect(frame.getByTestId('screen')).toHaveCSS('color', 'rgb(10, 120, 30)');
  await expect(frame.getByTestId('js-only')).toHaveText('Built by JavaScript');
  await frame.getByRole('combobox').selectOption('job');
  await expect(frame.getByTestId('screen')).toHaveText('Job details (rendered by script)');
  await expect(page).toHaveURL(/\/artifacts\/art-h$/);

  // The opt-in is remembered for this file only, until stopped.
  await page.reload();
  await expect(frameOf(page, 'board.html').getByTestId('screen')).toHaveText('Find work (rendered by script)');
  await expect(fileCard(page, 'hostile.html').getByTestId('artifact-preview-html')).toHaveAttribute('sandbox', '');
  await card.getByRole('button', { name: 'Stop scripts' }).click();
  await expect(card.getByTestId('artifact-preview-html')).toHaveAttribute('sandbox', '');
  await expect(frameOf(page, 'board.html').getByTestId('screen')).toHaveText('Find work (pre-rendered)');
  await page.reload();
  await expect(card.getByRole('button', { name: 'Run scripts' })).toBeVisible();
});

test('an interactive hostile artifact cannot reach the app, storage, network, top window or forms', async ({ mockedPage: page }) => {
  const { escapes, checkWatcher } = await watchEscapes(page);
  await page.goto('/artifacts/art-h');
  await fileCard(page, 'hostile.html').getByRole('button', { name: 'Run scripts' }).click();
  const results = frameOf(page, 'hostile.html').getByTestId('results');
  await expect(results).not.toHaveText('pending');
  const r = JSON.parse(await results.textContent() ?? '{}') as Record<string, string>;
  expect(r.origin).toBe('ok:null');
  for (const key of ['parentDocument', 'topDocument', 'frameElement', 'cookie', 'localStorage', 'sessionStorage', 'indexedDB',
    'fetchSameOrigin', 'fetchExternal', 'webSocket', 'popup']) {
    expect(r[key], key).toMatch(/^blocked:/);
  }
  // Chromium reports img-src/frame-src violations asynchronously; the network check below is the ground truth.
  expect(r.violations).toContain('connect-src');
  expect(r.socketBlockedByPolicy).toBe('true');
  await page.waitForTimeout(1000);
  expect(escapes).toEqual([]);
  await checkWatcher(page);
  await expect(page).toHaveURL(/\/artifacts\/art-h$/);
  // The form submission did not navigate the frame away from the report.
  await expect(results).not.toHaveText('pending');
  expect(await page.evaluate(() => document.cookie)).toContain('ocman_secret=cookie-value');
});

test('the known limit: a running page can still navigate its own frame to another site', async ({ mockedPage: page }) => {
  const { escapes } = await watchEscapes(page);
  await page.goto('/artifacts/art-h');
  const card = fileCard(page, 'selfnav.html');
  await card.getByRole('button', { name: 'Run scripts' }).click();
  await expect(card.getByText(/can still navigate itself to another site/)).toBeVisible();
  await frameOf(page, 'selfnav.html').getByRole('button', { name: 'Navigate away' }).click();
  await expect.poll(() => escapes).toEqual(['http://evil.test/exfil?d=secret-title']);
  await expect(page).toHaveURL(/\/artifacts\/art-h$/);
});

test('direct file URLs keep their policy at the top level', async ({ mockedPage: page }) => {
  const { escapes, checkWatcher } = await watchEscapes(page);
  await page.goto('/api/artifacts/art-h/files/0');
  await expect(page.getByTestId('screen')).toHaveText('Find work (pre-rendered)');
  await page.goto('/api/artifacts/art-h/files/1');
  await expect(page.getByTestId('results')).toHaveText('pending');

  await page.goto('/api/artifacts/art-h/files/1/interactive');
  await expect(page.getByTestId('results')).not.toHaveText('pending');
  const r = JSON.parse(await page.getByTestId('results').textContent() ?? '{}') as Record<string, string>;
  expect(r.origin).toBe('ok:null');
  for (const key of ['cookie', 'localStorage', 'sessionStorage', 'fetchSameOrigin', 'fetchExternal', 'popup']) expect(r[key], key).toMatch(/^blocked:/);
  await page.waitForTimeout(500);
  expect(escapes).toEqual([]);
  await page.goto('/artifacts');
  await checkWatcher(page);
});

test('the download link saves the original bytes, which run interactively when opened locally', async ({ mockedPage: page }) => {
  await page.goto('/artifacts/art-h');
  await expect(fileCard(page, 'board.html').getByRole('link', { name: 'Download board.html' }))
    .toHaveAttribute('href', '/api/artifacts/art-h/files/0?download=1');
  // Playwright cancels downloads it fulfills itself, so open the bytes the
  // server sends (pinned by TestArtifactInteractivePreview) as a saved file:
  // response headers, including the sandbox CSP, never travel with it.
  const path = test.info().outputPath('board.html');
  writeFileSync(path, bodies[0]);
  const local = await page.context().newPage();
  await local.goto(`file://${path}`);
  await expect(local.getByTestId('screen')).toHaveText('Find work (rendered by script)');
  await local.getByRole('combobox').selectOption('job');
  await expect(local.getByTestId('screen')).toHaveText('Job details (rendered by script)');
});
