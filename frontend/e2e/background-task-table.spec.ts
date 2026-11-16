import { test, expect } from './fixtures';

for (const width of [1280, 390]) {
  test(`task groups stay compact and restore the selected details at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 900 });
    const names = ['Conversation sync', 'Repository sync', 'PR monitoring', 'Worktree cleanup', 'History maintenance', 'Session preparation'];
    await page.route('/api/background-tasks?*', route => route.fulfill({ json: names.map((title, index) => ({
      id: String(index), key: `group-${index}`, kind: 'maintenance', title, ownerId: 'local',
      status: index === 0 ? 'running' : index === 2 ? 'failed' : 'succeeded',
      error: index === 2 ? 'Forge connection timed out' : undefined,
      createdAt: Date.now() - (index + 1) * 60_000, updatedAt: Date.now(),
      progress: { phase: 'Checking sessions', completed: 34, total: 50, unit: 'sessions', counters: { imported: 28, unchanged: 6 } },
    })) }));
    await page.goto('/sessions?activity=tasks&keep=yes');
    const table = page.getByRole('table', { name: 'Background task groups' });
    const rows = table.getByRole('row').filter({ has: page.getByRole('button') });
    await expect(rows).toHaveCount(5);
    await expect(page.getByRole('progressbar')).toHaveCount(0);
    const sync = table.getByRole('button', { name: 'Conversation sync', exact: true });
    await sync.focus();
    await sync.press('Enter');
    const details = page.getByRole('region', { name: 'Task group details for Conversation sync' });
    await expect(details).toBeVisible();
    await expect(details.getByLabel('Current progress for Conversation sync').getByRole('progressbar')).toHaveAttribute('value', '34');
    await expect(page).toHaveURL(/taskGroup=/);
    await page.reload();
    await expect(details).toBeVisible();
    await expect(sync).toHaveAttribute('aria-expanded', 'true');
    expect(await table.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
    expect(await details.evaluate(element => element.scrollWidth <= element.clientWidth)).toBe(true);
    await details.scrollIntoViewIfNeeded();
    await page.screenshot({ path: testInfo.outputPath('background-task-table.png') });
    await page.getByRole('button', { name: 'Close task details' }).click();
    await expect(sync).toBeFocused();
    await expect(details).toHaveCount(0);
    await page.getByRole('button', { name: 'Next task groups' }).click();
    await expect(rows).toHaveCount(1);
    await expect(rows.nth(0)).toContainText('Session preparation');
    await expect(page).toHaveURL(/taskPage=2/);
    await page.reload();
    await expect(rows).toHaveCount(1);
    await page.getByRole('button', { name: 'Previous task groups' }).click();
    await expect(rows).toHaveCount(5);
    await rows.filter({ hasText: 'PR monitoring' }).getByText('Failed', { exact: true }).click();
    await expect(page.getByRole('region', { name: 'Task group details for PR monitoring' }).getByRole('alert')).toHaveText('Forge connection timed out');
    await page.getByRole('combobox', { name: 'Task status' }).selectOption('failed');
    await expect(rows).toHaveCount(1);
    await expect(page.getByRole('region', { name: 'Task group details for PR monitoring' })).toHaveCount(0);
    await expect(page).toHaveURL(/keep=yes/);
  });
}
