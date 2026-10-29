import { test, expect, mockSessionWithLiveConnection } from './fixtures';
test.use({ factoryBeta: true });

for (const kind of ['recovery', 'approval']) {
  test(`Factory ${kind} scrolls with the conversation`, async ({ mockedPage: page }) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    const session = { ...mockSessionWithLiveConnection(), factoryAttemptId: 'attempt' };
    const now = Date.now();
    await page.route(new RegExp(`/api/session/${session.id}(\\?|$)`), route => route.fulfill({ json: {
      session,
      messages: [
        { id: 'u1', sessionId: session.id, timeCreated: now - 1000, data: { role: 'user' } },
        { id: 'a1', sessionId: session.id, timeCreated: now, data: { role: 'assistant' } },
      ],
      parts: [
        { id: 'p1', sessionId: session.id, messageId: 'u1', data: { type: 'text', text: 'Inspect the existing conversation before deciding.' } },
        { id: 'p2', sessionId: session.id, messageId: 'a1', data: { type: 'text', text:
          Array.from({ length: 30 }, (_, i) => `Step ${i + 1}: Review the implementation and its tests.`).join('\n\n') } },
      ],
      totalMessages: 2,
    } }));
    const epic = {
      id: 'ship', initialProject: session.directory, status: 'open', goal: 'Ship work',
      attempts: [{ id: 'attempt', workId: 'work', phase: 'active', session: { id: session.id, platform: session.platform } }],
      ...(kind === 'approval' ? { planGate: { issueId: 'gate', resolution: 'open', proposalRevision: 1, proposalHash: 'hash' } } : {}),
    };
    await page.route('/api/factory/epics', route => route.fulfill({ json: [epic] }));
    await page.route('/api/factory/epics/ship', route => route.fulfill({ json: epic }));
    await page.route('/api/factory/epics/ship/issues', route => route.fulfill({ json: kind === 'approval'
      ? [{ id: 'materialize', kind: 'materialization', status: 'open' }]
      : [{ id: 'gate', recovery: { issueId: 'gate', epicId: 'ship', attemptId: 'attempt', workId: 'work', question: 'Which API?', reason: 'Unsafe to guess', choices: ['A', 'B'], resolution: 'open' } }],
    }));
    await page.goto(`/session/${session.id}${kind === 'approval' ? '?factoryEpic=ship' : ''}`);
    const viewport = page.getByTestId('conversation-viewport');
    const panel = viewport.getByLabel(kind === 'approval' ? 'Plan approval' : 'Factory recovery', { exact: true });
    await expect(panel).toBeVisible();
    await expect(page.getByTestId('conversation-composer').getByLabel(kind === 'approval' ? 'Plan approval' : 'Factory recovery', { exact: true })).toHaveCount(0);
    await viewport.evaluate(el => { el.scrollTop = el.scrollHeight; });
    await expect(panel).toBeInViewport();
    await viewport.hover();
    await page.mouse.wheel(0, -10000);
    await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBeLessThan(100);
    await expect(panel).not.toBeInViewport();
    await expect(viewport.getByText('Inspect the existing conversation before deciding.', { exact: true })).toBeInViewport();
  });
}
