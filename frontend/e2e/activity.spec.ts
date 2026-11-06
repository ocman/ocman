import { test, expect } from './fixtures';

test('Activity sits below Inbox and opens background tasks and live resources', async ({ mockedPage: page }) => {
  let samples = 0;
  await page.route('/api/background-tasks?*', (route) => route.fulfill({ json: [] }));
  await page.route('/api/resources?remoteId=local', (route) => {
    samples++;
    const point = { timestamp: Date.now(), cpuPercent: 125, memoryBytes: 1024 ** 3 };
    return route.fulfill({ json: {
      ...point, history: [point],
      processes: [{ pid: 100, parentPid: 1, name: 'ocman', cpuPercent: 25, memoryBytes: 128 * 1024 ** 2 },
        { pid: 101, parentPid: 100, name: 'coding-agent', cpuPercent: 100, memoryBytes: 896 * 1024 ** 2 }],
    } });
  });
  await page.goto('/');
  const nav = page.getByRole('navigation', { name: 'Main navigation' });
  const inbox = nav.getByRole('link', { name: /^Inbox/ });
  const activity = nav.getByRole('button', { name: 'Activity', exact: true });
  await expect(activity).toBeVisible();
  const inboxBox = await inbox.boundingBox();
  const activityBox = await activity.boundingBox();
  expect(activityBox!.y).toBeGreaterThan(inboxBox!.y);
  await activity.click();
  const dialog = page.getByRole('dialog', { name: 'Activity', exact: true });
  await expect(dialog.getByRole('tab', { name: 'Background tasks' })).toHaveAttribute('aria-selected', 'true');
  await expect(dialog.getByText('No background tasks match this view.')).toBeVisible();
  expect(samples).toBe(0);
  await dialog.getByRole('tab', { name: 'Resources', exact: true }).click();
  await expect(dialog.getByRole('table', { name: 'Ocman processes' })).toBeVisible();
  await expect(dialog.getByRole('img', { name: 'CPU history' })).toBeVisible();
  await expect(dialog.getByRole('img', { name: 'Memory history' })).toBeVisible();
  await expect(dialog.getByRole('cell', { name: 'coding-agent', exact: true })).toBeVisible();
  const initial = samples;
  await expect.poll(() => samples).toBeGreaterThan(initial);
  await page.keyboard.press('Escape');
  await expect(dialog).not.toBeVisible();
  await expect(activity).toBeFocused();
  await expect(page).not.toHaveURL(/activity=/);
});

test('Activity resource graphs fit a phone viewport and close with the shared control', async ({ mockedPage: page }) => {
  await page.setViewportSize({ width: 390, height: 844 });
  await page.route('/api/resources?*', (route) => route.fulfill({ json: {
    timestamp: Date.now(), cpuPercent: 0, memoryBytes: 0, processes: [], history: [],
  } }));
  await page.goto('/sessions');
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await page.getByRole('navigation', { name: 'More destinations' }).getByRole('link', { name: 'Activity' }).click();
  const dialog = page.getByRole('dialog', { name: 'Activity', exact: true });
  await dialog.getByRole('tab', { name: 'Resources', exact: true }).click();
  await expect(dialog.getByRole('img', { name: 'CPU history' })).toBeVisible();
  await expect(dialog.getByRole('img', { name: 'Memory history' })).toBeVisible();
  const box = await dialog.boundingBox();
  expect(box!.x).toBeGreaterThanOrEqual(0);
  expect(box!.x + box!.width).toBeLessThanOrEqual(390);
  await dialog.getByRole('button', { name: 'Close activity' }).click();
  await expect(dialog).not.toBeVisible();
});
