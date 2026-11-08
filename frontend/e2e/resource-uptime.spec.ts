import { test, expect, MOCK_SESSION } from './fixtures';

test('shows backend uptime on Resources instead of the session sidebar', async ({ mockedPage: page }) => {
  let statsRequests = 0;
  await page.route('/api/system/stats', (route) => {
    statsRequests += 1;
    return route.fulfill({ json: { uptime: 3661 } });
  });
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await expect(page.getByTestId('session-sidebar')).toBeVisible();
  await expect(page.getByRole('button', { name: /system stats/i })).toHaveCount(0);
  await expect(page.getByTitle('Backend memory usage')).toHaveCount(0);
  expect(statsRequests).toBe(0);

  await page.getByRole('button', { name: 'Activity', exact: true }).click();
  await expect(page.getByText('Backend uptime: 1h 1m')).toBeVisible();
  await page.reload();
  await expect(page.getByText('Backend uptime: 1h 1m')).toBeVisible();
});
