// @vitest-environment jsdom
import { act, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderSessionPage } from './harness';

beforeEach(() => { HTMLElement.prototype.scrollTo = vi.fn(); });

describe('conversation prompts', () => {
  it.each([
    ['permission.asked', { id: 'permission-1', permission: 'bash', patterns: ['pwd'] }, 'Allow once'],
    ['question.asked', { id: 'question-1', questions: [{ header: 'Choice', question: 'Which approach?', options: [{ label: 'Small', description: 'Keep it small' }] }] }, 'Small'],
  ])('places %s at the bottom of the conversation viewport', async (type, properties, label) => {
    const page = renderSessionPage({ sessionId: 'sess_1', realAssistantThread: true });
    await screen.findByRole('textbox');
    await waitFor(() => expect(page.sse()).toBeDefined());
    act(() => page.sse()!.emitMessage({ type, properties: { ...properties, sessionID: 'sess_1' } }));
    const prompt = await screen.findByRole(type === 'question.asked' ? 'radio' : 'button', { name: new RegExp(label, 'i') });
    const viewport = screen.getByTestId('conversation-viewport');
    expect(viewport).toContainElement(prompt);
    expect(viewport.lastElementChild).toContainElement(prompt);
    expect(screen.getByTestId('conversation-composer')).not.toContainElement(prompt);
    expect(within(screen.getByTestId('conversation-composer')).queryByRole('textbox')).toBeNull();
  });
});
