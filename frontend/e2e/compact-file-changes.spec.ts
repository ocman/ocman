import { test, expect, MOCK_SESSION } from './fixtures';

test('compact patches and writes list relative paths below the file count', async ({ mockedPage: page }, testInfo) => {
  await page.setViewportSize({ width: 1280, height: 800 });
  await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
    state: { compactToolCalls: true, changesSidebarOpenTabs: [] }, version: 7,
  })));
  const directory = MOCK_SESSION.directory;
  const tools = [
    { tool: 'apply_patch', input: { patchText: `*** Begin Patch\n*** Update File: ${directory}/relative/path.js\n@@\n-old\n+new\n*** Add File: ${directory}/other/file.go\n+package main\n*** End Patch` } },
    { tool: 'write', input: { filePath: `${directory}/src/new.ts`, content: 'export const answer = 42;' } },
  ];
  await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
    session: MOCK_SESSION,
    messages: [{ id: 'a1', sessionId: MOCK_SESSION.id, timeCreated: 1000, data: {
      role: 'assistant', finish: 'stop', time: { created: 1000, completed: 4000 },
    } }],
    parts: tools.map(({ tool, input }, i) => ({
      id: `p${i}`, messageId: 'a1', sessionId: MOCK_SESSION.id, data: {
        type: 'tool', tool, callID: `file-${i}`,
        state: { status: 'completed', input, output: 'Done', time: { start: 1000, end: 2500 } },
      },
    })), totalMessages: 1,
  } }));
  await page.goto(`/session/${MOCK_SESSION.id}`);
  const viewport = page.getByTestId('conversation-viewport');
  const patch = viewport.getByRole('button', { name: /Patching 2 files.*relative\/path.js.*other\/file.go/ });
  const write = viewport.getByRole('button', { name: /Writing 1 file.*src\/new.ts/ });
  await expect(patch).toBeVisible();
  await expect(write).toBeVisible();
  await expect(patch).toHaveAttribute('aria-expanded', 'false');
  const first = patch.getByText('relative/path.js', { exact: true });
  const second = patch.getByText('other/file.go', { exact: true });
  expect((await first.boundingBox())!.y).toBeGreaterThan((await patch.getByText('Patching 2 files', { exact: true }).boundingBox())!.y);
  expect((await second.boundingBox())!.y).toBeGreaterThan((await first.boundingBox())!.y);
  const bounds = (await viewport.boundingBox())!;
  const top = (await patch.boundingBox())!.y - 16;
  const bottom = (await write.boundingBox())!;
  await page.screenshot({ path: testInfo.outputPath('compact-file-changes.png'), animations: 'disabled',
    clip: { x: bounds.x, y: top, width: bounds.width, height: bottom.y + bottom.height - top + 24 },
  });
  await patch.click();
  await expect(patch).toHaveAttribute('aria-expanded', 'true');
});
