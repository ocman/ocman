import { test, expect, MOCK_SESSION } from './fixtures';
import type { PageDocument } from '../src/lib/pluginPages';

test('enabled plugin workspace appears in navigation and saves private target settings', async ({ mockedPage: page }) => {
  // Never send an unmocked API request to a developer's running backend.
  await page.context().route('**/api/**', (route) => route.abort());
  await page.route('/api/capabilities', (route) => route.fulfill({ json: {
    hosts: [{ remoteId: 'local', remoteName: 'This machine', pluginManagement: true, capabilities: {} }], platforms: [],
  } }));
  await page.route('/api/plugins/pages?*', (route) => route.fulfill({ json: [{ ownerId: 'local', pluginId: 'be.nousefreak.pentest', page: { id: 'pentest', label: 'Pentest' } }] }));
  await page.route('/api/sessions?*', (route) => route.fulfill({ json: [{ ...MOCK_SESSION, projectId: 'project-a' }] }));
  await page.route('/api/plugins/actions?*', (route) => route.fulfill({ json: [{ ownerId: 'local', pluginId: 'be.nousefreak.pentest', scope: 'owner', action: { id: 'start-pentest', label: 'Start pentest run', placement: 'project' } }] }));
  let doc: PageDocument = {
    revision: 'r1', fields: [{ key: 'selected_target', label: 'Selected target', type: 'text' },
      { key: 'targets', label: 'Targets', type: 'list', fields: [
        { key: 'name', label: 'Target name', type: 'text' }, { key: 'base_url', label: 'Base URL', type: 'text' },
        { key: 'credentials', label: 'Credentials', type: 'list', fields: [{ key: 'user', label: 'Username', type: 'text' }, { key: 'pass', label: 'Password', type: 'secret' }] },
      ] }], values: { selected_target: '', targets: [] }, tree: { available: true, nodes: [{ id: 'run-1', title: 'Staging scan', status: 'closed' }] },
  };
  let saved = 0;
  await page.route('/api/plugins/pages/call', (route) => {
    const input = route.request().postDataJSON();
    expect(input.ownerId).toBe('local');
    expect(input.request.projectId).toBe('project-a');
    if (input.method === 'save') {
      expect(input.request.revision).toBe('r1');
      expect(input.request.values.targets[0].credentials[0].pass).toBe('write-only');
      doc = { ...doc, revision: 'r2', values: { ...input.request.values, targets: [{ ...input.request.values.targets[0], credentials: [{ ref: '0:0', user: 'alice', pass: '' }] }] } };
      saved++;
    }
    return route.fulfill({ json: doc });
  });
  await page.goto('/sessions');
  await page.getByRole('navigation', { name: 'Main navigation' }).getByRole('link', { name: 'Pentest' }).click();
  await expect(page.getByRole('heading', { name: 'Pentest', exact: true })).toBeVisible();
  await page.getByLabel('Project', { exact: true }).selectOption(MOCK_SESSION.directory);
  await page.getByRole('button', { name: 'Add Targets', exact: true }).click();
  await page.getByLabel('Selected target', { exact: true }).fill('staging');
  await page.getByLabel('Targets 1: Target name', { exact: true }).fill('staging');
  await page.getByLabel('Targets 1: Base URL', { exact: true }).fill('https://example.com');
  await page.getByRole('button', { name: 'Add Credentials', exact: true }).click();
  await page.getByLabel('Targets 1: Credentials 1: Username', { exact: true }).fill('alice');
  await page.getByLabel('Targets 1: Credentials 1: Password', { exact: true }).fill('write-only');
  await expect(page.getByRole('button', { name: 'Start pentest run' })).toBeDisabled();
  await page.getByRole('button', { name: 'Save configuration' }).click();
  await expect(page.getByText('Saved.', { exact: true })).toBeVisible();
  await expect(page.getByLabel('Targets 1: Credentials 1: Password', { exact: true })).toHaveValue('');
  await expect(page.getByRole('button', { name: 'Start pentest run' })).toBeEnabled();
  await expect(page.getByText('Staging scan', { exact: true })).toBeVisible();
  expect(saved).toBe(1);
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole('navigation', { name: 'Phone navigation' }).getByRole('link', { name: 'Sessions', exact: true }).click();
  await expect(page).toHaveURL(/\/sessions$/);
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await page.getByRole('navigation', { name: 'More destinations' }).getByRole('link', { name: 'Pentest' }).click();
  await expect(page).toHaveURL(/\/plugins\/be.nousefreak.pentest\/pentest\?ownerId=local$/);
  await expect(page.getByRole('dialog', { name: 'More destinations' })).toHaveCount(0);
  await page.goBack();
  await expect(page).toHaveURL(/\/sessions$/);
  await expect(page.getByRole('dialog', { name: 'More destinations' })).toHaveCount(0);
  await page.goForward();
  await expect(page).toHaveURL(/\/plugins\/be.nousefreak.pentest\/pentest\?ownerId=local$/);
  await expect(page.getByRole('heading', { name: 'Pentest', exact: true })).toBeVisible();
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await expect(page.getByRole('navigation', { name: 'More destinations' }).getByRole('link', { name: 'Pentest' })).toBeVisible();
  await page.getByRole('button', { name: 'Close more destinations' }).click();
  await expect(page.getByRole('heading', { name: 'Pentest', exact: true })).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth)).toBe(true);
  await page.route('/api/plugins/pages?*', (route) => route.fulfill({ json: [] }));
  await page.reload();
  await expect(page.getByText(/Plugin unavailable/)).toBeVisible();
  await page.getByRole('button', { name: 'More', exact: true }).click();
  await expect(page.getByRole('navigation', { name: 'More destinations' }).getByRole('link', { name: 'Pentest' })).toHaveCount(0);
});
