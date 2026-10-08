import { test, expect } from './fixtures';

for (const width of [1280, 390]) {
  test(`Remotes table scrolls inside Settings at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto('/settings');
    if (width === 390) await page.getByRole('combobox', { name: 'Settings group', exact: true }).selectOption('remotes');
    else await page.getByRole('button', { name: 'Remotes', exact: true }).click();
    const settings = page.getByTestId('remote-settings');
    await expect(settings).toBeVisible();
    const bounds = await settings.boundingBox();
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width);
    expect(bounds!.width).toBeLessThanOrEqual(640);
    const frame = page.getByRole('table', { name: 'Machines' }).locator('..');
    expect(await frame.evaluate((element) => element.scrollWidth > element.clientWidth)).toBe(true);
    expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(width);
  });
  test(`settings group navigation and search share the same selection at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.route('/api/settings/prompt-sections', route => route.fulfill({ json: [{ title: 'Project safety', content: 'Review commands before approving changes outside this project.' }] }));
    await page.goto('/settings');
    const groups = page.getByRole('combobox', { name: 'Settings group', exact: true });
    const search = page.getByRole('searchbox', { name: 'Search settings' });
    await expect(search).toBeVisible();
    if (width === 390) {
      await expect(groups).toBeVisible();
      await expect(page.getByRole('button', { name: 'Auto-approve', exact: true })).toHaveCount(0);
      await expect(groups.getByRole('option', { name: 'Account', exact: true })).toHaveCount(0);
      await groups.selectOption('auto-approve');
    } else {
      await expect(groups).toBeHidden();
      await page.getByRole('button', { name: 'Auto-approve', exact: true }).click();
    }
    await expect(page.getByRole('textbox', { name: 'Section 1 title' })).toHaveValue('Project safety');
    const content = page.getByRole('textbox', { name: 'Section 1 instructions' });
    const bounds = await content.boundingBox();
    expect(bounds!.width).toBeGreaterThan(width === 390 ? 300 : 500);
    await search.fill('Bell sound');
    await page.getByRole('button', { name: /Notifications Bell sound/ }).click();
    await expect(search).toHaveValue('');
    await expect(page.getByRole('checkbox', { name: 'Bell sound', exact: true })).toBeVisible();
    if (width === 390) await expect(groups).toHaveValue('notifications');
    else await expect(page.getByRole('button', { name: 'Notifications', exact: true })).toHaveAttribute('aria-current', 'page');
    await search.fill('disk space');
    if (width === 390) await groups.selectOption('sessions');
    else await page.getByRole('button', { name: 'Sessions', exact: true }).click();
    await expect(search).toHaveValue('');
    await expect(page.getByRole('heading', { name: 'Sessions', exact: true })).toBeVisible();
  });
}
