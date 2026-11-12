import { test, expect, MOCK_SESSION } from './fixtures';

const questions = [
  {
    question: 'What should /api/gateway/health and /health/{module} do?', header: 'Health checks',
    options: [
      { label: 'Always deep', description: 'Always check database and downstream dependencies. Keep /healthz process-only.' },
      { label: 'Shallow', description: 'Only check the process.' },
    ],
  },
  {
    question: 'How should DB unavailability become a 503?', header: 'Error handling',
    options: [{ label: 'Translate at source + central handler', description: 'Raise a typed availability error near the database and map it to HTTP 503 in one shared handler.' }],
  },
  {
    question: '503 response shape?', header: 'Response',
    options: [{ label: 'Gateway shape + Retry-After', description: 'Keep the existing gateway error body and include a Retry-After header for clients.' }],
  },
];

for (const width of [1280, 390]) {
  test(`answered question cards show descriptions without overflow at ${width}px`, async ({ mockedPage: page }, testInfo) => {
    await page.setViewportSize({ width, height: 1000 });
    await page.addInitScript(() => localStorage.setItem('ocman:ui', JSON.stringify({
      state: { changesSidebarOpenTabs: [] }, version: 7,
    })));
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), route => route.fulfill({ json: {
      session: { ...MOCK_SESSION, title: 'Health check behavior' },
      messages: [{ id: 'assistant', sessionId: MOCK_SESSION.id, timeCreated: 2000, data: {
        role: 'assistant', finish: 'tool-calls', time: { created: 2000, completed: 3000 },
      } }],
      parts: [
        { id: 'intro', messageId: 'assistant', sessionId: MOCK_SESSION.id,
          data: { type: 'text', text: 'Before I implement this, a few decisions:' } },
        { id: 'questions', messageId: 'assistant', sessionId: MOCK_SESSION.id, data: {
          type: 'tool', tool: 'question', callID: 'question-call', state: {
            status: 'completed', input: { questions },
            output: `User has answered your questions: ${questions.map(q => `${JSON.stringify(q.question)}=${JSON.stringify(q.options[0].label)}`).join(', ')}. You can now continue with the user's answers in mind.`,
            time: { start: 2000, end: 3000 },
          },
        } },
      ],
      totalMessages: 1,
    } }));
    await page.goto(`/session/${MOCK_SESSION.id}`);
    const answered = page.getByRole('region', { name: 'Answered questions' });
    await expect(answered).toBeVisible();
    await expect(answered.getByRole('article')).toHaveCount(3);
    for (const question of questions) {
      const card = answered.getByRole('article', { name: question.question });
      await expect(card.getByText(question.options[0].label, { exact: true })).toBeVisible();
      await expect(card.getByText(question.options[0].description)).toBeVisible();
      expect(await card.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
    }
    await expect(answered.getByText('Only check the process.')).toHaveCount(0);
    await expect(answered.getByRole('button')).toHaveCount(0);
    await answered.screenshot({ path: testInfo.outputPath(`answered-questions-${width}.png`), animations: 'disabled' });
    await page.screenshot({ path: testInfo.outputPath(`conversation-${width}.png`), fullPage: true, animations: 'disabled' });
  });
}
