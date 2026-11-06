import { test, expect, MOCK_SESSION } from './fixtures';

for (const label of ['Home, needs attention', 'Inbox, unread messages']) {
  test(`collapsed desktop navigation preserves the ${label} indicator`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    await page.route('/api/sessions/notify*', route => route.fulfill({ json: [
      { ...MOCK_SESSION, status: 'waiting', seen: false },
    ] }));
    await page.route('/api/inbox*', route => route.fulfill({ json: {
      items: [{ id: 'attention', remoteId: 'local', title: 'Review ready', body: 'Please review.', createdAt: 1 }], unreadTotal: 0,
    } }));
    await page.goto('/sessions');
    const navigation = page.getByRole('navigation', { name: 'Main navigation' });
    const link = navigation.getByRole('link', { name: label, exact: true });
    const dot = link.getByTestId('nav-attention-dot');
    await expect(dot).toHaveCSS('opacity', '1');
    await page.getByRole('button', { name: 'Collapse navigation' }).click();
    await expect(page.getByRole('button', { name: 'Expand navigation' })).toBeVisible();
    await expect(page.getByRole('complementary')).toHaveCSS('width', '52px');
    await expect(dot).toHaveCSS('opacity', '1');
    const [outer, inner] = await Promise.all([link.boundingBox(), dot.boundingBox()]);
    expect(outer).not.toBeNull();
    expect(inner).not.toBeNull();
    expect(inner!.x).toBeGreaterThanOrEqual(outer!.x);
    expect(inner!.x + inner!.width).toBeLessThanOrEqual(outer!.x + outer!.width);
    await page.getByRole('button', { name: 'Expand navigation' }).click();
    await expect(dot).toHaveCSS('opacity', '1');
  });
}
