import { test, expect, MOCK_SESSION } from './fixtures';

test('new-session catalog loading shows selector skeletons', async ({ mockedPage: page }) => {
  await page.route('**/api/sessions/prepare', () => {});
  await page.route('**/api/sessions/resolve-targets', (route) => route.fulfill({ json: {
    candidates: [{ remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: MOCK_SESSION.directory }], remotes: [],
  } }));
  await page.route('**/api/git/info*', (route) => route.fulfill({ json: {} }));
  await page.route('**/api/worktree/list*', (route) => route.fulfill({ json: { worktrees: [] } }));
  await page.goto(`/session/new?dir=${encodeURIComponent(MOCK_SESSION.directory)}&platform=opencode`);
  const loading = page.getByRole('status', { name: 'Loading agents and models' });
  await expect(loading).toBeVisible();
  await expect(loading).toHaveAttribute('aria-busy', 'true');
  await expect(page.getByRole('textbox')).toBeEnabled();
  if (process.env.CATALOG_LOADING_SCREENSHOT) {
    await page.screenshot({ path: process.env.CATALOG_LOADING_SCREENSHOT });
  }
});
