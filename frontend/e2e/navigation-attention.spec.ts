import { test, expect, MOCK_SESSION } from './fixtures';

for (const label of ['Home, 1 unread sessions', 'Inbox, 1 unread messages']) {
  test(`collapsed desktop navigation preserves the ${label} badge`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.route('/api/sessions/notify*', route => route.fulfill({ json: [
      { ...MOCK_SESSION, status: 'waiting', seen: false },
    ] }));
    await page.route('/api/inbox*', route => route.fulfill({ json: {
      items: [{ id: 'attention', remoteId: 'local', title: 'Review ready', body: 'Please review.', createdAt: 1 }], unreadTotal: 0,
    } }));
    // Recent Home badges require the sidebar's current rows, not notify-only history.
    await page.goto('/');
    const navigation = page.getByRole('navigation', { name: 'Main navigation' });
    const link = navigation.getByRole('link', { name: label, exact: true });
    const badge = link.getByText('1', { exact: true });
    await expect(badge).toHaveCSS('opacity', '1');
    await navigation.getByRole('link', { name: 'Sessions', exact: true }).click();
    await expect(badge).toHaveCSS('opacity', '1');
    await page.getByRole('button', { name: 'Collapse navigation' }).click();
    await expect(page.getByRole('button', { name: 'Expand navigation' })).toBeVisible();
    await expect(page.getByRole('complementary').filter({ has: navigation })).toHaveCSS('width', '52px');
    await expect(badge).toHaveCSS('opacity', '1');
    const [outer, inner] = await Promise.all([link.boundingBox(), badge.boundingBox()]);
    expect(outer).not.toBeNull();
    expect(inner).not.toBeNull();
    expect(inner!.x).toBeGreaterThanOrEqual(outer!.x);
    expect(inner!.x + inner!.width).toBeLessThanOrEqual(outer!.x + outer!.width);
    await page.getByRole('button', { name: 'Expand navigation' }).click();
    await expect(badge).toHaveCSS('opacity', '1');
  });
}

test('recent Home has no badge for hidden history while current sessions are running or viewed', async ({ mockedPage: page }) => {
  const running = { ...MOCK_SESSION, id: 'running', title: 'Current work', status: 'busy' };
  const viewed = { ...MOCK_SESSION, id: 'viewed', title: 'Already viewed', seen: true };
  const factory = { ...MOCK_SESSION, id: 'factory', title: 'Old hidden Factory run', status: 'interrupted', factoryAttemptId: 'attempt' };
  await page.route(/\/api\/sessions(\?|$)/, route => route.fulfill({ json: [running, viewed, factory] }));
  await page.route('/api/sessions/notify*', route => route.fulfill({ json: [
    { ...running, status: 'waiting' }, { ...viewed, seen: false }, factory,
    { ...MOCK_SESSION, id: 'missing', title: 'Notification-only history' },
  ] }));
  await page.goto('/');
  await expect(page.getByText(running.title, { exact: true })).toBeVisible();
  await expect(page.getByText(viewed.title, { exact: true })).toBeVisible();
  const navigation = page.getByRole('navigation', { name: 'Main navigation' });
  await expect(navigation.getByRole('link', { name: 'Home', exact: true })).toHaveText('Home');
  await expect(page.getByText(factory.title, { exact: true })).not.toBeVisible();
  await expect(page.getByText('Notification-only history', { exact: true })).not.toBeVisible();
});
