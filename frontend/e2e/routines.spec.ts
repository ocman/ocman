import { test, expect } from './fixtures';

test('creates and runs a routine', async ({ mockedPage: page }) => {
  let routines: object[] = [];
  let runs: object[] = [];
  await page.route('/api/sessions/resolve-targets', (route) => route.fulfill({ json: { candidates: [{ remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: '/home/user/projects/myapp' }], remotes: [] } }));
  await page.route('/api/routines', async (route) => {
    if (route.request().method() === 'POST') {
      const body = route.request().postDataJSON();
      const routine = { ...body, id: 'routine-1', scheduleKind: body.schedule.kind, scheduleConfigJSON: '{}', nextDueAt: 0, deleted: false, createdAt: Date.now(), updatedAt: Date.now() };
      routines = [routine];
      return route.fulfill({ status: 201, json: routine });
    }
    return route.fulfill({ json: routines });
  });
  await page.route('/api/routines/routine-1/history', (route) => route.fulfill({ json: runs }));
  await page.route('/api/routines/routine-1/run', (route) => {
    const run = { id: 'run-1', routineId: 'routine-1', routineName: 'Release check', prompt: 'Check release health', directory: '/home/user/projects/myapp', remoteId: 'local', trigger: 'manual', platform: 'opencode', sessionId: 'routine-session', state: 'running', occurrenceAt: Date.now(), createdAt: Date.now() };
    runs = [run];
    return route.fulfill({ json: run });
  });

  await page.goto('/routines');
  await page.getByRole('button', { name: 'New routine' }).click();
  await page.getByLabel('Name').fill('Release check');
  await page.getByLabel('Prompt').fill('Check release health');
  await page.getByRole('combobox', { name: 'Project' }).click();
  await page.getByRole('option', { name: '/home/user/projects/myapp' }).click();
  await page.getByRole('button', { name: 'Create routine' }).click();
  await expect(page.getByRole('heading', { name: 'Release check' })).toBeVisible();
  await page.getByRole('button', { name: 'Run now' }).click();
  await page.getByText('History (1)').click();
  await expect(page.getByRole('link', { name: 'Open session' })).toHaveAttribute('href', '/session/routine-session?platform=opencode');
});
