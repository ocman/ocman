import { test, expect, MOCK_SESSION, MOCK_SESSION_2 } from './fixtures';

for (const width of [1280, 390]) {
  test(`MCP notification retries installation and dismisses success at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.route('/api/mcp/config', route => route.fulfill({ json: { configured: false, editable: true, wantUrl: 'http://127.0.0.1:8227/mcp', path: '/home/user/.config/opencode/opencode.json' } }));
    let installs = 0;
    await page.route('/api/mcp/config/install', route => {
      installs++;
      return installs === 1
        ? route.fulfill({ status: 409, json: { error: 'Could not write the config file. Try again.' } })
        : route.fulfill({ json: { installed: true, path: '/home/user/.config/opencode/opencode.json', backupPath: '/home/user/.config/opencode/opencode-backup.json', url: 'http://127.0.0.1:8227/mcp' } });
    });
    await page.goto('/sessions');
    await expect(page.getByTestId('mcp-config-prompt')).toBeVisible();
    const prompt = page.getByTestId('mcp-config-prompt');
    expect(await prompt.evaluate(el => el.scrollHeight)).toBeLessThanOrEqual(await prompt.evaluate(el => el.clientHeight));
    await page.getByRole('button', { name: 'Install', exact: true }).click();
    await expect(page.getByTestId('mcp-config-error')).toContainText('Could not write');
    await page.getByRole('button', { name: 'Install', exact: true }).click();
    await expect(page.getByTestId('mcp-config-installed')).toContainText('Restart OpenCode');
    await page.getByRole('button', { name: 'Dismiss', exact: true }).click();
    await expect(page.getByTestId('mcp-config-installed')).toBeHidden();
    expect(installs).toBe(2);
  });

  test(`session notifications stay dismissible while a modal is open at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.route('/api/sessions/notify*', route => route.fulfill({ json: [
      { ...MOCK_SESSION, pendingPermission: true },
      { ...MOCK_SESSION_2, pendingQuestion: true },
    ] }));
    await page.route('/api/session/*/share-links*', route => route.fulfill({ json: [] }));
    await page.goto('/sessions');
    await expect(page.getByRole('button', { name: 'Open session', exact: true })).toHaveCount(2);
    await page.getByRole('button', { name: 'Open session', exact: true }).first().click();
    await expect(page).toHaveURL(new RegExp(`/session/${MOCK_SESSION.id}$`));
    await page.getByRole('button', { name: 'Session actions' }).click();
    await page.getByRole('menuitem', { name: 'Share link…' }).press('Enter');
    const dialog = page.getByRole('dialog', { name: 'Public share link' });
    await expect(dialog).toBeVisible();
    await page.getByRole('button', { name: 'Dismiss', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Open session', exact: true })).toHaveCount(0);
    await expect(dialog).toBeVisible();
  });

  test(`backend notification retries the connection at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    // Keep automatic reads offline until the actual gesture, otherwise a
    // background success can dismiss the toast before Playwright clicks it.
    await page.addInitScript(() => {
      document.addEventListener('click', event => {
        if ((event.target as Element).closest('[data-testid="backend-status-banner"] button')) {
          document.documentElement.dataset.backendRetry = 'clicked';
        }
      }, true);
    });
    await page.route('/api/**', async route => {
      const retried = await page.evaluate(() => document.documentElement.dataset.backendRetry === 'clicked');
      return !retried && !route.request().url().endsWith('/api/auth/me')
        ? route.fulfill({ status: 502, json: { error: 'Backend gateway unavailable' } })
        : route.fallback();
    });
    await page.goto('/sessions');
    const banner = page.getByTestId('backend-status-banner');
    await expect(banner).toBeVisible();
    await banner.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(banner).toBeHidden();
  });
}
