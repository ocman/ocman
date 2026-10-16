import { test, expect, MOCK_SESSION } from './fixtures';

for (const hidden of [true, false]) {
  test(`loads earlier pages while the oldest message is visible, hidden=${hidden}`, async ({ mockedPage: page }) => {
    const messages = Array.from({ length: 61 }, (_, index) => ({
      id: `history-${index}`,
      sessionId: MOCK_SESSION.id,
      timeCreated: MOCK_SESSION.timeCreated + index * 1000,
      data: {
        role: index === 0 ? 'user' : 'assistant',
        finish: index === 0 ? undefined : 'stop',
        time: { created: MOCK_SESSION.timeCreated + index * 1000, completed: MOCK_SESSION.timeCreated + index * 1000 + 100 },
      },
    }));
    const offsets: number[] = [];
    await page.route(new RegExp(`/api/session/${MOCK_SESSION.id}(\\?|$)`), (route) => {
      const params = new URL(route.request().url()).searchParams;
      const offset = Number(params.get('offset') || 0);
      offsets.push(offset);
      const batch = messages.slice(Math.max(0, messages.length - offset - 30), messages.length - offset);
      return route.fulfill({ json: {
        session: { ...MOCK_SESSION, messageCount: messages.length },
        messages: batch,
        parts: batch.flatMap((message) => hidden && message.data.role !== 'user' ? [] : [{
          id: `part-${message.id}`,
          messageId: message.id,
          sessionId: MOCK_SESSION.id,
          data: { type: 'text', text: message.data.role === 'user' ? 'The original user prompt' : 'A short reply' },
        }]),
        totalMessages: messages.length,
      } });
    });

    await page.goto(`/session/${MOCK_SESSION.id}`);
    const viewport = page.getByTestId('conversation-viewport');
    if (!hidden) {
      await expect(viewport.getByText('A short reply').first()).toBeAttached();
      // The tail starts scrollable, so it must not fetch pages until the
      // oldest message is revealed by a deliberate scroll to the top.
      await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBeGreaterThan(0);
      expect(offsets.filter(offset => offset > 0)).toEqual([]);
      await viewport.evaluate(el => { el.scrollTop = 0; });
      await expect(viewport.getByText('A short reply')).toHaveCount(60);
      await expect.poll(() => viewport.evaluate(el => el.scrollTop)).toBeGreaterThan(0);
      await viewport.evaluate(el => { el.scrollTop = 0; });
    }
    await expect(viewport.getByText('The original user prompt')).toBeVisible();
    expect(offsets.filter(offset => offset > 0)).toEqual([30, 60]);
  });
}
