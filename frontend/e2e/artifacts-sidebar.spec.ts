import { test, expect, MOCK_SESSION } from './fixtures';

const title = 'Build report with deployment checks, dependency updates and follow-up notes for the next release';
const artifact = {
  id: 'sidebar-report', title, directory: MOCK_SESSION.directory, remoteId: 'local', createdAt: '2026-09-01T10:00:00Z',
  items: [
    { kind: 'file', name: 'build-log.txt', mime: 'text/plain', size: 20, url: '/api/artifacts/sidebar-report/files/0' },
    { kind: 'link', url: 'https://ci.example/builds/this-is-a-long-build-reference-with-detailed-release-notes', label: 'Build and release notes' },
  ],
};

for (const width of [1280, 390]) {
  test(`artifact sidebar wraps titles and supports retry, previews and scope at ${width}px`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width, height: 844 });
    await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({ version: 6, state: {
      changesSidebarOpenTabs: ['artifacts'], artifactsSidebarScope: 'session',
    } })));
    let failed = true;
    const queries: URLSearchParams[] = [];
    await page.route(/\/api\/artifacts(\?.*)?$/, route => {
      const query = new URL(route.request().url()).searchParams;
      queries.push(query);
      if (failed) return route.fulfill({ status: 503, contentType: 'text/plain', body: 'Artifact list unavailable. Retry this session.' });
      const more = query.has('cursor');
      return route.fulfill({ json: { artifacts: [more ? { ...artifact, id: 'second-report', title: 'Follow-up report' } : artifact], nextCursor: more ? '' : 'next-page' } });
    });
    await page.route('/api/artifacts/sidebar-report/files/0', route => route.fulfill({ contentType: 'text/plain', body: 'Build successful. All deployment checks passed.' }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    if (width === 390) await page.getByRole('button', { name: 'Open session details', exact: true }).click();
    const pane = page.getByTestId('artifacts-pane');
    await expect(pane.getByRole('alert')).toContainText('Artifact list unavailable');
    await expect(pane.getByText('No artifacts yet.')).toHaveCount(0);
    failed = false;
    await pane.getByRole('button', { name: 'Retry', exact: true }).click();
    const row = pane.getByRole('button', { name: new RegExp(title) });
    await expect(row).toBeVisible();
    expect(queries.at(-1)!.get('sessionId')).toBe(MOCK_SESSION.id);
    expect(queries.at(-1)!.get('includeDescendants')).toBe('1');
    await expect(row).toHaveAttribute('aria-expanded', 'false');
    expect(await row.evaluate(el => el.scrollHeight <= el.clientHeight)).toBe(true);
    await row.click();
    await expect(row).toHaveAttribute('aria-expanded', 'true');
    await expect(pane.getByTestId('artifact-preview-text')).toContainText('Build successful');
    await expect(pane.getByRole('link', { name: 'Open artifact', exact: true })).toHaveAttribute('href', '/artifacts/sidebar-report');
    await expect(pane.getByRole('link', { name: 'Build and release notes' })).toHaveAttribute('target', '_blank');
    expect(await pane.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    await pane.getByRole('radio', { name: 'This project' }).check();
    await expect.poll(() => queries.at(-1)!.get('directory')).toBe(MOCK_SESSION.directory);
    await pane.getByRole('button', { name: 'Load more', exact: true }).click();
    await expect(pane.getByRole('button', { name: /Follow-up report/ })).toBeVisible();
    await expect(pane.getByRole('button', { name: 'Load more', exact: true })).toHaveCount(0);
  });
}
