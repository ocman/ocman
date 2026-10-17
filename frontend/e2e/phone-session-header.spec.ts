import { test, expect, MOCK_SESSION } from './fixtures';

for (const width of [320, 390, 640]) {
  test(`session header is one compact row at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const header = page.getByRole('banner');
    await expect(header.getByRole('heading', { level: 1 })).toContainText(MOCK_SESSION.title);
    await expect(header.getByRole('link', { name: 'Back to sessions' })).toBeVisible();
    await expect(header.getByRole('button', { name: 'Open session details' })).toBeVisible();
    await expect(header.getByRole('button', { name: 'Explore files' })).toBeHidden();
    await expect(header.getByRole('button', { name: 'Open session list' })).toHaveCount(0);
    expect((await header.boundingBox())!.height).toBe(56);
    expect(await header.getByRole('button').filter({ visible: true }).count()).toBe(1);
    await header.getByRole('button', { name: 'Open session details' }).click();
    await expect(page.getByRole('button', { name: 'Close session details' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Explore project files' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Session actions' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Open session list' })).toBeVisible();
    await page.getByRole('button', { name: 'Open session list' }).click();
    await expect(page.getByTestId('session-layout')).toHaveClass(/mobile-sidebar-open/);
  });
}

test('desktop retains project and file controls in the header', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1280, height: 900 });
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const header = page.getByRole('banner');
  await expect(header.getByRole('button', { name: 'Explore files' })).toBeVisible();
  await expect(header.getByRole('button', { name: 'Project quick settings' })).toBeVisible();
  await expect(header.getByRole('button', { name: 'Session actions' })).toBeVisible();
  await expect(page.getByRole('group', { name: 'Session tools' })).toHaveCount(0);
});
