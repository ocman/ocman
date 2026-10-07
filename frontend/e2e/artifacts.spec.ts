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
