import { test, expect, MOCK_SESSION, MOCK_SESSION_2, type Page } from './fixtures';
import type { Locator } from '@playwright/test';

async function swipe(page: Page, row: Locator, distance: number) {
  const box = (await row.boundingBox())!;
  const x = box.x + box.width / 2;
  const y = box.y + box.height / 2;
  const cdp = await page.context().newCDPSession(page);
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
  for (let step = 1; step <= 10; step++) {
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x: x + distance * step / 10, y }] });
  }
  await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
  await cdp.detach();
}

test.describe('phone session actions', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: true });
  test('keeps Pin and Unpin touch-accessible without navigating away', async ({ mockedPage: page }) => {
    const pins: Record<string, unknown>[] = [];
    let pinned = false;
    await page.route('/api/sessions*', route => route.fulfill({ json: [MOCK_SESSION, { ...MOCK_SESSION_2, pinned }] }));
    await page.route('/api/session/pin', route => {
      const request = route.request().postDataJSON();
      pins.push(request);
      pinned = request.pinned;
      return route.fulfill({ json: { ok: true } });
    });
    await page.goto(`/session/${MOCK_SESSION.id}`);
    await page.getByRole('button', { name: 'Open session details' }).click();
    await page.getByRole('button', { name: 'Open session list' }).click();
    const sidebar = page.getByTestId('session-sidebar');
    const row = sidebar.getByRole('button', { name: /Refactor auth module/ });
    const pin = row.getByRole('button', { name: 'Pin session', exact: true });
    await expect(pin).toBeVisible();
    expect((await pin.boundingBox())!.height).toBeGreaterThanOrEqual(44);
    const url = page.url();
    await pin.tap();
    await expect.poll(() => pins.length).toBe(1);
    const unpin = sidebar.getByRole('button', { name: 'Unpin session', exact: true });
    await expect(unpin).toBeVisible();
    await unpin.tap();
    await expect.poll(() => pins.length).toBe(2);
    expect(pins.map(request => request.pinned)).toEqual([true, false]);
    await expect(page).toHaveURL(url);
  });
  for (const view of ['page', 'drawer'] as const) {
    test(`${view} swipes right to archive and left to choose a snooze duration`, async ({ mockedPage: page, browserName }) => {
      test.skip(browserName !== 'chromium', 'Native touch gestures use Chromium CDP.');
      const archives: unknown[] = [];
      const snoozes: Record<string, unknown>[] = [];
      await page.route('/api/session/archive', route => {
        archives.push(route.request().postDataJSON());
        return route.fulfill({ json: { ok: true } });
      });
      await page.route('/api/session/snooze', route => {
        snoozes.push(route.request().postDataJSON());
        return route.fulfill({ json: { ok: true } });
      });
      await page.goto(view === 'page' ? '/sessions' : `/session/${MOCK_SESSION.id}`);
      if (view === 'drawer') {
        await page.getByRole('button', { name: 'Open session details' }).click();
        await page.getByRole('button', { name: 'Open session list' }).click();
      }
      const row = view === 'page'
        ? page.getByRole('row').filter({ has: page.getByRole('link', { name: /Fix the login bug/ }) })
        : page.getByTestId('session-sidebar').getByRole('button', { name: /Refactor auth module/ });
      await expect(row).toBeVisible();
      await row.hover();
      expect(await row.getByRole('button', { name: 'Archive session' }).evaluate(element => getComputedStyle(element).pointerEvents)).toBe('none');
      const url = page.url();
      await swipe(page, row, -110);
      const choice = page.getByRole('menuitem', { name: 'In 1 hour', exact: true });
      await expect(choice).toBeVisible();
      expect(snoozes).toHaveLength(0);
      await choice.click();
      await expect.poll(() => snoozes.length).toBe(1);
      await expect(row).toHaveAttribute('aria-busy', 'false');
      expect(snoozes[0]).toMatchObject({ platform: 'opencode' });
      expect(Number(snoozes[0].until)).toBeGreaterThan(Date.now() + 59 * 60_000);
      await expect(page).toHaveURL(url);
      await swipe(page, row, 110);
      await expect.poll(() => archives.length).toBe(1);
      await expect(page).toHaveURL(url);
    });

    for (const failure of [false, true]) {
      test(`${view} hides an archive swipe before the server responds, failure=${failure}`, async ({ mockedPage: page, browserName }) => {
        test.skip(browserName !== 'chromium', 'Native touch gestures use Chromium CDP.');
        let release!: () => void;
        const responseReady = new Promise<void>(resolve => { release = resolve; });
        let requests = 0;
        await page.route('/api/session/archive', async route => {
          requests++;
          await responseReady;
          return route.fulfill({ status: failure ? 503 : 200, json: failure ? { error: 'Offline' } : { ok: true } });
        });
        await page.goto(view === 'page' ? '/sessions' : `/session/${MOCK_SESSION.id}`);
        if (view === 'drawer') {
          await page.getByRole('button', { name: 'Open session details' }).click();
          await page.getByRole('button', { name: 'Open session list' }).click();
        }
        const row = view === 'page'
          ? page.getByRole('row').filter({ has: page.getByRole('link', { name: /Fix the login bug/ }) })
          : page.getByTestId('session-sidebar').getByRole('button', { name: /Refactor auth module/ });
        await expect(row).toBeVisible();
        try {
          await swipe(page, row, 110);
          await expect(row).toHaveCount(0);
          await expect.poll(() => requests).toBe(1);
        } finally { release(); }
        if (failure) {
          await expect(row).toBeVisible();
          await expect(page.getByRole('alert').filter({ hasText: 'Could not archive session' })).toBeVisible();
        } else {
          await expect(row).toHaveCount(0);
        }
      });
    }
  }

  test('opens the terminal from details instead of the conversation footer', async ({ mockedPage: page }) => {
    await page.route('/api/tmux/sessions', route => route.fulfill({ json: { available: true, sessions: [] } }));
    let discoveries = 0;
    await page.route('/api/term/windows*', route => {
      discoveries++;
      return route.fulfill({ json: { windows: [{ name: 'ocman-test-1', title: 'Shell' }] } });
    });
    await page.goto(`/session/${MOCK_SESSION.id}`);
    await expect(page.getByTestId('conversation-composer')).toBeVisible();
    await expect(page.getByTestId('terminal-dock')).toHaveCount(0);
    expect(discoveries).toBe(0);
    await page.getByRole('button', { name: 'Open session details' }).click();
    await page.getByRole('button', { name: 'Open terminal', exact: true }).click();
    const terminal = page.getByRole('dialog', { name: 'Terminal', exact: true });
    await expect(terminal).toBeVisible();
    await expect(terminal.getByRole('tab', { name: 'Shell' })).toBeVisible();
    expect((await terminal.boundingBox())!.height).toBeGreaterThanOrEqual(800);
    await expect(terminal.getByRole('separator', { name: 'Resize terminal' })).toHaveCount(0);
    await page.goBack();
    await expect(terminal).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Open terminal', exact: true })).toBeVisible();
    await expect(page.getByTestId('terminal-dock')).toHaveCount(0);
  });

  test('hides the Escape hint while retaining the mobile Stop button', async ({ mockedPage: page }) => {
    await page.route(`/api/session/${MOCK_SESSION.id}?*`, route => route.fulfill({ json: {
      session: { ...MOCK_SESSION, status: 'busy', liveConnection: true }, messages: [], parts: [],
    } }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    await expect(page.getByText('Esc to interrupt', { exact: true })).toBeHidden();
    await expect(page.getByRole('button', { name: 'Stop generation (Esc)', exact: true })).toBeVisible();
    await page.setViewportSize({ width: 1024, height: 900 });
    await expect(page.getByText('Esc to interrupt', { exact: true })).toBeVisible();
  });
});

test('desktop retains session hover buttons and the terminal dock', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto('/sessions');
  const row = page.getByRole('row').filter({ has: page.getByRole('link', { name: /Fix the login bug/ }) });
  await row.hover();
  await expect(row.getByRole('button', { name: 'Archive session' })).toBeVisible();
  await page.route('/api/tmux/sessions', route => route.fulfill({ json: { available: true, sessions: [] } }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await expect(page.getByTestId('terminal-dock')).toBeVisible();
  const sidebarRow = page.getByTestId('session-sidebar').getByRole('button', { name: /Refactor auth module/ });
  await sidebarRow.hover();
  await expect(sidebarRow.getByRole('button', { name: 'Archive session' })).toBeVisible();
  await expect(sidebarRow.getByRole('button', { name: 'Snooze conversation' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Open terminal', exact: true })).toHaveCount(0);
});
