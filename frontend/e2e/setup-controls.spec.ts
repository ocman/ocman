import { test, expect } from './fixtures';

for (const width of [1280, 390]) {
  test(`setup controls dismiss optional warnings and recover from required failures at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    let required = false;
    let healthy = false;
    await page.route('/api/doctor', route => route.fulfill({ json: { logPath: '/home/user/.local/share/ocman/ocman.log', checks: healthy ? [] : [required
      ? { id: 'opencode-db', label: 'OpenCode database', required: true, ok: false, detail: 'OpenCode database was not found.', hint: 'Run OpenCode once to create it, then re-check.' }
      : { id: 'tmux', label: 'tmux', required: false, ok: false, detail: 'tmux is not on PATH.', hint: 'Install tmux to enable terminal sessions.' },
    ] } }));
    await page.goto('/sessions');
    await expect(page.getByTestId('setup-banner')).toBeVisible();
    await page.getByRole('button', { name: 'Dismiss', exact: true }).click();
    await expect(page.getByTestId('setup-banner')).toBeHidden();
    required = true;
    await page.reload();
    await expect(page.getByRole('alertdialog', { name: 'Setup required' })).toBeVisible();
    healthy = true;
    await page.getByRole('button', { name: 'Re-check', exact: true }).click();
    await expect(page.getByTestId('setup-panel')).toBeHidden();
    await expect(page.getByRole('button', { name: width <= 640 ? 'Filter sessions' : 'Include archived', exact: true })).toBeVisible();
  });
}

for (const width of [1280, 390]) {
  test(`Codex selection checks, installs and rechecks dependencies at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 844 });
    let enabled = ['opencode', 'claudecode'];
    let ready = false;
    let checks = 0;
    await page.route('/api/settings/platforms/codex/setup', route => {
      checks++;
      return route.fulfill({ json: { ready, installable: true, checks: [{ id: 'codex-acp', label: 'Codex ACP adapter', ok: ready, detail: 'Not installed', hint: 'Install the adapter.' }] } });
    });
    await page.route('/api/settings/platforms/codex/install', route => {
      ready = true;
      return route.fulfill({ json: { ready, installable: true, checks: [] } });
    });
    await page.route('/api/settings/platforms', async route => {
      if (route.request().method() === 'POST') enabled = route.request().postDataJSON().enabled;
      await route.fulfill({ json: { enabled } });
    });
    await page.goto('/settings?tab=platforms');
    const codex = page.getByRole('checkbox', { name: 'Codex', exact: true });
    await expect(codex).toBeEnabled();
    await codex.click();
    await expect(page.getByRole('dialog', { name: 'Set up Codex' })).toBeVisible();
    await expect(codex).not.toBeChecked();
    await expect(page.getByText('pnpm add -g @agentclientprotocol/codex-acp@2.5.0', { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Copy adapter install command' })).toBeVisible();
    await expect(page.getByText(/Node.js 22/)).toBeVisible();
    await page.getByRole('button', { name: 'Check again', exact: true }).click();
    await page.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(page.getByRole('dialog')).toBeHidden();
    await expect(codex).not.toBeChecked();
    expect(checks).toBe(3);
    await codex.click();
    await page.getByRole('button', { name: 'Install', exact: true }).click();
    await expect(page.getByText('Dependencies are available. Close to enable Codex.')).toBeVisible();
    await page.screenshot({ path: testInfo.outputPath(`codex-setup-${width}.png`), fullPage: true });
    await page.getByRole('button', { name: 'Close', exact: true }).click();
    await expect(codex).toBeChecked();
    await page.getByRole('button', { name: 'Save', exact: true }).click();
    await expect(page.getByText('Platform changes applied.', { exact: true })).toBeVisible();
    expect(enabled).toContain('codex');
    await codex.uncheck();
    await expect(page.getByText('pnpm add -g @agentclientprotocol/codex-acp@2.5.0', { exact: true })).toBeHidden();
  });
}
