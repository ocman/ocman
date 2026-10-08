import { test, expect, MOCK_PROJECT, MOCK_SESSION } from './fixtures';

for (const width of [1280, 390]) {
  test(`project, worktree and session controls exclude tmux and VS Code at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.route('/api/tmux/sessions', route => route.fulfill({ json: { available: true, sessions: [{ name: '~/projects/myapp', resolvedPath: MOCK_PROJECT.directory }] } }));
    await page.route('/api/tmux/clients', route => route.fulfill({ json: { available: true, clients: [
      { tty: '/dev/ttys001', session: '~/projects/myapp', width: '120', height: '40' },
    ] } }));
    await page.route('/api/worktree/list*', route => route.fulfill({ json: { worktrees: [
      { path: MOCK_PROJECT.directory, branch: 'main', main: true, locked: false, bare: false, head: 'abc' },
      { path: '/home/user/.worktrees/myapp/feature', branch: 'feature', main: false, locked: false, bare: false, head: 'def' },
    ] } }));
    await page.goto(`/project/${encodeURIComponent(MOCK_PROJECT.directory)}`);
    await expect(page.getByRole('button', { name: 'Worktrees', exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: /^(tmux|VS Code)$/ })).toHaveCount(0);
    await page.getByRole('button', { name: 'Worktrees', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Delete', exact: true })).toBeVisible();
    await expect(page.getByRole('button', { name: /tmux|VS Code/ })).toHaveCount(0);
    await page.goto(`/session/${MOCK_SESSION.id}`);
    await page.getByRole('button', { name: 'Session actions' }).click();
    await expect(page.getByRole('menuitem', { name: 'New session', exact: true })).toBeVisible();
    await expect(page.getByRole('menuitem', { name: /Switch tmux|Open in VS Code/ })).toHaveCount(0);
  });
}
