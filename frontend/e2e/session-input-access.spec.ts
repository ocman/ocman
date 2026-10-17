import { test, expect, MOCK_SESSION } from './fixtures';

test.describe('narrow-screen mouse controls', () => {
  test.use({ viewport: { width: 390, height: 844 }, hasTouch: false });
  for (const view of ['page', 'drawer'] as const) {
    test(`${view} keeps archive and snooze clickable without touch gestures`, async ({ mockedPage: page }) => {
      const archives: unknown[] = [];
      const snoozes: unknown[] = [];
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
        ? page.getByRole('row').filter({ has: page.getByRole('link', { name: /Refactor auth module/ }) })
        : page.getByTestId('session-sidebar').getByRole('button', { name: /Refactor auth module/ });
      await row.hover();
      await row.getByRole('button', { name: 'Snooze conversation' }).click();
      await page.getByRole('menuitem', { name: 'In 1 hour', exact: true }).click();
      await expect.poll(() => snoozes.length).toBe(1);
      await row.getByRole('button', { name: 'Archive session' }).click();
      await expect.poll(() => archives.length).toBe(1);
      await expect(row).toHaveCount(0);
    });
  }
});

for (const width of [641, 768]) {
  test(`tablet session drawer has an explicit dismissal at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 900 });
    await page.goto(`/session/${MOCK_SESSION.id}`);
    await page.getByRole('button', { name: 'Open session list' }).click();
    await expect(page.getByTestId('session-sidebar')).toBeVisible();
    await page.getByRole('button', { name: 'Close session list' }).click();
    await expect(page.getByTestId('session-sidebar')).toBeHidden();
    await expect(page.getByTestId('conversation-composer')).toBeVisible();
    await expect(page).toHaveURL(`/session/${MOCK_SESSION.id}`);
  });
}
