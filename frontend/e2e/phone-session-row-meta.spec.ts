import { test, expect, MOCK_SESSION, MOCK_SESSION_2 } from './fixtures';

test.use({ serviceWorkers: 'block' });

test('phone rows omit status text and unread counts while desktop retains counts', async ({ mockedPage: page }) => {
  await page.route('/api/git/info*', route => route.fulfill({ json: {
    [MOCK_SESSION.directory]: { branch: 'fix/login', dirty: false, ahead: 0, behind: 0 },
  } }));
  await page.route('/api/sessions*', route => route.fulfill({ json: [
    { ...MOCK_SESSION, status: 'busy', seen: false, unreadCount: 7 },
    { ...MOCK_SESSION_2, status: 'waiting', seen: false, unreadCount: 12 },
  ] }));
  await page.setViewportSize({ width: 390, height: 844 });
  await page.goto('/sessions');
  const first = page.getByRole('link', { name: /Fix the login bug/ });
  const second = page.getByRole('link', { name: /Refactor auth module/ });
  await expect(first).toBeVisible();
  const project = first.getByText('myapp', { exact: true });
  const branch = first.getByText('fix/login', { exact: true });
  await expect(project).toBeVisible();
  await expect(branch).toBeVisible();
  await expect(first.getByTitle('Branch', { exact: true })).toBeVisible();
  const titleBounds = await first.getByText(MOCK_SESSION.title, { exact: true }).boundingBox();
  const projectBounds = await project.boundingBox();
  const branchBounds = await branch.boundingBox();
  expect(projectBounds!.y).toBeGreaterThan(titleBounds!.y + titleBounds!.height);
  expect(branchBounds!.y).toBe(projectBounds!.y);
  await expect(first.getByText('Working', { exact: true })).toHaveCount(0);
  await expect(second.getByText('Waiting', { exact: true })).toHaveCount(0);
  await expect(page.getByTestId('session-unread-badge')).toHaveCount(0);
  await expect(first.getByLabel(/Session status:/)).toBeVisible();
  await expect(second.getByLabel(/Session status:/)).toBeVisible();
  await page.setViewportSize({ width: 1280, height: 900 });
  await expect(project).toBeHidden();
  await expect(branch).toBeHidden();
  await expect(first.getByTestId('session-unread-badge')).toHaveText('7');
  await expect(second.getByTestId('session-unread-badge')).toHaveText('12');
});
