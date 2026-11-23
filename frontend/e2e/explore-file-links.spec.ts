import { test, expect, MOCK_SESSION } from './fixtures';

test('session file links open Explore on the session host', async ({ mockedPage: page }) => {
  const root = '/remote/worktree';
  const path = 'docs/Review notes.md';
  const session = { ...MOCK_SESSION, directory: `${root}/docs`, remoteId: 'remote-box' };
  await page.route(new RegExp(`/api/session/${session.id}(\\?|$)`), route => route.fulfill({ json: {
    session,
    messages: [{ id: 'a1', sessionId: session.id, timeCreated: 2000, data: { role: 'assistant', finish: 'stop' } }],
    parts: [{ id: 'p1', sessionId: session.id, messageId: 'a1', data: { type: 'text', text:
      '[Review notes](ocman://explore/file/docs/Review%20notes.md) [Other file](ocman://explore/file/docs/not-listed.md)',
    } }],
    totalMessages: 1,
  } }));
  await page.route('/api/git/files?*', route => route.fulfill({ json: { root, files: [path] } }));
  const opened: string[] = [];
  await page.route('/api/git/file?*', route => {
    const params = new URL(route.request().url()).searchParams;
    expect(params.get('remoteId')).toBe(session.remoteId);
    expect(params.get('dir')).toBe(session.directory);
    expect(params.has('ignored')).toBe(false);
    opened.push(params.get('path') ?? '');
    return route.fulfill({ json: { path, content: '# Review results', size: 16, binary: false } });
  });

  await page.goto(`/session/${session.id}`);
  const dialog = page.getByRole('dialog', { name: 'Explore', exact: true });
  await page.getByRole('link', { name: 'Review notes' }).click();
  expect(new URL(page.url()).searchParams.get('exploreFile')).toBe(path);
  await expect(dialog.getByRole('heading', { name: 'Review results' })).toBeVisible();
  await expect(dialog.getByRole('treeitem', { name: 'Review notes.md' })).toBeVisible();
  expect(opened).toEqual([path]);
  await dialog.getByRole('button', { name: 'Close', exact: true }).click();
  await expect(dialog).toBeHidden();
  await page.getByRole('link', { name: 'Other file' }).click();
  await expect(dialog.getByText('The linked file is not listed in this checkout.')).toBeVisible();
  expect(opened).toEqual([path]);
  await page.goto(`/session/${session.id}?exploreFile=${encodeURIComponent(`${root}/${path}`)}`);
  await expect(page.getByRole('link', { name: 'Review notes' })).toBeVisible();
  await expect(dialog).toBeHidden();
  expect(opened).toEqual([path]);
});
