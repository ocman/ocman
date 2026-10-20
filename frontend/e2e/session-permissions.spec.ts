import { test, expect, MOCK_SESSION } from './fixtures';

for (const width of [1440, 390]) {
  test(`session permission rules and history work at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 1100 });
    const rules = [
      { permission: 'bash', pattern: 'tea do *', action: 'allow' },
      { permission: 'bash', pattern: 'tea do delete *', action: 'ask' },
    ];
    const requests = [
      { platform: 'opencode', sessionId: MOCK_SESSION.id, permissionId: 'approved', permission: 'bash', patterns: ['tea do something'], requestedAt: Date.now() - 60_000, resolvedAt: Date.now() - 59_000, resolution: 'user-once' },
      { platform: 'opencode', sessionId: 'subagent', permissionId: 'pending', permission: 'bash', patterns: ['tea do delete release'], requestedAt: Date.now() - 30_000, resolution: 'pending' },
    ];
    await page.route('**/permission-policy?*', route => route.fulfill({ json: {
      rules: route.request().method() === 'PUT' ? route.request().postDataJSON().rules : rules,
      requests,
    } }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    await expect(page.getByTestId('conversation-composer')).toBeVisible();
    await page.evaluate(() => window.dispatchEvent(new Event('oc-session-permissions-open')));
    const dialog = page.getByRole('dialog', { name: 'Session permissions' });
    await expect(dialog.getByLabel('Rule 1 pattern')).toHaveValue('tea do *');
    await expect(dialog.getByText('tea do something', { exact: true })).toBeVisible();
    const panelBox = (await dialog.boundingBox())!;
    if (width > 600) expect(panelBox.width).toBeCloseTo(width * 0.8, 0);
    const patternBox = (await dialog.getByLabel(width > 600 ? 'Rule 1 pattern' : 'Rule 1 action').boundingBox())!;
    const arrowBox = (await dialog.getByRole('button', { name: 'Move rule 1 up' }).boundingBox())!;
    expect(Math.abs(patternBox.y + patternBox.height / 2 - arrowBox.y - arrowBox.height / 2)).toBeLessThan(2);
    await expect(dialog.getByRole('button', { name: 'Move rule 1 up' })).toHaveText('');
    expect(await dialog.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath('session-permissions.png'), fullPage: true });
    await dialog.getByRole('button', { name: 'deny tea do something' }).click();
    await dialog.getByLabel('Rule 3 pattern').fill('tea do something *');
    const saved = page.waitForRequest(request => request.method() === 'PUT' && request.url().includes('/permission-policy'));
    await dialog.getByRole('button', { name: 'Save session rules' }).click();
    expect((await saved).postDataJSON().rules[2]).toEqual({ permission: 'bash', pattern: 'tea do something *', action: 'deny' });
    await expect(dialog.getByRole('button', { name: 'Save session rules' })).toBeDisabled();
  });
}
