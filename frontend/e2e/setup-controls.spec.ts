import { test, expect, MOCK_PLATFORM_OPTIONS } from './fixtures';

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
  test(`legacy owner Codex selection checks, installs and rechecks dependencies at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 844 });
    // Owners without the named lifecycle still mount the compatibility UI.
    // Its automatic installer remains a supported, separately tested flow.
    await page.route('**/api/platform-connections**', route => route.fulfill({ json: { supported: false, onboardingComplete: true, connections: [], platforms: [] } }));
    let enabled = ['opencode', 'claudecode'];
    let ready = false;
    let checks = 0;
    await page.route('/api/settings/platforms/codex/setup', route => {
      checks++;
      return route.fulfill({ json: { ready, installable: true, command: 'pnpm add -g @agentclientprotocol/codex-acp@2.5.0', notes: ['Codex requires the Codex CLI, Node.js 22 or newer and the Codex ACP adapter.'], checks: [{ id: 'codex-acp', label: 'Codex ACP adapter', ok: ready, detail: 'Not installed', hint: 'Install the adapter.' }] } });
    });
    await page.route('/api/settings/platforms/codex/install', route => {
      ready = true;
      return route.fulfill({ json: { ready, installable: true, checks: [] } });
    });
    await page.route('/api/settings/platforms', async route => {
      if (route.request().method() === 'POST') enabled = route.request().postDataJSON().enabled;
      await route.fulfill({ json: { platforms: MOCK_PLATFORM_OPTIONS, enabled } });
    });
    await page.goto('/settings?tab=platforms');
    const codex = page.getByRole('checkbox', { name: 'Codex', exact: true });
    await expect(codex).toBeEnabled();
    await codex.click();
    await expect(page.getByRole('dialog', { name: 'Set up Codex' })).toBeVisible();
    await expect(codex).not.toBeChecked();
    await expect(page.getByText('pnpm add -g @agentclientprotocol/codex-acp@2.5.0', { exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Copy install command' })).toBeVisible();
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

for (const width of [1280, 390]) {
  test(`named Codex setup pins the owner receipt through dependency checks at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    let ready = false;
    const checks: Record<string, unknown>[] = [];
    const saved: Record<string, unknown>[] = [];
    const command = 'pnpm add -g @agentclientprotocol/codex-acp@2.5.0';
    const platforms = [{ id: 'codex', label: 'Codex', existingDirectory: '/config/codex', supportsIsolation: true, isolationNote: 'Separate native configuration.' }];
    await page.route('**/api/platform-connections**', route => {
      const request = route.request();
      expect(new URL(request.url()).searchParams.get('remoteId')).toBe('local');
      if (new URL(request.url()).pathname.endsWith('/check')) {
        checks.push(request.postDataJSON());
        return route.fulfill({ json: { reservationId: 'stp_codex', status: ready ? 'ready' : 'dependencies-missing', directory: '/config/codex-work', checks: [{ label: 'Codex ACP adapter', ok: ready, detail: ready ? 'Dependencies are available.' : 'Not installed. Install the adapter.' }], ...(!ready ? { instructions: 'Codex requires Node.js 22 or newer and the Codex ACP adapter. Install on this machine, then check again.', command } : {}) } });
      }
      if (request.method() === 'POST') {
        saved.push(request.postDataJSON());
        return route.fulfill({ json: { saved: true } });
      }
      return route.fulfill({ json: { supported: true, onboardingComplete: true, platforms, connections: saved.length ? [{ id: 'cnx_codex_work', kind: 'codex', name: 'Codex Work', configuration: 'isolated', directory: '/config/codex-work', enabled: true, isDefault: false, status: 'ready' }] : [] } });
    });
    await page.goto('/settings?tab=platforms');
    await page.getByRole('button', { name: 'Add platform', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Add platform', exact: true });
    await dialog.getByRole('textbox', { name: 'Name', exact: true }).fill('Codex Work');
    await dialog.getByRole('combobox', { name: 'Configuration', exact: true }).selectOption('isolated');
    await dialog.getByRole('button', { name: 'Check setup', exact: true }).click();
    await expect(dialog.getByRole('status').filter({ hasText: /^Setup required$/ })).toBeVisible();
    await expect(dialog.getByText(command, { exact: true })).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Copy sign-in command', exact: true })).toBeVisible();
    await expect(dialog.getByText(/Node.js 22/)).toBeVisible();
    await expect(dialog.getByRole('button', { name: 'Save for later', exact: true })).toBeEnabled();
    await dialog.getByRole('button', { name: 'Check again', exact: true }).click();
    await expect.poll(() => checks.length).toBe(2);
    expect(saved).toHaveLength(0);
    ready = true; // Represents completing the displayed command on the owner.
    await dialog.getByRole('button', { name: 'Check again', exact: true }).click();
    await expect(dialog.getByRole('status').filter({ hasText: /^Ready$/ })).toBeVisible();
    await expect(dialog.getByText('Dependencies are available.', { exact: true })).toBeVisible();
    await expect(dialog.getByText(command, { exact: true })).toBeHidden();
    await dialog.getByRole('button', { name: 'Add platform', exact: true }).click();
    await expect(dialog).toBeHidden();
    await expect(page.getByRole('button', { name: 'Codex Work', exact: true })).toBeVisible();
    expect(checks).toHaveLength(3);
    expect(checks[0]).not.toHaveProperty('reservationId');
    for (const check of checks.slice(1)) expect(check).toMatchObject({ kind: 'codex', configuration: 'isolated', reservationId: 'stp_codex' });
    expect(saved).toEqual([{ reservationId: 'stp_codex', name: 'Codex Work', enabled: true }]);
  });
}
