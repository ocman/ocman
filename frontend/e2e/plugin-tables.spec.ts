import { test, expect } from './fixtures';

test('plugin pages share one navigation entry and open rich table details in a drawer', async ({ mockedPage: page }) => {
  await page.context().route('**/api/**', (route) => route.abort());
  await page.route('/api/capabilities', (route) => route.fulfill({ json: {
    hosts: [{ remoteId: 'local', pluginManagement: true, capabilities: {} }], platforms: [],
  } }));
  await page.route('/api/projects*', (route) => route.fulfill({ json: [] }));
  await page.route('/api/plugins/pages?*', (route) => route.fulfill({ json:
    ['workspaces', 'findings'].map((id) => ({ ownerId: 'local', pluginId: 'example.pentest', pluginLabel: 'Pentest',
      page: { id, label: id === 'workspaces' ? 'Workspaces' : 'Findings', scope: 'plugin' } })),
  }));
  await page.route('/api/plugins/pages/call', (route) => {
    const { request } = route.request().postDataJSON();
    expect(request.projectId).toBe('');
    if (request.itemId) {
      expect(request.pageId).toBe('findings');
      expect(request.itemId).toBe('finding-a');
      return route.fulfill({ json: { revision: 'r1', fields: [], values: {}, tree: {}, detail: {
        title: 'Finding details', properties: [{ label: 'Remediation', display: 'text', value: 'Verify object ownership.' }],
      } } });
    }
    return route.fulfill({ json: { revision: 'r1', fields: [], values: {}, tree: {}, ...(request.pageId === 'findings' && { collection: {
      title: 'Saved findings', detailPresentation: 'drawer',
      columns: [{ key: 'name', label: 'Name', display: 'summary' }, { key: 'severity', label: 'Severity', display: 'severity' }],
      rows: [{ id: 'finding-a', values: { name: { label: 'Missing authorization', description: 'auth.go:12', count: 2 }, severity: 'high' } }],
    } }) } });
  });
  await page.goto('/plugins/example.pentest/workspaces?ownerId=local');
  const navigation = page.getByRole('navigation', { name: 'Main navigation' });
  await expect(navigation.getByRole('link', { name: 'Pentest' })).toHaveCount(1);
  await expect(page.getByTestId('header-breadcrumb')).toHaveText('Pentest');
  await page.getByRole('tab', { name: 'Findings' }).click();
  await expect(page).toHaveURL(/\/findings\?ownerId=local$/);
  await expect(navigation.getByRole('link', { name: 'Pentest' })).toHaveAttribute('aria-current', 'page');
  const opener = page.getByRole('button', { name: 'View Missing authorization' });
  await expect(opener).toContainText('auth.go:12');
  await expect(opener).toContainText('2 others');
  const location = page.url();
  await opener.click();
  const drawer = page.getByRole('dialog', { name: 'Finding details' });
  await expect(drawer.getByText('Verify object ownership.')).toBeVisible();
  await expect(page).toHaveURL(location);
  await page.keyboard.press('Escape');
  await expect(drawer).toHaveCount(0);
  await expect(opener).toBeFocused();
});
