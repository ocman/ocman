import { test, expect, MOCK_SESSION } from './fixtures';

for (const tool of ['read', 'bash']) {
  test(`${tool} following assistant text stays flush with the next muted row`, async ({ mockedPage: page }) => {
    await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
      state: { compactToolCalls: true, changesSidebarOpenTabs: [] }, version: 7,
    })));
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: MOCK_SESSION,
      messages: [0, 1].map(i => ({ id: `a${i}`, sessionId: MOCK_SESSION.id, timeCreated: 2000 + i, data: {
        role: 'assistant', finish: 'tool-calls', time: { created: 2000 + i, completed: 2001 + i },
      } })),
      parts: [
        { id: 'text', messageId: 'a0', sessionId: MOCK_SESSION.id, data: { type: 'text', text: 'Checking the relevant files.' } },
        ...[0, 1].map(i => ({ id: `p${i}`, messageId: `a${i}`, sessionId: MOCK_SESSION.id, data: {
          type: 'tool', tool, callID: `call${i}`, state: {
            status: 'completed', input: tool === 'read' ? { filePath: `/example-${i}.ts` }
              : { command: `echo command-${i}\necho second-line\necho third-line` }, output: 'Done',
          },
        } })),
      ],
      totalMessages: 2,
    } }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const viewport = page.getByTestId('conversation-viewport');
    const summaries = tool === 'read' ? viewport.getByText(/^Explored:/) : viewport.getByRole('button', { name: /^\$ echo command-/ });
    await expect(summaries).toHaveCount(2);
    const first = summaries.nth(0);
    const second = summaries.nth(1);
    const firstBounds = (await first.boundingBox())!;
    const secondBounds = (await second.boundingBox())!;
    expect(secondBounds.y - firstBounds.y).toBeCloseTo(firstBounds.height, 0);

    if (tool === 'bash') return;
    await first.click();
    const child = viewport.getByText('Read /example-0.ts', { exact: true });
    await expect(child).toBeVisible();
    const childBounds = (await child.boundingBox())!;
    const expandedNext = (await second.boundingBox())!;
    expect(expandedNext.y - childBounds.y).toBeCloseTo(childBounds.height, 0);
  });
}
