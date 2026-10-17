import { test, expect, MOCK_SESSION, MOCK_SESSION_2 } from './fixtures';

for (const width of [390, 640, 641, 1280]) {
  test(`Home ${width <= 640 ? 'opens the session list' : 'restores the last session'} at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto(`/session/${MOCK_SESSION_2.id}`);
    await expect(page.getByTestId('session-layout')).toBeVisible();
    // The layout mounts before the session read and opened-session bookkeeping.
    await expect(page.getByTestId('header-breadcrumb')).toContainText(MOCK_SESSION_2.title);
    await page.goto('/');
    await expect(page).toHaveURL(width <= 640 ? '/sessions' : `/session/${MOCK_SESSION_2.id}`);
    if (width <= 640) {
      await expect(page.getByRole('searchbox', { name: 'Search sessions' })).toBeVisible();
      await expect(page.getByRole('link', { name: /Refactor auth module/ })).toBeVisible();
    } else {
      await expect(page.getByTestId('session-layout')).toBeVisible();
    }
  });
}

test('mobile Home keeps the session list as the landing page when there are no sessions', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route('/api/sessions*', route => route.fulfill({ json: [] }));
  await page.goto('/');
  await expect(page).toHaveURL('/sessions');
  await expect(page.getByText('No active sessions found', { exact: true })).toBeVisible();
});

test('mobile session deep links still open the requested conversation', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await expect(page).toHaveURL(`/session/${MOCK_SESSION.id}`);
  await expect(page.getByTestId('session-layout')).toBeVisible();
});

test('mobile Back returns from a conversation to the Home session list', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/');
  await expect(page).toHaveURL('/sessions');
  await page.getByRole('link', { name: /Fix the login bug/ }).click();
  await expect(page).toHaveURL(`/session/${MOCK_SESSION.id}`);
  await page.goBack();
  await expect(page).toHaveURL('/sessions');
  await expect(page.getByRole('searchbox', { name: 'Search sessions' })).toBeVisible();
});
