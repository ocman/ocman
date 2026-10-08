import { test, expect, MOCK_SESSION, mockSessionWithLiveConnection, mockSse, sseEvent } from './fixtures';

for (const kind of ['permission', 'question']) {
  test(`${kind} prompt scrolls with the conversation`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width: 1280, height: 900 });
    const session = mockSessionWithLiveConnection();
    const now = Date.now();
    await page.route(new RegExp(`/api/session/${session.id}(\\?|$)`), route => route.fulfill({ json: {
      session,
      messages: [
        { id: 'u1', sessionId: session.id, timeCreated: now - 60000, data: { role: 'user' } },
        { id: 'a1', sessionId: session.id, timeCreated: now - 50000, data: { role: 'assistant' } },
      ],
      parts: [
        { id: 'p1', sessionId: session.id, messageId: 'u1', data: { type: 'text', text: 'Fix the login redirect and check the existing authentication tests.' } },
        { id: 'p2', sessionId: session.id, messageId: 'a1', data: { type: 'text', text:
          '## Login redirect\n\nI traced the login flow from the form submission to the authenticated route.\n\n' +
          '### Findings\n\n' + Array.from({ length: 12 }, (_, i) => `${i + 1}. The authentication flow preserves the requested destination and checks the session before redirecting.`).join('\n\n') +
          '\n\nThe redirect change is ready. I need your input before continuing.' } },
      ],
      totalMessages: 2,
    } }));
    await mockSse(page, MOCK_SESSION.id, [sseEvent({
      type: `${kind}.asked`,
      properties: kind === 'permission'
        ? { sessionID: session.id, id: 'permission-preview', permission: 'bash', patterns: ['pnpm test -- auth'], metadata: { command: 'pnpm test -- auth' } }
        : { sessionID: session.id, id: 'question-preview', questions: [{ header: 'Redirect behavior', question: 'Where should users go after signing in?', options: [
          { label: 'Requested page', description: 'Return to the page they originally tried to open.' },
          { label: 'Dashboard', description: 'Always open the dashboard after signing in.' },
        ] }] },
    })]);
    await page.goto(`/session/${session.id}`);
    const viewport = page.getByTestId('conversation-viewport');
    const prompt = kind === 'permission'
      ? viewport.getByRole('button', { name: /allow once/i })
      : viewport.getByRole('dialog', { name: 'Pending question' });
    await expect(prompt).toBeVisible();
    await page.getByRole('tab', { name: 'Session changes', exact: true }).click();
    await viewport.evaluate(el => { el.scrollTop = el.scrollHeight; });
    await page.screenshot({ path: testInfo.outputPath(`${kind}.png`), animations: 'disabled' });
    await viewport.hover();
    await page.mouse.wheel(0, -3000);
    await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBeLessThan(100);
    await expect(prompt).not.toBeInViewport();
    await expect(viewport.getByText('Fix the login redirect and check the existing authentication tests.', { exact: true })).toBeInViewport();
  });
}
