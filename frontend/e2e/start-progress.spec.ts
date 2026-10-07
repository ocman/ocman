import { test, expect, MOCK_SESSION } from './fixtures';

test('new-session launch checklist keeps its rows close together', async ({ mockedPage: page }) => {
  await page.addInitScript(() => {
    class MockEventSource extends EventTarget {
      static sources: MockEventSource[] = [];
      constructor() { super(); MockEventSource.sources.push(this); }
      close() {}
    }
    Object.assign(window, { EventSource: MockEventSource });
    Object.assign(window, {
      emitStartProgress: (startId: string) => {
        for (const source of MockEventSource.sources) {
          for (const [step, state] of [['opencode', 'done'], ['worktree', 'done'], ['session', 'done'], ['prompt', 'active']]) {
            source.dispatchEvent(new MessageEvent('ocman.session.start.progress', {
              data: JSON.stringify({ startId, step, state }),
            }));
          }
        }
      },
    });
  });
  await page.route('**/api/sessions/prepare', (route) => route.fulfill({ json: {
    platform: 'opencode', agents: [], commands: [], models: { hasProviders: true, models: [] }, liveConnection: true,
  } }));
  await page.route('**/api/sessions/resolve-targets', (route) => route.fulfill({ json: {
    candidates: [{ remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: MOCK_SESSION.directory }], remotes: [],
  } }));
  await page.route('**/api/git/info*', (route) => route.fulfill({ json: {} }));
  await page.route('**/api/worktree/list*', (route) => route.fulfill({ json: { worktrees: [] } }));
  await page.route('**/api/sessions/start', async (route) => {
    const { startId } = route.request().postDataJSON();
    await page.evaluate((id) => {
      (window as unknown as { emitStartProgress: (id: string) => void }).emitStartProgress(id);
    }, startId);
    // Hold creation pending so the real launch UI remains visible.
  });
  await page.goto(`/session/new?dir=${encodeURIComponent(MOCK_SESSION.directory)}&platform=opencode`);
  await page.getByRole('textbox').fill('Tighten the spacing in the session launch checklist.');
  await page.getByRole('textbox').press('Enter');
  const checklist = page.getByTestId('start-progress');
  await expect(checklist.getByRole('listitem')).toHaveCount(4);
  await expect(checklist).toHaveCSS('font-size', '10px');
  const mutedColor = await checklist.evaluate((element) => getComputedStyle(element).color);
  for (const icon of await checklist.locator('[aria-hidden]').all()) {
    await expect(icon).toHaveCSS('width', '10px');
    await expect(icon).toHaveCSS('height', '10px');
    await expect(icon).toHaveCSS('color', mutedColor);
  }
  await expect(checklist.getByTestId('start-step-prompt').locator('[aria-hidden]')).toHaveCSS('border-top-color', mutedColor);
  const rows = await checklist.getByRole('listitem').evaluateAll((items) => items.map((item) => {
    const rect = item.getBoundingClientRect();
    const label = item.lastElementChild!.getBoundingClientRect();
    return { top: rect.top, bottom: rect.bottom, height: rect.height, labelLeft: label.left };
  }));
  for (let i = 0; i < rows.length; i++) {
    expect(rows[i].height).toBeLessThanOrEqual(12.5);
    expect(rows[i].labelLeft).toBe(rows[0].labelLeft);
    if (i > 0) expect(rows[i].top - rows[i - 1].bottom).toBeLessThanOrEqual(1);
  }
  if (process.env.START_PROGRESS_SCREENSHOT) {
    await page.getByTestId('pending-prompt').evaluate((element) => Promise.all(
      element.getAnimations({ subtree: true }).map((animation) => animation.finished),
    ));
    await page.screenshot({ path: process.env.START_PROGRESS_SCREENSHOT });
  }
});
