import { test, expect, MOCK_PROJECT } from './fixtures';

// A localhost subdomain exercises the remote-browser client picker.
const origin = new URL(process.env.E2E_BASE_URL ?? 'http://localhost:8228');
origin.hostname = 'tmux-ui.localhost';
test.use({ baseURL: origin.origin });

for (const width of [1280, 390]) {
  test(`tmux client picker supports keyboard selection and stays on screen at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    const session = '~/projects/myapp';
    await page.route('/api/tmux/sessions', route => route.fulfill({ json: { available: true, sessions: [{ name: session, resolvedPath: MOCK_PROJECT.directory }] } }));
    await page.route('/api/tmux/clients', route => route.fulfill({ json: { available: true, clients: [
      { tty: '/dev/ttys001', session: '~/projects/myapp', width: '120', height: '40' },
      { tty: '/dev/ttys002', session: '~/projects/another-project-with-a-long-name', width: '80', height: '24' },
    ] } }));
    let selected: unknown;
    await page.route('/api/tmux/switch', async route => {
      selected = route.request().postDataJSON();
      await route.fulfill({ status: 204 });
    });
    await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}`);
    await page.getByRole('button', { name: 'tmux', exact: true }).click();
    const picker = page.getByRole('group', { name: 'Select tmux client' });
    await expect(picker).toBeVisible();
    const bounds = await picker.boundingBox();
    expect(bounds!.x).toBeGreaterThanOrEqual(12);
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width - 12);
    const client = picker.getByRole('button', { name: /ttys002/ });
    await client.focus();
    await client.press('Enter');
    await expect(picker).toBeHidden();
    expect(selected).toEqual({ session, client: '/dev/ttys002' });
  });
}
