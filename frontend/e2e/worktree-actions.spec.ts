import { test, expect, MOCK_PROJECT } from './fixtures';

for (const width of [1280, 390]) {
  test(`project and worktree actions stay usable at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: width === 390 ? 844 : 800 });
    const project = MOCK_PROJECT.directory;
    await page.route('**/api/worktree/list*', route => route.fulfill({ json: { worktrees: [
      { path: project, branch: 'main', head: 'abc', bare: false, locked: false, main: true },
      { path: '/home/user/.worktrees/myapp/shared-controls', branch: 'chore/shared-controls', head: 'def', bare: false, locked: false, main: false },
    ] } }));
    await page.route('**/api/worktree/remove', route => route.fulfill({ status: 409, json: { error: 'Worktree has uncommitted changes' } }));
    await page.goto(`/project/${encodeURIComponent(project)}`);
    await expect(page.getByRole('button', { name: 'Exclude archived' })).toBeVisible();
    await expect(page.getByRole('button', { name: 'Worktrees', exact: true })).toBeVisible();
    await page.getByRole('button', { name: 'Worktrees', exact: true }).click();
    await expect(page.getByText('chore/shared-controls', { exact: true })).toBeVisible();
    await expect(page.getByRole('group', { name: 'Actions for chore/shared-controls' })).toHaveCSS('white-space', 'nowrap');
    await expect(page.getByRole('link', { name: 'Back to project' })).toBeVisible();
    const bounds = await page.getByRole('button', { name: 'New worktree session', exact: true }).boundingBox();
    expect(bounds!.x + bounds!.width).toBeLessThanOrEqual(width);
    await page.getByRole('button', { name: 'Delete', exact: true }).click();
    await expect(page.getByRole('button', { name: 'Confirm delete' })).toBeVisible();
    await page.getByRole('button', { name: 'Confirm delete' }).click();
    await expect(page.getByRole('button', { name: 'Force delete' })).toBeVisible();
  });
}
