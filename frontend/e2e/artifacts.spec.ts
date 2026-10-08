import { test, expect } from './fixtures';

const artifact = {
  id: 'art-1', title: 'Build report', description: 'Nightly **build**', directory: '/home/user/projects/myapp', remoteId: 'local',
  createdAt: '2026-09-01T10:00:00Z',
  items: [
    { kind: 'file', name: 'log.txt', mime: 'text/plain', size: 11, url: '/api/artifacts/art-1/files/0' },
    { kind: 'link', url: 'https://ci.example/run/1', label: 'CI run' },
  ],
};

test('browses, previews and deletes an artifact', async ({ mockedPage: page }) => {
  let deleted = false;
  await page.route('/api/artifacts/stats', (route) => route.fulfill({ json: { count: 1, totalBytes: 11 } }));
  await page.route(/\/api\/artifacts(\?.*)?$/, (route) => route.fulfill({ json: { artifacts: deleted ? [] : [artifact], nextCursor: '' } }));
  await page.route('/api/artifacts/art-1', (route) => {
    if (route.request().method() === 'DELETE') { deleted = true; return route.fulfill({ status: 204 }); }
    return route.fulfill({ json: artifact });
  });
  await page.route('/api/artifacts/art-1/files/0', (route) => route.fulfill({ contentType: 'text/plain', body: 'build ok 42' }));

  await page.goto('/artifacts');
  await expect(page.getByTestId('artifact-stats')).toHaveText('1 artifacts · 11 B stored');
  await page.getByRole('link', { name: 'Build report' }).click();
  await expect(page.getByRole('heading', { name: 'Build report' })).toBeVisible();
  await expect(page.getByTestId('artifact-preview-text')).toHaveText('build ok 42');
  await expect(page.getByRole('link', { name: 'CI run' })).toHaveAttribute('href', 'https://ci.example/run/1');

  page.once('dialog', (dialog) => void dialog.accept());
  await page.getByRole('button', { name: 'Delete' }).click();
  await expect(page).toHaveURL(/\/artifacts$/);
  await expect(page.getByText('No artifacts yet.')).toBeVisible();
});

for (const width of [1280, 390]) {
  test(`artifact sharing owns alerts and link controls at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    const shared = { ...artifact, items: [...artifact.items, { kind: 'link', url: 'https://ci.example/releases/long-build-reference-with-deployment-notes-and-follow-up-checks', label: 'Deployment notes' }] };
    const old = { id: 'old', url: 'https://relay.example/v/old-report#key=example-only', createdAt: 1 };
    const created = { id: 'new', url: 'https://relay.example/v/new-report#key=example-only', createdAt: 2 };
    let attempts = 0;
    let revoked = false;
    await page.route('/api/artifacts/art-1', route => route.fulfill({ json: shared }));
    await page.route('/api/artifacts/art-1/files/0', route => route.fulfill({ contentType: 'text/plain', body: 'Build successful.' }));
    await page.route('/api/artifacts/art-1/shares', route => route.fulfill({ json: { relayConfigured: true, maxShareBytes: 32 * 1024 * 1024, shares: [old] } }));
    await page.route('/api/artifacts/art-1/share', route => {
      attempts++;
      if (attempts === 1) return route.fulfill({ status: 503, contentType: 'text/plain', body: 'Share relay temporarily unavailable.' });
      return route.fulfill({ json: created });
    });
    await page.route('/api/artifacts/art-1/share/old', route => { revoked = true; return route.fulfill({ status: 204 }); });
    await page.goto('/artifacts/art-1');
    await page.getByRole('button', { name: 'Share', exact: true }).click();
    const dialog = page.getByRole('dialog', { name: 'Share artifact', exact: true });
    await expect(dialog.getByRole('textbox', { name: 'Share link', exact: true })).toHaveValue(old.url);
    expect(await dialog.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    await dialog.getByRole('button', { name: 'Create share link', exact: true }).click();
    await expect(dialog.getByRole('alert')).toContainText('Share relay temporarily unavailable');
    await dialog.getByRole('button', { name: 'Create share link', exact: true }).click();
    await expect(dialog.getByRole('textbox', { name: 'Share link', exact: true })).toHaveCount(2);
    await expect(dialog.getByRole('textbox', { name: 'Share link', exact: true }).first()).toHaveValue(created.url);
    await expect(dialog.getByRole('alert')).toHaveCount(0);
    await dialog.getByRole('button', { name: 'Revoke', exact: true }).nth(1).click();
    await expect(dialog.getByRole('textbox', { name: 'Share link', exact: true })).toHaveCount(1);
    expect(revoked).toBe(true);
    await dialog.getByRole('button', { name: 'Close share dialog', exact: true }).click();
    await expect(dialog).toHaveCount(0);
    await expect(page.getByRole('button', { name: 'Share', exact: true })).toBeFocused();
  });

  test(`artifact list filters, retry and table fit at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    let failed = true;
    const queries: URLSearchParams[] = [];
    const release = { ...artifact, id: 'release', title: 'Release report with deployment checks and follow-up notes for the next build' };
    await page.route('/api/artifacts/stats', route => route.fulfill({ json: { count: 2, totalBytes: 22 } }));
    await page.route(/\/api\/artifacts(\?.*)?$/, route => {
      const query = new URL(route.request().url()).searchParams;
      queries.push(query);
      if (failed) return route.fulfill({ status: 503, contentType: 'text/plain', body: 'Could not load artifact list.' });
      return route.fulfill({ json: { artifacts: query.get('q') ? [release] : [artifact, release], nextCursor: '' } });
    });
    await page.goto('/artifacts');
    await expect(page.getByRole('alert')).toContainText('Could not load artifact list');
    await expect(page.getByText('No artifacts yet.')).toHaveCount(0);
    failed = false;
    await page.getByRole('button', { name: 'Retry', exact: true }).click();
    await expect(page.getByRole('link', { name: 'Build report', exact: true })).toBeVisible();
    const project = page.getByRole('combobox', { name: 'Project', exact: true });
    const search = page.getByRole('searchbox', { name: 'Search artifacts', exact: true });
    if (width === 390) {
      expect((await project.boundingBox())!.width).toBeGreaterThan(340);
      expect((await search.boundingBox())!.width).toBeGreaterThan(340);
      expect((await page.getByRole('cell', { name: release.title, exact: true }).boundingBox())!.width).toBeGreaterThanOrEqual(240);
    }
    expect(await page.getByRole('main').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    await project.selectOption(artifact.directory);
    await expect.poll(() => queries.at(-1)!.get('directory')).toBe(artifact.directory);
    await search.fill('release');
    await expect.poll(() => queries.at(-1)!.get('q')).toBe('release');
    await expect(page.getByRole('link', { name: 'Build report', exact: true })).toHaveCount(0);
    await expect(page.getByRole('link', { name: release.title, exact: true })).toBeVisible();
    expect(queries.at(-1)!.get('directory')).toBe(artifact.directory);
  });

  test(`artifact content owns responsive previews and file actions at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    const filename = 'release-build-output-with-a-long-file-name-and-deployment-check-results.txt';
    await page.route('/api/artifacts/art-1', route => route.fulfill({ json: { ...artifact, items: [
      { kind: 'file', name: filename, mime: 'text/plain', size: 600, url: '/api/artifacts/art-1/files/0' },
      { kind: 'file', name: 'preview.html', mime: 'text/html', size: 60, url: '/api/artifacts/art-1/files/1' },
      artifact.items[1],
    ] } }));
    await page.route('/api/artifacts/art-1/files/0', route => route.fulfill({ contentType: 'text/plain', body: 'Deployment check passed. '.repeat(30) }));
    await page.route('/api/artifacts/art-1/files/1', route => route.fulfill({ contentType: 'text/html', body: '<h1>Deployment summary</h1><p>All checks passed.</p>' }));
    await page.goto('/artifacts/art-1');
    await expect(page.getByRole('heading', { name: 'Build report' })).toBeVisible();
    const file = page.getByTestId('artifact-file').first();
    await expect(file.getByRole('link', { name: `Download ${filename}`, exact: true })).toHaveAttribute('href', '/api/artifacts/art-1/files/0?download=1');
    await expect(file.getByTestId('artifact-preview-text')).toContainText('Deployment check passed');
    await expect(page.getByTestId('artifact-preview-html')).toHaveAttribute('sandbox', '');
    expect(await page.getByRole('main').evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    expect(await file.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
  });
}
