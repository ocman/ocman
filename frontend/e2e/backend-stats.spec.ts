import { test, expect, MOCK_SESSION } from './fixtures';

test('bottom stats start collapsed, align expanded items and remember the choice', async ({ mockedPage: page }, testInfo) => {
  await page.route(/\/api\/sessions\?view=running-count$/, (route) => route.fulfill({ json: { count: 5 } }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  await expect(page.getByTitle('Currently running sessions, including archived sessions and subagents')).toHaveText('active sessions: 5');
  const show = page.getByRole('button', { name: 'Show system stats' });
  await expect(show).toHaveAttribute('aria-expanded', 'false');
  await expect(page.getByTitle('Backend memory usage')).toHaveCount(0);
  await page.getByTestId('session-sidebar').screenshot({ path: testInfo.outputPath('collapsed.png') });

  await show.click();
  const backend = page.getByTitle('Backend memory usage');
  const uptime = page.getByTitle('Time since the backend started');
  await expect(backend).toBeVisible();
  await expect(uptime).toBeVisible();
  const backendBox = (await backend.boundingBox())!;
  const frontendBox = (await page.getByTitle(/^Frontend Memory \(/).boundingBox())!;
  const uptimeBox = (await uptime.boundingBox())!;
  expect(frontendBox.y).toBe(backendBox.y);
  expect(frontendBox.x).toBeGreaterThan(backendBox.x);
  expect([backendBox.x, frontendBox.x]).toContain(uptimeBox.x);
  await page.getByTestId('session-sidebar').screenshot({ path: testInfo.outputPath('expanded.png') });
  await page.reload();
  await expect(page.getByRole('button', { name: 'Hide system stats' })).toHaveAttribute('aria-expanded', 'true');
  await page.getByRole('button', { name: 'Hide system stats' }).click();
  await page.reload();
  await expect(page.getByRole('button', { name: 'Show system stats' })).toHaveAttribute('aria-expanded', 'false');
});
