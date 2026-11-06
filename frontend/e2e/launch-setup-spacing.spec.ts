import { test, expect, mockSessionWithLiveConnection } from './fixtures';

test('completed session setup stays close to the conversation', async ({ mockedPage: page }) => {
  const session = mockSessionWithLiveConnection();
  const now = Date.now();
  const draft = {
    id: session.id, metadata: { draftId: session.id, directory: session.directory },
    phase: 'ready', createdAt: now, submittedAt: now,
    request: { platform: session.platform, prompt: 'First prompt' },
    steps: { platform: 'done', session: 'done', prompt: 'done' },
  };
  await page.route('**/api/session-drafts**', route => route.fulfill({ json:
    new URL(route.request().url()).pathname === '/api/session-drafts' ? [draft] : draft,
  }));
  await page.route(new RegExp(`/api/session/${session.id}(\\?|$)`), route => route.fulfill({ json: {
    session,
    messages: [
      { id: 'u1', sessionId: session.id, timeCreated: now, data: { role: 'user' } },
      { id: 'a1', sessionId: session.id, timeCreated: now + 1, data: { role: 'assistant' } },
    ],
    parts: [
      { id: 'p1', sessionId: session.id, messageId: 'u1', data: { type: 'text', text: 'First prompt' } },
      { id: 'p2', sessionId: session.id, messageId: 'a1', data: { type: 'text', text: 'Conversation begins here.' } },
    ],
    totalMessages: 2,
  } }));
  await page.goto(`/session/${session.id}`);
  const setup = page.getByText(/^Setup/);
  const response = page.getByText('Conversation begins here.', { exact: true });
  await expect(setup).toBeVisible();
  await expect(response).toBeVisible();
  const gap = await response.evaluate((element) => {
    const setup = document.querySelector('.oc-launch-setup')!;
    const message = element.closest('.oc-msg')!;
    return message.getBoundingClientRect().top - setup.getBoundingClientRect().bottom;
  });
  expect(gap).toBeLessThanOrEqual(12);
  await expect(setup).toHaveText('Setup ocman session');
  await expect(setup.locator('..')).not.toHaveAttribute('open');
  await setup.click();
  await expect(page.getByTestId('start-step-session')).toBeVisible();
});
