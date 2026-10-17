import { test, expect, MOCK_SESSION, MOCK_SESSION_2 } from './fixtures';
import { installPhoneReviewRoutes } from './phone-review-fixtures';

test.describe('native phone shell', () => {
  test.use({ viewport: { width: 390, height: 844 } });

  test('keeps wrapped Factory navigation inside its own space', async ({ mockedPage: page }) => {
    await installPhoneReviewRoutes(page);
    for (const path of ['/factory/overview', '/factory/configuration', '/factory/how-to', '/factory/issues']) {
      await page.goto(path);
      const nav = page.getByRole('navigation', { name: 'Factory', exact: true });
      await expect(nav.getByRole('link', { name: 'Configuration', exact: true })).toBeVisible();
      const box = (await nav.boundingBox())!;
      for (const link of await nav.getByRole('link').all()) {
        const target = (await link.boundingBox())!;
        expect(target.x).toBeGreaterThanOrEqual(box.x);
        expect(target.x + target.width).toBeLessThanOrEqual(box.x + box.width + 1);
        expect(target.y + target.height).toBeLessThanOrEqual(box.y + box.height + 1);
        expect(target.height).toBeGreaterThanOrEqual(44);
      }
    }
  });

  test('keeps approval choices large and separated', async ({ mockedPage: page }) => {
    await page.route('**/api/inbox*', route => route.fulfill({ json: {
      items: [{ id: 'permission', remoteId: 'local', title: 'Run the tests?', body: 'Review the command.', readAt: 1, createdAt: Date.now(),
        permission: { platform: 'opencode', sessionId: MOCK_SESSION.id, permissionId: 'p1', permission: 'bash', patterns: ['go test ./...'], always: ['go test *'], metadata: { command: 'go test ./...' } } }], unreadTotal: 0,
    } }));
    await page.goto('/inbox');
    await page.getByRole('button', { name: /Run the tests/ }).click();
    const once = page.getByRole('button', { name: 'Allow once', exact: true });
    const always = page.getByRole('button', { name: 'Allow always', exact: true });
    await expect(once).toBeVisible();
    const a = await once.boundingBox();
    const b = await always.boundingBox();
    expect(a!.height).toBeGreaterThanOrEqual(44);
    expect(a!.width).toBeGreaterThanOrEqual(44);
    if (b!.y > a!.y) expect(b!.y - a!.y - a!.height).toBeGreaterThanOrEqual(8);
    else expect(b!.x - a!.x - a!.width).toBeGreaterThanOrEqual(8);
  });

  test('separates filter and row action targets for fingers', async ({ mockedPage: page }) => {
    await installPhoneReviewRoutes(page);
    await page.goto('/sessions');
    for (const group of ['Time range', 'Session status']) {
      const choices = page.getByRole('radiogroup', { name: group }).getByRole('radio');
      const a = await choices.nth(0).boundingBox();
      const b = await choices.nth(1).boundingBox();
      expect(a!.height).toBeGreaterThanOrEqual(44);
      expect(a!.width).toBeGreaterThanOrEqual(44);
      expect(b!.x - a!.x - a!.width).toBeGreaterThanOrEqual(8);
    }
    await page.goto('/inbox');
    const pin = page.getByRole('button', { name: 'Pin message', exact: true });
    const archive = page.getByRole('button', { name: 'Archive', exact: true });
    await expect(pin).toBeVisible();
    const pinBox = await pin.boundingBox();
    const archiveBox = await archive.boundingBox();
    expect(archiveBox!.y - pinBox!.y - pinBox!.height).toBeGreaterThanOrEqual(8);
    expect(pinBox!.width).toBeGreaterThanOrEqual(44);
  });

  test('stacks wide settings controls without squeezing their explanations', async ({ mockedPage: page }) => {
    await installPhoneReviewRoutes(page);
    await page.goto('/settings?tab=webhooks');
    const title = page.getByText('Webhook relay', { exact: true });
    const field = page.getByRole('textbox', { name: 'Webhook relay', exact: true });
    await expect(field).toBeVisible();
    const titleBox = await title.boundingBox();
    const fieldBox = await field.boundingBox();
    expect(titleBox!.width).toBeGreaterThanOrEqual(280);
    expect(fieldBox!.y).toBeGreaterThan(titleBox!.y + titleBox!.height);
  });

  test('uses the app\'s square edges for phone lists, controls and sheets', async ({ mockedPage: page }) => {
    for (const path of ['/sessions', '/projects']) {
      await page.goto(path);
      await expect(page.getByRole('link', { name: path === '/sessions' ? /Fix the login bug/ : /myapp/ }).first()).toBeVisible();
      await expect(page.getByRole('table')).toHaveCSS('border-top-left-radius', '0px');
      await expect(page.getByRole('searchbox')).toHaveCSS('border-top-left-radius', '0px');
      await expect(page.getByRole('button', { name: path === '/sessions' ? 'New session' : 'New project', exact: true })).toHaveCSS('border-top-left-radius', '0px');
    }
    await page.getByRole('button', { name: 'More', exact: true }).click();
    await expect(page.getByRole('dialog', { name: 'More destinations' })).toHaveCSS('border-top-left-radius', '0px');
  });

  test('uses tabs, a more sheet, and full-screen conversations', async ({ mockedPage: page }) => {
    await page.goto('/sessions');
    const nav = page.getByRole('navigation', { name: 'Phone navigation' });
    await expect(nav).toBeVisible();
    await expect(page.getByRole('navigation', { name: 'Main navigation' })).toBeHidden();
    await nav.getByRole('link', { name: 'Projects' }).click();
    await expect(page).toHaveURL('/projects');
    await nav.getByRole('button', { name: 'More' }).click();
    const sheet = page.getByRole('dialog', { name: 'More destinations' });
    await expect(sheet).toBeVisible();
    await sheet.getByRole('link', { name: 'Settings' }).click();
    await expect(sheet).toBeHidden();
    await expect(nav.getByRole('button', { name: 'More' })).toHaveClass(/active/);
    await nav.getByRole('link', { name: 'Sessions' }).click();
    await page.getByRole('link', { name: /Fix the login bug/ }).click();
    await expect(page.getByTestId('session-layout')).toBeVisible();
    await expect(nav).toHaveCount(0);
  });

  test('filters sessions only on phones and clears the effect on desktop', async ({ mockedPage: page }) => {
    await page.route('/api/sessions*', route => route.fulfill({ json: [
      { ...MOCK_SESSION, status: 'busy' },
      { ...MOCK_SESSION_2, status: 'waiting', pendingPermission: true },
    ] }));
    await page.goto('/sessions');
    const filters = page.getByRole('radiogroup', { name: 'Session status' });
    await filters.getByRole('radio', { name: 'Running', exact: true }).check();
    await expect(page.getByRole('link', { name: /Fix the login bug/ })).toBeVisible();
    await expect(page.getByRole('link', { name: /Refactor auth module/ })).toHaveCount(0);
    await filters.getByRole('radio', { name: 'Waiting', exact: true }).check();
    await expect(page.getByRole('link', { name: /Refactor auth module/ })).toBeVisible();
    await expect(page.getByRole('link', { name: /Fix the login bug/ })).toHaveCount(0);
    await page.setViewportSize({ width: 1024, height: 900 });
    await expect(filters).toBeHidden();
    await expect(page.getByRole('link', { name: /Fix the login bug/ })).toBeVisible();
    await expect(page.getByRole('link', { name: /Refactor auth module/ })).toBeVisible();
  });

  test('keeps the content above the tab bar and touch actions at least 44px', async ({ mockedPage: page }) => {
    await page.goto('/sessions');
    const nav = page.getByRole('navigation', { name: 'Phone navigation' });
    const content = page.getByTestId('page-content');
    const contentBox = await content.boundingBox();
    const navBox = await nav.boundingBox();
    expect(contentBox!.y + contentBox!.height).toBeLessThanOrEqual(navBox!.y + 1);
    const archive = page.getByRole('button', { name: 'Archive session', exact: true }).first();
    const box = await archive.boundingBox();
    expect(box!.height).toBeGreaterThanOrEqual(44);
    expect(box!.width).toBeGreaterThanOrEqual(44);
    await archive.click();
    await expect(page).toHaveURL('/sessions');
    expect(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth)).toBe(true);
  });
});

for (const width of [320, 390, 640, 641, 768, 1024, 1440]) {
  test(`session and project tables respect the phone boundary at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 900 });
    for (const path of ['/sessions', '/projects']) {
      await page.goto(path);
      const phone = width <= 640;
      const table = page.getByRole('table');
      await expect(table).toHaveCSS('display', phone ? 'block' : 'table');
      await expect(table.getByRole('row').last()).toHaveCSS('display', phone ? 'grid' : 'table-row');
      const heading = table.getByRole('columnheader', { name: path === '/sessions' ? 'Activity' : 'Messages', includeHidden: true });
      if (phone) await expect(heading).toBeHidden(); else await expect(heading).toBeVisible();
      const nav = page.getByRole('navigation', { name: 'Phone navigation' });
      if (phone) await expect(nav).toBeVisible(); else await expect(nav).toHaveCount(0);
    }
  });
}

test('phone inbox shows the message list before the reader', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 640, height: 900 });
  await page.route('**/api/inbox*', route => route.fulfill({ json: {
    items: [{ id: 'request', remoteId: 'local', title: 'Run tests', body: 'Review this request', createdAt: Date.now(), readAt: 0 }], unreadTotal: 1,
  } }));
  await page.goto('/inbox');
  await expect(page.getByRole('button', { name: /Run tests/ })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Inbox messages' })).toBeVisible();
  await expect(page.getByRole('region', { name: 'Message body' })).toBeHidden();
});
