import { test, expect, MOCK_SESSION } from './fixtures';

test('background completion notices do not split the turn metadata', async ({ mockedPage: page }) => {
  const sessionId = MOCK_SESSION.id;
  const messages = [
    { id: 'u', sessionId, timeCreated: 1000, data: { role: 'user' } },
    { id: 'a1', sessionId, timeCreated: 2000, data: { role: 'assistant', finish: 'stop' } },
    { id: 'notification', sessionId, timeCreated: 3000, data: { role: 'user', synthetic: true } },
    { id: 'a2', sessionId, timeCreated: 4000, data: { role: 'assistant', finish: 'stop' } },
  ];
  const parts = [
    { messageId: 'u', text: 'Run the checks.' },
    { messageId: 'a1', text: 'The checks are running.' },
    { messageId: 'notification', text: '<shell id="sh1" state="completed" command="make lint">\nPassed\n</shell>' },
    { messageId: 'a2', text: 'All checks passed.' },
  ].map(({ messageId, text }) => ({ id: `p-${messageId}`, messageId, sessionId, data: { type: 'text', text } }));
  await page.route(new RegExp(`/api/session/${sessionId}(\\?|$)`), route => route.fulfill({ json: {
    session: MOCK_SESSION, messages, parts, totalMessages: messages.length,
  } }));
  await page.goto(`/session/${sessionId}`);
  const viewport = page.getByTestId('conversation-viewport');
  await expect(viewport.getByText('The checks are running.', { exact: true })).toBeVisible();
  await expect(viewport.getByText('All checks passed.', { exact: true })).toBeVisible();
  await expect(viewport.getByText('make lint', { exact: false })).toBeVisible();
  await expect(viewport.getByRole('group', { name: 'Turn actions' })).toHaveCount(1);
});
