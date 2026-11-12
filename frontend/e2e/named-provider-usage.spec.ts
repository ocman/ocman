import { test, expect } from './fixtures';

test('named accounts keep distinct current usage and history on desktop and phone', async ({ mockedPage: page }, testInfo) => {
  const now = Date.now();
  const reset = new Date(now + 3_600_000).toISOString();
  const providers = [
    { id: 'cnx_work', name: 'Claude Work', platformName: 'Claude Code', machineName: 'This machine', plan: 'max', status: 'ok', windows: [
      { name: '5 hours', usedPercent: 35, resetsAt: reset }, { name: '7 days', usedPercent: 82, resetsAt: new Date(now + 3 * 86_400_000).toISOString() },
    ] },
    { id: 'cnx_personal', name: 'Claude Personal', platformName: 'Claude Code', machineName: 'This machine', plan: 'pro', status: 'ok', windows: [
      { name: '5 hours', usedPercent: 12, resetsAt: reset }, { name: '7 days', usedPercent: 24, resetsAt: new Date(now + 5 * 86_400_000).toISOString() },
    ] },
    { id: 'cnx_codex', name: 'Codex Work', platformName: 'Codex', machineName: 'This machine', plan: 'pro', status: 'ok', windows: [
      { name: '5 hours', usedPercent: 58, resetsAt: reset }, { name: '7 days', usedPercent: 41, resetsAt: new Date(now + 2 * 86_400_000).toISOString() },
    ] },
  ];
  await page.route('**/api/provider-usage', route => route.fulfill({ json: { providers } }));
  await page.route('**/api/provider-usage/history?*', route => route.fulfill({ json: {
    sampleIntervalMs: 300_000,
    samples: providers.flatMap(provider => [0, 1, 2].map(index => ({ sampledAt: now - (2 - index) * 300_000,
      provider: { ...provider, windows: provider.windows.map(window => ({ ...window, usedPercent: window.usedPercent - (2 - index) * 2 })) },
    }))),
  } }));
  await page.setViewportSize({ width: 1440, height: 900 });
  await page.goto('/provider-usage');
  const content = page.getByTestId('page-content');
  await expect(content.getByRole('region', { name: 'Claude Work', exact: true })).toContainText('Claude Code · This machine');
  await expect(content.getByRole('progressbar', { name: 'Claude Work 5 hours usage' })).toHaveAttribute('value', '35');
  await expect(content.getByRole('progressbar', { name: 'Claude Personal 5 hours usage' })).toHaveAttribute('value', '12');
  await expect(content.getByText(/Connections using the same account may share limits/)).toBeVisible();
  await page.screenshot({ path: testInfo.outputPath('named-provider-usage-desktop.png'), fullPage: true });
  await page.getByRole('button', { name: 'Provider usage', exact: true }).click();
  const popover = page.getByRole('dialog', { name: 'Provider usage' });
  await expect(popover.getByRole('progressbar', { name: 'Claude Personal 5 hours usage' })).toHaveAttribute('value', '12');
  await page.keyboard.press('Escape');
  await page.getByRole('tab', { name: 'History', exact: true }).click();
  await expect(content.getByRole('img', { name: 'Claude Work subscription usage history' })).toBeVisible();
  await expect(content.getByRole('img', { name: 'Claude Personal subscription usage history' })).toBeVisible();
  await page.reload();
  await expect(content.getByRole('heading', { name: 'Claude Personal', exact: true })).toBeVisible();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(content.getByRole('img', { name: 'Claude Work subscription usage history' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.getByRole('tab', { name: 'Current usage', exact: true }).click();
  await expect(content.getByRole('progressbar', { name: 'Claude Personal 5 hours usage' })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBeLessThanOrEqual(390);
  await page.screenshot({ path: testInfo.outputPath('named-provider-usage-phone.png'), fullPage: true });
});
