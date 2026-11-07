import { test, expect, MOCK_SESSION } from './fixtures';

for (const width of [1280, 390]) {
  test(`compact updates match authoritative rendering without list refetches at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.addInitScript(() => {
      class Source extends EventTarget {
        constructor(url: string) {
          super();
          if (url === '/api/events') window.addEventListener('test-session-change', event => {
            this.dispatchEvent(new MessageEvent('ocman.session.changed', { data: JSON.stringify((event as CustomEvent).detail) }));
          });
        }
        close() {}
      }
      Object.assign(window, { EventSource: Source });
    });
    let rows = [{ ...MOCK_SESSION, title: 'Original session', seen: true }];
    let listReads = 0;
    await page.route(/\/api\/sessions\?/, route => {
      if (new URL(route.request().url()).searchParams.get('view') === 'running-count') return route.fulfill({ json: { count: 0 } });
      listReads++;
      return route.fulfill({ json: rows });
    });
    await page.route('**/build-hash.json', route => route.fulfill({ status: 404, body: '' }));
    await page.goto('/sessions');
    await expect(page.getByRole('link', { name: /Original session/ })).toBeVisible();
    const initialReads = listReads;
    rows = [{ ...rows[0], title: 'Updated session' }];
    await page.evaluate(row => window.dispatchEvent(new CustomEvent('test-session-change', { detail: {
      sessionID: row.id, platform: row.platform, revision: 1, session: row, patch: row,
    } })), rows[0]);
    await expect(page.getByRole('link', { name: /Updated session/ })).toBeVisible();
    expect(listReads).toBe(initialReads);
    const live = page.getByRole('link', { name: /Updated session/ });
    const pushed = await live.screenshot({ animations: 'disabled' });
    await page.reload();
    const canonical = page.getByRole('link', { name: /Updated session/ });
    await expect(canonical).toBeVisible();
    expect(await canonical.screenshot({ animations: 'disabled' })).toEqual(pushed);
  });
}
