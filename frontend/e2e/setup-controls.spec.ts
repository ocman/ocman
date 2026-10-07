import { test, expect } from './fixtures';

for (const width of [1280, 390]) {
  test(`setup controls dismiss optional warnings and recover from required failures at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    let required = false;
    let healthy = false;
    await page.route('/api/doctor', route => route.fulfill({ json: { logPath: '/home/user/.local/share/ocman/ocman.log', checks: healthy ? [] : [required
      ? { id: 'opencode-db', label: 'OpenCode database', required: true, ok: false, detail: 'OpenCode database was not found.', hint: 'Run OpenCode once to create it, then re-check.' }
      : { id: 'whisper', label: 'Voice transcription', required: false, ok: false, detail: 'whisper-cpp is not on PATH.', hint: 'Install whisper-cpp to enable voice input.' },
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
    await expect(page.getByRole('button', { name: 'Include archived', exact: true })).toBeVisible();
  });
}
