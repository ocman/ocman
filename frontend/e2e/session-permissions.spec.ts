import { test, expect, MOCK_SESSION } from './fixtures';
import type { Page } from '@playwright/test';

const syntheticRules = [
  { permission: 'bash', pattern: 'demo first *', action: 'allow' },
  { permission: 'bash', pattern: 'demo second *', action: 'ask' },
  { permission: 'bash', pattern: 'demo third *', action: 'deny' },
];

async function openPhoneEditor(page: Page) {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route('**/permission-policy?*', route => route.fulfill({ json: { rules: syntheticRules, requests: [] } }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const opener = page.getByRole('button', { name: 'Open session details' });
  await opener.focus();
  await expect(opener).toBeFocused();
  await page.evaluate(() => window.dispatchEvent(new Event('oc-session-permissions-open')));
  const editor = page.getByRole('dialog', { name: 'Session permissions' });
  await expect(editor.getByLabel('Rule 1 pattern')).toHaveValue(syntheticRules[0].pattern);
  return { editor, opener };
}

test('dirty session permissions use app confirmation and restore focus at 390px', async ({ mockedPage: page }) => {
  const { editor, opener } = await openPhoneEditor(page);
  const browserDialogs: string[] = [];
  page.on('dialog', async dialog => { browserDialogs.push(dialog.message()); await dialog.dismiss(); });
  const pattern = editor.getByLabel('Rule 1 pattern');
  await pattern.fill('demo edited *');
  const close = editor.getByRole('button', { name: 'Close session permissions' });
  await close.focus();
  await page.keyboard.press('Enter');
  expect(browserDialogs).toEqual([]);
  const confirmation = page.getByRole('dialog', { name: 'Discard unsaved session permission rules?' });
  const cancel = confirmation.getByRole('button', { name: 'Cancel' });
  await expect(cancel).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(close).toBeFocused();
  await expect(pattern).toHaveValue('demo edited *');
  await expect(editor.getByRole('button', { name: 'Save session rules' })).toBeEnabled();

  await pattern.focus();
  await page.keyboard.press('Escape');
  await expect(cancel).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(pattern).toBeFocused();
  await expect(pattern).toHaveValue('demo edited *');

  await page.keyboard.press('Escape');
  await confirmation.getByRole('button', { name: 'Discard changes' }).click();
  await expect(confirmation).toHaveCount(0);
  await expect(editor).toHaveCount(0);
  await expect(opener).toBeFocused();
});

test('keyboard focus follows reordered session permission rules at 390px', async ({ mockedPage: page }) => {
  const { editor } = await openPhoneEditor(page);
  const arrow = (row: number, direction: 'up' | 'down') => editor.getByRole('button', { name: `Move rule ${row} ${direction}` });
  await arrow(1, 'down').focus();
  await page.keyboard.press('Enter');
  await expect(editor.getByLabel('Rule 1 pattern')).toHaveValue(syntheticRules[1].pattern);
  await expect(editor.getByLabel('Rule 2 pattern')).toHaveValue(syntheticRules[0].pattern);
  await expect(arrow(2, 'down')).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(editor.getByLabel('Rule 3 pattern')).toHaveValue(syntheticRules[0].pattern);
  await expect(arrow(3, 'down')).toBeDisabled();
  await expect(arrow(3, 'up')).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(arrow(2, 'up')).toBeFocused();
  await page.keyboard.press('Enter');
  await expect(arrow(1, 'up')).toBeDisabled();
  await expect(arrow(1, 'down')).toBeFocused();
  for (const [index, rule] of syntheticRules.entries()) {
    await expect(editor.getByLabel(`Rule ${index + 1} pattern`)).toHaveValue(rule.pattern);
    await expect(editor.getByLabel(`Rule ${index + 1} action`)).toHaveValue(rule.action);
  }
});

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
