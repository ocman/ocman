import { test, expect, MOCK_SESSION } from './fixtures';

for (const width of [1280, 390]) {
  test(`entry and session read failures retry without creating a session at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    let listFailed = true;
    let sessionFailed = true;
    await page.route(/\/api\/sessions(?:\?.*)?$/, route => listFailed
      ? route.fulfill({ status: 400, contentType: 'text/plain', body: 'Could not load sessions. Restore the connection and retry.' })
      : route.fulfill({ json: [MOCK_SESSION] }));
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(?:\\?.*)?$`), route => sessionFailed
      ? route.fulfill({ status: 400, contentType: 'text/plain', body: 'Could not load this session from its owner.' })
      : route.fulfill({ json: { session: MOCK_SESSION, messages: [], parts: [], totalMessages: 0 } }));
    let creates = 0;
    await page.route(/\/api\/(?:session\/create|sessions\/start)(?:\?.*)?$/, route => { creates++; return route.fulfill({ status: 400 }); });
    await page.goto('/');
    const entryError = page.getByRole('alert').filter({ hasText: 'Could not load sessions' });
    await expect(entryError).toBeVisible();
    await expect(page).toHaveURL('/');
    listFailed = false;
    await entryError.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}$`));
    const sessionError = page.getByTestId('error-banner');
    await expect(sessionError.getByRole('alert')).toContainText('Could not load this session');
    await expect(sessionError.getByRole('button', { name: 'Retry', exact: true })).toHaveCSS('font-size', '12px');
    sessionFailed = false;
    await sessionError.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(sessionError).toBeHidden();
    await expect(page.getByRole('button', { name: 'Session actions' })).toBeVisible();
    await page.goto('/session/new');
    await expect(page.getByTestId('empty-detail')).toContainText('No session open.');
    expect(creates).toBe(0);
  });
}
