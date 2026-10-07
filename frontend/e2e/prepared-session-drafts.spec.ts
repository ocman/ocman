import { test, expect } from './fixtures';

test('prepares multiple sidebar drafts without starting sessions', async ({ mockedPage: page }, testInfo) => {
  let starts = 0;
  await page.route('**/api/sessions/start', async (route) => {
    starts++;
    await route.fulfill({ json: {} });
  });
  await page.route('**/api/sessions/prepare', (route) => route.fulfill({ json: {
    platform: 'opencode', agents: [{ name: 'build' }, { name: 'plan' }], commands: [],
    models: { hasProviders: true, models: [] }, liveConnection: false,
  } }));
  await page.route('**/api/worktree/list?*', (route) => route.fulfill({ json: { worktrees: [] } }));
  await page.route('**/api/git/info?*', (route) => route.fulfill({ json: {} }));
  await page.goto('/session/new?dir=%2Frepo&draftId=first&title=Plan+the+API');
  await page.getByRole('textbox').fill('Design the API before implementation.');
  await expect.poll(() => page.evaluate(() => localStorage.getItem('ocman.composerDrafts.v1')))
    .toContain('Design the API');
  await page.goto('/session/new?dir=%2Frepo&draftId=second&title=Prepare+the+UI');
  await expect(page.getByRole('textbox')).toHaveValue('');
  await page.getByRole('textbox').fill('Prepare the sidebar UI.');
  const drafts = page.getByLabel('Prepared sessions');
  await drafts.getByRole('button', { name: /Plan the API/ }).click();
  await expect(page.getByRole('textbox')).toHaveValue('Design the API before implementation.');
  await drafts.getByRole('button', { name: /Prepare the UI/ }).click();
  await expect(page.getByRole('textbox')).toHaveValue('Prepare the sidebar UI.');
  await expect.poll(() => page.evaluate(() => localStorage.getItem('ocman.composerDrafts.v1')))
    .toContain('Prepare the sidebar UI.');
  await page.reload();
  await expect(drafts.getByRole('button', { name: /Plan the API/ })).toBeVisible();
  await expect(page.getByRole('textbox')).toHaveValue('Prepare the sidebar UI.');
  const screenshot = testInfo.outputPath('prepared-session-drafts.png');
  await page.screenshot({ path: screenshot });
  await testInfo.attach('prepared-session-drafts', { path: screenshot, contentType: 'image/png' });
  await page.setViewportSize({ width: 390, height: 844 });
  const toggle = page.getByTestId('mobile-sessions-toggle');
  await toggle.click();
  await drafts.getByRole('button', { name: /Prepare the UI/ }).click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.click();
  await drafts.getByRole('button', { name: /Plan the API/ }).click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await expect(page.getByRole('textbox')).toHaveValue('Design the API before implementation.');
  await toggle.click();
  await drafts.getByRole('button', { name: /Prepare the UI/ }).click();
  await expect(toggle).toHaveAttribute('aria-expanded', 'false');
  await toggle.click();
  await drafts.getByRole('button', { name: 'Discard draft' }).first().click();
  await expect(drafts.getByRole('button', { name: /Plan the API/ })).toHaveCount(0);
  await toggle.click();
  await expect(page.getByRole('textbox')).toHaveValue('Prepare the sidebar UI.');
  expect(starts).toBe(0);
});
