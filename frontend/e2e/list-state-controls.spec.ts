import { test, expect, MOCK_PROJECT, makeCapabilitiesWithPort } from './fixtures';

for (const width of [1280, 390]) {
  test(`inbox read failures retry into the empty state at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    let failed = true;
    await page.route(/\/api\/inbox(?:\?.*)?$/, route => failed
      ? route.fulfill({ status: 400, contentType: 'text/plain', body: 'Inbox read failed' })
      : route.fulfill({ json: { items: [], unreadTotal: 0 } }));
    await page.goto('/inbox');
    const alert = page.getByRole('alert').filter({ hasText: 'Could not load inbox.' });
    await expect(alert).toBeVisible();
    await expect(page.getByText('Your inbox is empty.', { exact: true })).toHaveCount(0);
    failed = false;
    await alert.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(alert).toBeHidden();
    await expect(page.getByText('Your inbox is empty.', { exact: true })).toBeVisible();
  });

  test(`worktree read retries retain the owner and use a narrow empty state at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    const capabilities = makeCapabilitiesWithPort();
    capabilities.hosts.push({ ...capabilities.hosts[0], remoteId: 'build', remoteName: 'Build machine' });
    await page.route('/api/capabilities', route => route.fulfill({ json: capabilities }));
    let failed = true;
    const owners: string[] = [];
    await page.route('/api/worktree/list?*', route => {
      owners.push(new URL(route.request().url()).searchParams.get('remoteId')!);
      return failed ? route.fulfill({ status: 400, contentType: 'text/plain', body: 'Could not read worktrees on Build machine.' }) : route.fulfill({ json: { worktrees: [] } });
    });
    await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}/worktrees?remoteId=build`);
    const alert = page.getByRole('alert').filter({ hasText: 'Could not read worktrees' });
    await expect(alert).toBeVisible();
    failed = false;
    await alert.getByRole('button', { name: 'Retry', exact: true }).click();
    const empty = page.getByText('No worktrees found', { exact: true });
    await expect(empty).toBeVisible();
    await expect(page.getByRole('table')).toHaveCount(0);
    const bounds = await empty.boundingBox();
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width);
    expect(owners.length).toBeGreaterThanOrEqual(2);
    expect(owners.every(owner => owner === 'build')).toBe(true);
    await page.getByRole('tab', { name: 'Sessions', exact: true }).click();
    await expect(page).toHaveURL(new RegExp(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}\\?remoteId=build$`));
  });
}
