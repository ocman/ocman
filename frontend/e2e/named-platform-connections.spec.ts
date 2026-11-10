import { test, expect, MOCK_SESSION, makeCapabilitiesWithPort } from './fixtures';
import type { PlatformConnection } from '../src/lib/platformConnections';

const personal: PlatformConnection = { id: 'cnx_personal', kind: 'opencode', name: 'OpenCode Personal', configuration: 'existing', directory: '/config/personal', enabled: true, isDefault: true, status: 'ready' };
const work: PlatformConnection = { ...personal, id: 'cnx_work', name: 'OpenCode Work', directory: '/config/work', configuration: 'isolated', isDefault: false };
const platform = { id: 'opencode', label: 'OpenCode', existingDirectory: personal.directory, supportsIsolation: true, isolationNote: 'Separate configuration' };

test('mounted Settings resumes the owner receipt after reload and retries without allocating a sibling', async ({ mockedPage: page }) => {
  const checks: Record<string, unknown>[] = [];
  const saves: Record<string, unknown>[] = [];
  let connections: PlatformConnection[] = [personal];
  await page.route('**/api/platform-connections**', async route => {
    const request = route.request();
    expect(new URL(request.url()).searchParams.get('remoteId')).toBe('local');
    if (new URL(request.url()).pathname.endsWith('/check')) {
      checks.push(request.postDataJSON());
      if (checks.length === 1) return route.fulfill({ status: 409, json: { reservationId: 'stp_work', error: 'Setup check failed. Retry with this reservation.' } });
      return route.fulfill({ json: { reservationId: 'stp_work', status: 'ready', directory: work.directory, checks: [] } });
    }
    if (request.method() === 'POST') {
      saves.push(request.postDataJSON());
      connections = [personal, work];
      return route.fulfill({ json: { saved: true } });
    }
    return route.fulfill({ json: { supported: true, onboardingComplete: true, platforms: [platform], connections } });
  });
  await page.goto('/settings?tab=platforms');
  await page.getByRole('button', { name: 'Add platform', exact: true }).click();
  let dialog = page.getByRole('dialog', { name: 'Add platform', exact: true });
  await dialog.getByRole('textbox', { name: 'Name', exact: true }).fill(work.name);
  await dialog.getByRole('combobox', { name: 'Configuration', exact: true }).selectOption('isolated');
  await dialog.getByRole('button', { name: 'Check setup', exact: true }).click();
  await expect(dialog.getByText(/Your reservation is kept/)).toBeVisible();
  await page.reload();
  await page.getByRole('button', { name: 'Resume setup', exact: true }).click();
  dialog = page.getByRole('dialog', { name: 'Add platform', exact: true });
  await expect(dialog.getByRole('textbox', { name: 'Name', exact: true })).toHaveValue(work.name);
  await dialog.getByRole('button', { name: 'Check setup', exact: true }).click();
  await expect(dialog.getByRole('status')).toHaveText('Ready');
  await dialog.getByRole('button', { name: 'Add platform', exact: true }).click();
  await expect(dialog).not.toBeVisible();
  await expect(page.getByRole('button', { name: work.name, exact: true })).toBeVisible();
  expect(checks).toHaveLength(2);
  expect(checks[0]).not.toHaveProperty('reservationId');
  expect(checks[1]).toMatchObject({ reservationId: 'stp_work', configuration: 'isolated', name: work.name });
  expect(saves).toEqual([{ reservationId: 'stp_work', name: work.name, enabled: true }]);
  await page.reload();
  await expect(page.getByRole('button', { name: work.name, exact: true })).toBeVisible();
  await expect(page.getByRole('button', { name: 'Resume setup', exact: true })).toHaveCount(0);
});

test('mounted composer keeps the chosen same-kind connection through model changes and account unavailability', async ({ mockedPage: page }) => {
  let unavailable = false;
  const prepared: string[] = [];
  const capabilities = makeCapabilitiesWithPort();
  capabilities.platforms = [personal, work].map(connection => ({ ...capabilities.platforms[0], id: connection.id, kind: connection.kind, displayName: connection.name }));
  await page.route('**/api/capabilities', route => route.fulfill({ json: capabilities }));
  await page.route('**/api/platform-connections**', route => route.fulfill({ json: { supported: true, onboardingComplete: true, platforms: [platform], connections: [personal, { ...work, status: unavailable ? 'sign-in-required' : 'ready' }] } }));
  await page.route('**/api/sessions/resolve-targets', route => route.fulfill({ json: { candidates: [{ remoteId: 'local', remoteName: 'This machine', platform: personal.id, dir: MOCK_SESSION.directory }], remotes: [] } }));
  await page.route('**/api/git/info*', route => route.fulfill({ json: {} }));
  await page.route('**/api/worktree/list*', route => route.fulfill({ json: { worktrees: [] } }));
  await page.route('**/api/sessions/prepare', route => {
    const selected = route.request().postDataJSON().platform;
    prepared.push(selected);
    if (unavailable && selected === work.id) return route.fulfill({ status: 409, json: { error: 'Selected Work account requires sign-in' } });
    return route.fulfill({ json: { platform: selected, defaultAgent: 'build', defaultModel: 'test/alpha', agents: [{ name: 'build' }], commands: [], liveConnection: true, models: { hasProviders: true, models: ['alpha', 'beta'].map(model => ({ platform: 'opencode', provider: 'test', model, name: model, isAvailable: true })) } } });
  });
  await page.goto(`/session/new?dir=${encodeURIComponent(MOCK_SESSION.directory)}&platform=${personal.id}`);
  const selector = page.getByRole('combobox', { name: 'Platform connection', exact: true });
  await expect(selector).toHaveValue(personal.id);
  await selector.selectOption(work.id);
  await expect(selector).toHaveValue(work.id);
  await expect.poll(() => new URL(page.url()).searchParams.get('platform')).toBe(work.id);
  await page.getByRole('button', { name: 'alpha', exact: true }).click();
  await page.getByRole('option', { name: /beta/ }).click();
  await expect(page.getByRole('button', { name: 'beta', exact: true })).toBeVisible();
  await expect(selector).toHaveValue(work.id);
  expect(new URL(page.url()).searchParams.get('platform')).toBe(work.id);
  unavailable = true;
  await page.reload();
  await expect(selector).toHaveValue(work.id);
  await expect(selector.getByRole('option', { name: /Work.*setup required/ })).toHaveAttribute('disabled', '');
  await expect(selector.getByRole('option', { name: personal.name, exact: true })).not.toHaveAttribute('disabled');
  await expect(page.getByRole('alert').filter({ hasText: 'Selected Work account requires sign-in' }).first()).toBeVisible();
  expect(new URL(page.url()).searchParams.get('platform')).toBe(work.id);
  expect(prepared.at(-1)).toBe(work.id);
});
