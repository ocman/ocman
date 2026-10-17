import { test, expect, MOCK_SESSION, mockSessionWithLiveConnection } from './fixtures';

for (const width of [390, 1280]) {
  for (const kind of ['question', 'permission'] as const) {
    test(`recovers an already-pending ${kind} without waiting for the session list at ${width}px`, async ({ mockedPage: page }) => {
      await page.setViewportSize({ width, height: 844 });
      const session = mockSessionWithLiveConnection();
      const questions = [{ header: 'Approach', question: 'Which approach should I use?', options: [
        { label: 'Small change', description: 'Keep the existing structure.' },
        { label: 'Refactor', description: 'Extract a reusable component.' },
      ] }];
      const request = kind === 'question'
        ? { id: 'pending-question', sessionID: session.id, questions }
        : { id: 'pending-permission', sessionID: session.id, permission: 'bash', patterns: ['pnpm test'], metadata: { command: 'pnpm test' } };
      let releaseSessions!: () => void;
      const sessionListReady = new Promise<void>(resolve => { releaseSessions = resolve; });
      await page.route('/api/sessions*', async route => {
        await sessionListReady;
        return route.fulfill({ json: [{ ...session,
          pendingQuestion: kind === 'question', pendingPermission: kind === 'permission',
        }] });
      });
      // The historical snapshot contains the running tool, not the live prompt.
      // The question was already asked before this browser connected to SSE.
      await page.route(new RegExp(`/api/session/${session.id}(\\?|$)`), route => route.fulfill({ json: {
        session, totalMessages: 2,
        messages: [
          { id: 'user', sessionId: session.id, timeCreated: Date.now() - 2000, data: { role: 'user' } },
          { id: 'assistant', sessionId: session.id, timeCreated: Date.now() - 1000, data: { role: 'assistant', finish: 'tool-calls' } },
        ],
        parts: [
          { id: 'text', sessionId: session.id, messageId: 'user', data: { type: 'text', text: 'Make the mobile layout usable.' } },
          { id: 'tool', sessionId: session.id, messageId: 'assistant', data: {
            type: 'tool', tool: kind === 'question' ? 'question' : 'bash', callID: 'pending-call',
            state: { status: 'running', input: kind === 'question' ? { questions } : { command: 'pnpm test' }, time: { start: Date.now() - 1000 } },
          } },
        ],
      } }));
      await page.route(`/api/session/${session.id}/${kind === 'question' ? 'questions' : 'permissions'}`, route => route.fulfill({ json: [request] }));
      const endpoint = `/api/session/${session.id}/${kind === 'question' ? 'questions/pending-question' : 'permissions/pending-permission'}`;
      await page.route(endpoint, route => route.fulfill({ json: { ok: true } }));
      await page.goto(`/session/${MOCK_SESSION.id}`);
      if (kind === 'question') await expect(page.getByText('Asked 1 question', { exact: true })).toBeVisible();
      const prompt = page.getByRole('dialog', { name: kind === 'question' ? 'Pending question' : 'Permission required', exact: true });
      try {
        // The choices must render before the slow desktop sidebar finishes loading.
        await expect(prompt).toBeVisible();
      } finally { releaseSessions(); }
      await expect(prompt).toBeInViewport();
      const reply = page.waitForRequest(request => request.method() === 'POST' && request.url().includes(kind === 'question' ? '/questions/' : '/permissions/'));
      if (kind === 'question') {
        await prompt.getByRole('radio', { name: /Small change/ }).click();
      } else {
        await prompt.getByRole('button', { name: 'Allow once', exact: true }).click();
      }
      expect((await reply).postDataJSON()).toMatchObject(kind === 'question' ? { answers: [['Small change']] } : { reply: 'once' });
      await expect(prompt).toHaveCount(0);
    });
  }
}
