import { test, expect, MOCK_SESSION } from './fixtures';

for (const width of [320, 390, 1280]) {
  test(`snooze choices persist and custom picker fits at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 844 });
    await page.goto('/settings?tab=sessions');
    const minutes = page.getByRole('spinbutton', { name: 'Quick choice 1 minutes' });
    await minutes.fill('15');
    await expect(minutes).toHaveValue('15');
    await page.reload();
    await expect(page.getByRole('spinbutton', { name: 'Quick choice 1 minutes' })).toHaveValue('15');
    await page.goto(width <= 640 ? '/sessions' : `/session/${MOCK_SESSION.id}`);
    const row = width <= 640
      ? page.getByRole('row').filter({ has: page.getByRole('link', { name: /Refactor auth module/ }) })
      : page.getByTestId('session-sidebar').getByRole('button', { name: /Refactor auth module/ });
    await row.hover();
    await row.getByRole('button', { name: 'Snooze conversation' }).click();
    await expect(page.getByRole('menuitem', { name: 'In 15 mins', exact: true })).toBeVisible();
    await expect(page.getByRole('menuitem', { name: 'Tomorrow at 8:00 AM', exact: true })).toBeVisible();
    await page.getByRole('menuitem', { name: 'Custom...', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Snooze conversation' });
    await expect(dialog).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Snooze', exact: true })).toBeDisabled();
    await dialog.getByLabel('Wake-up date').fill('2099-01-02');
    await dialog.getByLabel('Wake-up time').fill('14:30');
    await expect(dialog.getByRole('status')).toContainText('Wakes');
    const box = await dialog.boundingBox();
    expect(box!.x).toBeGreaterThanOrEqual(0);
    expect(box!.x + box!.width).toBeLessThanOrEqual(width);
    expect(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    await testInfo.attach(`snooze-${width}`, { body: await page.screenshot(), contentType: 'image/png' });
    const request = page.waitForRequest('**/api/session/snooze');
    await page.route('/api/session/snooze', route => route.fulfill({ json: { ok: true } }));
    await dialog.getByRole('button', { name: 'Snooze', exact: true }).click();
    expect((await request).postDataJSON().until).toBe(await page.evaluate(() => new Date('2099-01-02T14:30').getTime()));
    await expect(dialog).toBeHidden();
  });
}
