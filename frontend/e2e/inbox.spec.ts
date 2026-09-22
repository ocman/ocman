import { test, expect } from './fixtures';

test('Alt+I opens the inbox and is listed in keyboard shortcut help', async ({ mockedPage: page }) => {
  await page.route('**/api/inbox', (route) => route.fulfill({ json: { items: [], unreadTotal: 0 } }));
  await page.goto('/sessions');
  await page.getByRole('link', { name: 'Inbox', exact: true }).waitFor();
  await page.keyboard.press('Alt+i');
  await expect(page).toHaveURL('/inbox');
  await expect(page.getByText('Your inbox is empty.')).toBeVisible();
  await page.keyboard.press('Alt+/');
  await expect(page.getByText('Open inbox', { exact: true })).toBeVisible();
});

test('compact two-row header searches and archives messages from the actions dropdown', async ({ mockedPage: page }) => {
  let archived = false;
  await page.route('**/api/inbox**', (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === '/api/inbox/archive-all-read') {
      archived = true;
      return route.fulfill({ status: 204 });
    }
    const showArchived = url.searchParams.get('archived') === 'true';
    return route.fulfill({ json: { items: archived === showArchived ? [{ id: 'read', remoteId: 'local', title: 'Build finished', body: 'Review the changes.', createdAt: 1, readAt: 1, ...(archived ? { archivedAt: 2 } : {}) }] : [], unreadTotal: 0 } });
  });
  await page.goto('/inbox');
  await expect(page.getByRole('button', { name: /Build finished/ })).toBeVisible();
  // On desktop the first message opens on arrival. On narrow screens an open
  // message replaces the list (hiding the header measured below), so go back.
  await expect(page.getByRole('heading', { name: 'Build finished' })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('button', { name: 'Back to messages' }).click();
  for (const width of [1280, 390, 320]) {
    await page.setViewportSize({ width, height: 844 });
    const search = await page.getByRole('searchbox', { name: 'Search inbox' }).boundingBox();
    const status = await page.getByRole('group', { name: 'Message status' }).boundingBox();
    const types = await page.getByRole('group', { name: 'Message type' }).boundingBox();
    const actions = await page.getByLabel('Inbox actions').boundingBox();
    expect(search!.y).toBe(status!.y);
    expect(types!.y).toBe(actions!.y);
    expect(types!.y).toBeGreaterThan(search!.y);
    expect(types!.y - search!.y).toBeLessThanOrEqual(40);
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  }
  await page.getByRole('searchbox', { name: 'Search inbox' }).fill('bldfn');
  await expect(page.getByRole('button', { name: /Build finished/ })).toBeVisible();
  await page.getByRole('button', { name: 'Unread', exact: true }).click();
  await expect(page.getByText('No messages match these filters.')).toBeVisible();
  await page.getByRole('group', { name: 'Message status' }).getByRole('button', { name: 'All', exact: true }).click();
  await page.getByLabel('Inbox actions').click();
  await page.getByRole('button', { name: 'Archive all read', exact: true }).click();
  await expect(page.getByText('Your inbox is empty.')).toBeVisible();
  await page.getByRole('button', { name: 'Archived', exact: true }).click();
  await page.getByRole('button', { name: /Build finished/ }).click();
  await expect(page.getByRole('heading', { name: 'Build finished' })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Mark unread', exact: true })).toBeDisabled();
});

for (const unreadTotal of [1, 12, 120]) {
test(`unread count ${unreadTotal} is centered and visible in collapsed navigation`, async ({ mockedPage: page }) => {
  await page.route('**/api/inbox', (route) => route.fulfill({ json: { items: [], unreadTotal } }));
  await page.goto('/inbox');
  const link = page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: /^Inbox/ });
  const badge = link.getByText(unreadTotal > 99 ? '99+' : String(unreadTotal), { exact: true });
  await expect(badge).toBeVisible();
  await expect(badge).toHaveCSS('align-items', 'center');
  await expect(badge).toHaveCSS('justify-content', 'center');
  await page.getByRole('button', { name: 'Collapse navigation' }).click();
  await expect(page.getByRole('button', { name: 'Expand navigation' })).toBeVisible();
  await expect.poll(async () => (await link.boundingBox())?.width).toBeLessThan(40);
  await expect.poll(async () => {
    const outer = await link.boundingBox();
    const inner = await badge.boundingBox();
    return !!outer && !!inner && inner.x >= outer.x && inner.x + inner.width <= outer.x + outer.width;
  }).toBe(true);
  const box = await badge.boundingBox();
  expect(box).not.toBeNull();
  if (unreadTotal === 1) expect(box!.width).toBe(box!.height);
  else expect(box!.width).toBeGreaterThan(box!.height);
});
}

test('mark unread updates navigation and survives reloading the inbox', async ({ mockedPage: page }) => {
  let readAt: number | undefined = Date.now();
  await page.route('**/api/inbox**', async (route) => {
    const path = new URL(route.request().url()).pathname;
    if (path === '/api/inbox/unread') {
      expect(route.request().postDataJSON()).toEqual({ id: 'message-1', remoteId: 'local' });
      readAt = undefined;
      await route.fulfill({ status: 204 });
    } else if (path === '/api/inbox/open') {
      readAt = Date.now();
      await route.fulfill({ status: 204 });
    } else {
      await route.fulfill({ json: { items: [{ id: 'message-1', remoteId: 'local', title: 'Review ready', body: 'Please review the changes.', createdAt: 1, readAt }], unreadTotal: readAt ? 0 : 1 } });
    }
  });
  await page.goto('/inbox');
  await page.getByRole('button', { name: /Review ready/ }).click();
  await page.getByRole('button', { name: 'Mark unread' }).click();
  await expect(page.getByRole('heading', { name: 'Select a message' })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Inbox, 1 unread messages' })).toBeVisible();
  await page.reload();
  await expect(page.getByRole('button', { name: 'Unread', exact: true })).toBeVisible();
  await page.getByRole('button', { name: /Review ready/ }).click();
  await expect(page.getByRole('button', { name: 'Unread', exact: true })).toBeVisible();
  await expect(page.getByRole('link', { name: 'Inbox', exact: true })).toBeVisible();
});

test('message markdown preserves line breaks and has a full-width divider', async ({ mockedPage: page }) => {
  await page.route('**/api/inbox**', (route) => route.fulfill({ json: {
    items: [{ id: 'markdown', remoteId: 'local', title: 'Markdown message', createdAt: 1, readAt: 1,
      body: '**First line**\nSecond line\n\nThird paragraph\n\n- First item\n  Continued item\n- Second item\n\nHard break  \nAfter break\n\n```text\n  indented\n    code\n```' }], unreadTotal: 0,
  } }));
  await page.goto('/inbox');
  await page.getByRole('button', { name: /Markdown message/ }).click();
  const reader = page.getByRole('region', { name: 'Message body' });
  await expect(reader.getByText('First line', { exact: true })).toHaveCSS('font-weight', '700');
  const paragraph = reader.getByRole('paragraph').filter({ hasText: 'Second line' });
  const lineHeight = await paragraph.evaluate((element) => parseFloat(getComputedStyle(element).lineHeight));
  expect((await paragraph.boundingBox())!.height).toBeCloseTo(lineHeight * 2, 0);
  const hardBreak = reader.getByRole('paragraph').filter({ hasText: 'After break' });
  expect((await hardBreak.boundingBox())!.height).toBeCloseTo(lineHeight * 2, 0);
  await expect(reader.getByRole('listitem').first()).toContainText('Continued item');
  await expect(reader.getByText('indented\n    code', { exact: false })).toHaveCSS('white-space', 'pre');
  for (const width of [1280, 390]) {
    await page.setViewportSize({ width, height: 844 });
    const pane = await reader.boundingBox();
    const heading = await reader.getByTestId('inbox-message-header').boundingBox();
    expect(heading!.x).toBe(pane!.x);
    expect(heading!.width).toBe(pane!.width);
  }
});

for (const reply of ['once', 'always', 'reject'] as const) {
  test(`permission inbox supports ${reply} and archives the resolved request`, async ({ mockedPage: page }) => {
    let resolved = false;
    let received: unknown;
    const permission = { platform: 'r-laptop:opencode', sessionId: 'child-session', permissionId: 'perm-1', permission: 'bash', patterns: ['git status'], always: ['git *'], metadata: { command: 'git status' } };
    await page.route('**/api/inbox', (route) => route.fulfill({ json: {
      items: [
        ...(!resolved ? [{ id: 'permission', remoteId: 'laptop', category: 'permission', title: 'Deployment review: Permission requested: bash', body: 'Review this request.', permission, session: { platform: permission.platform, sessionId: permission.sessionId, title: 'Deployment review' }, createdAt: 2, readAt: 1 }] : []),
        { id: 'factory', remoteId: 'local', category: 'factory', title: 'Factory delivered', body: 'Ready for review.', createdAt: 1, readAt: 1 },
      ], unreadTotal: 0,
    } }));
    await page.route('**/api/session/child-session/permissions/perm-1?*', (route) => {
      expect(new URL(route.request().url()).searchParams.get('platform')).toBe('r-laptop:opencode');
      received = route.request().postDataJSON();
      resolved = true;
      return route.fulfill({ status: 204 });
    });
    await page.goto('/inbox?category=permission');
    await expect(page.getByRole('combobox')).toHaveCount(0);
    await expect(page.getByRole('button', { name: /Factory delivered/ })).toHaveCount(0);
    await page.getByRole('button', { name: 'Factory', exact: true }).click();
    await expect(page.getByRole('button', { name: /Factory delivered/ })).toBeVisible();
    await expect(page.getByRole('button', { name: /Permission requested: bash/ })).toHaveCount(0);
    const types = page.getByRole('group', { name: 'Message type' });
    await expect(types.getByRole('button', { pressed: true })).toHaveCount(1);
    await expect(types.getByRole('button', { name: 'Factory', exact: true })).toHaveText('Factory');
    await expect(types.getByRole('button', { name: 'Permissions', exact: true })).toHaveText('');
    await types.getByRole('button', { name: 'All', exact: true }).click();
    await page.getByRole('button', { name: /Permission requested: bash/ }).click();
    if (reply === 'reject') await page.setViewportSize({ width: 390, height: 844 });
    await expect(page.getByRole('region', { name: 'Permission actions' })).toBeVisible();
    await expect(page.getByRole('heading', { name: 'Deployment review: Permission requested: bash' })).toBeVisible();
    await expect(page.getByTestId('inbox-message-header').getByRole('link', { name: 'Deployment review' })).toHaveAttribute('href', '/session/child-session?platform=r-laptop%3Aopencode');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
    await page.waitForTimeout(400); // Existing permission controls reject in-flight keys for 350 ms.
    await page.getByRole('button', { name: { once: 'Allow once', always: 'Allow always', reject: 'Reject' }[reply], exact: true }).click();
    if (reply === 'always') {
      expect(received).toBeUndefined();
      await expect(page.getByText('git *', { exact: false })).toBeVisible();
      await page.getByRole('button', { name: 'Confirm', exact: true }).click();
    }
    await expect(page.getByRole('button', { name: /Permission requested: bash/ })).toHaveCount(0);
    await expect(page.getByRole('button', { name: /Factory delivered/ })).toBeVisible();
    expect(received).toEqual({ reply });
  });
}
