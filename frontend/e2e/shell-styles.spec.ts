import { test, expect, MOCK_SESSION } from './fixtures';
test.use({ factoryBeta: true });

for (const [platform, label] of [['opencode', 'OpenCode'], ['claudecode', 'Claude Code']]) {
  test(`${label} header icon is muted until hover and explains the platform`, async ({ mockedPage: page }) => {
    await page.route('/api/capabilities', route => route.fulfill({ json: {
      platforms: ['opencode', 'claudecode'].map(id => ({ id, displayName: id, available: true, capabilities: {} })),
      hosts: [], worktreeSessions: false,
    } }));
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: { ...MOCK_SESSION, platform }, sessionTree: [], messages: [], parts: [], totalMessages: 0,
    } }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const heading = page.getByRole('banner').getByRole('heading');
    const icon = heading.getByLabel(label, { exact: true });
    const image = icon.locator('img');
    await expect(page.getByTestId('header-breadcrumb')).toHaveText(`${MOCK_SESSION.title} ·`);
    await expect(image).toHaveAttribute('src', `/platform-${platform}.svg`);
    await expect(image).toHaveCSS('width', '16px');
    await expect(image).toHaveCSS('top', '0px');
    await expect(image).toHaveCSS('filter', 'grayscale(1)');
    await expect(image).toHaveCSS('opacity', '0.55');
    await icon.hover();
    await expect(image).toHaveCSS('filter', 'none');
    await expect(image).toHaveCSS('opacity', '1');
    await expect(page.getByRole('tooltip')).toHaveText(`This session runs on ${label}.`);
  });
}

test('desktop session title aligns with other page titles', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 1280, height: 720 });
  await page.goto('/sessions');
  const title = page.getByTestId('header-breadcrumb');
  await expect(title).toHaveText('Sessions');
  const pageTitleX = (await title.boundingBox())!.x;
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await expect(title).toContainText(MOCK_SESSION.title);
  expect((await title.boundingBox())!.x).toBeCloseTo(pageTitleX, 1);
});

test('shell keeps desktop, mobile and native navigation geometry', async ({ mockedPage: page }, testInfo) => {
  await page.goto('/sessions');
  const header = page.getByRole('banner');
  const sidebar = page.getByRole('complementary').filter({ has: page.getByRole('navigation', { name: 'Main navigation' }) });
  await expect(header).toHaveCSS('height', '44px');
  await expect(sidebar).toHaveCSS('width', '188px');
  await page.screenshot({ path: testInfo.outputPath('desktop-expanded.png') });
  await page.getByRole('button', { name: 'Collapse navigation', exact: true }).click();
  await expect(sidebar).toHaveCSS('width', '52px');
  await page.screenshot({ path: testInfo.outputPath('desktop-collapsed.png') });

  await page.setViewportSize({ width: 390, height: 844 });
  await expect(header).toHaveCSS('padding-left', '16px');
  await expect(sidebar).toBeHidden();
  const phoneNav = page.getByRole('navigation', { name: 'Phone navigation' });
  await expect(phoneNav).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('mobile-tabs.png') });
  await phoneNav.getByRole('link', { name: 'Projects', exact: true }).click();
  await expect(page).toHaveURL('/projects');
  await expect(sidebar).toBeHidden();

  await page.setViewportSize({ width: 1280, height: 720 });
  await page.evaluate(() => document.body.classList.add('wails-app'));
  await expect(header).toHaveCSS('padding-left', '16px');
  await expect(page.getByRole('button', { name: 'Expand navigation', exact: true })).toHaveCSS('height', '82px');
  await page.emulateMedia({ media: 'print' });
  await expect(header).toBeHidden();
  await expect(sidebar).toBeHidden();
});

for (const width of [1280, 390]) {
  test(`app header styles stay out of Factory drawer headers at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 844 });
    await page.route('/api/factory/epics', (route) => route.fulfill({ json: [] }));
    await page.route('/api/factory/formulas', (route) => route.fulfill({ json: [] }));
    await page.goto('/factory/epics');
    await page.getByRole('button', { name: 'New epic', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Create epic' });
    const heading = dialog.getByRole('heading', { name: 'Create epic' });
    await expect(heading).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath('factory-drawer.png') });
    // A drawer's semantic header must not acquire the shell's fixed height
    // or native-window drag region, including when its title wraps.
    const drawerHeader = page.getByRole('dialog', { name: 'Create epic' }).getByTestId('modal-header');
    await expect(drawerHeader).toHaveCSS('padding-bottom', '16px');
    expect((await drawerHeader.boundingBox())!.height).toBeGreaterThan((await heading.boundingBox())!.height + 16);
    await page.evaluate(() => document.body.classList.add('wails-app'));
    expect(await drawerHeader.evaluate((element) => getComputedStyle(element).getPropertyValue('--wails-draggable'))).toBe('');
  });
}
