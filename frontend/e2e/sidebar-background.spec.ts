import { test, expect, MOCK_SESSION } from './fixtures';

test('sidebar background tasks spin slower than foreground work and respect reduced motion', async ({ mockedPage: page }) => {
  const rows = [
    { ...MOCK_SESSION, status: 'done', seen: true, backgroundTaskCount: 3, backgroundServiceCount: 1 },
    { ...MOCK_SESSION, id: 'foreground', title: 'Foreground work', status: 'busy' },
    { ...MOCK_SESSION, id: 'service', title: 'Service only', status: 'done', backgroundTaskCount: 1, backgroundServiceCount: 1 },
  ];
  await page.route('/api/sessions*', route => route.fulfill({ json: rows }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const sidebar = page.getByTestId('session-sidebar');
  const background = sidebar.getByLabel('Session status: Waiting for 2 background tasks');
  const foreground = sidebar.getByLabel('Session status: Busy');
  await expect(background).toBeVisible();
  await expect(background).toHaveCSS('animation-duration', '2.4s');
  await expect(foreground).toHaveCSS('animation-duration', '0.8s');
  await expect(sidebar.getByRole('button').filter({ hasText: 'Service only' }).getByLabel('Session status: Done')).toHaveCSS('animation-name', 'none');
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await expect(background).toHaveCSS('animation-name', 'none');
  await expect(foreground).toHaveCSS('animation-name', 'none');
});
