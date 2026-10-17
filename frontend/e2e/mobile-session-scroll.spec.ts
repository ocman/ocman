import { test, expect, MOCK_SESSION } from './fixtures';

test.use({ viewport: { width: 390, height: 844 }, hasTouch: true });

for (const view of ['page', 'drawer'] as const) {
  test(`mobile session ${view} scrolls with a finger`, async ({ mockedPage: page, browserName }) => {
    test.skip(browserName !== 'chromium', 'Native touch gestures use Chromium CDP.');
    await page.route('/api/sessions*', route => route.fulfill({ json: Array.from({ length: 60 }, (_, index) => ({
      ...MOCK_SESSION,
      id: index === 0 ? MOCK_SESSION.id : `scroll-session-${index}`,
      title: `Scroll session ${index}`,
      timeUpdated: MOCK_SESSION.timeUpdated - index * 1000,
    })) }));
    await page.goto(view === 'page' ? '/sessions' : `/session/${MOCK_SESSION.id}`);
    if (view === 'drawer') {
      await page.getByRole('button', { name: 'Open session details' }).click();
      await page.getByRole('button', { name: 'Open session list', exact: true }).click();
    }
    const list = view === 'page' ? page.getByTestId('page-content') : page.getByTestId('session-sidebar');
    const first = view === 'page'
      ? list.getByRole('link', { name: /Scroll session 0\b/ })
      : list.getByRole('button', { name: /Scroll session 1\b/ });
    await expect(first).toBeVisible();
    const before = (await first.boundingBox())!.y;
    const box = (await list.boundingBox())!;
    const x = box.x + box.width / 2;
    const y = Math.min(box.y + box.height - 50, 700);
    const cdp = await page.context().newCDPSession(page);
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchStart', touchPoints: [{ x, y }] });
    for (let step = 1; step <= 10; step++) {
      await cdp.send('Input.dispatchTouchEvent', { type: 'touchMove', touchPoints: [{ x, y: y - step * 35 }] });
    }
    await cdp.send('Input.dispatchTouchEvent', { type: 'touchEnd', touchPoints: [] });
    await expect.poll(async () => (await first.boundingBox())!.y).toBeLessThan(before - 100);
    await cdp.detach();
  });
}
