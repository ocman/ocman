// @vitest-environment jsdom
import { act, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderSessionPage } from './harness';
import userEvent from '@testing-library/user-event';

beforeEach(() => {
  HTMLElement.prototype.scrollTo = vi.fn();
  HTMLElement.prototype.scrollIntoView = vi.fn();
});

describe('conversation prompts', () => {
  it.each(['permission.asked', 'question.asked'])('keeps the permission picker and YOLO confirmation open during %s', async (type) => {
    const user = userEvent.setup();
    const save = vi.fn().mockResolvedValue(undefined);
    const page = renderSessionPage({ sessionId: 'sess_1', realAssistantThread: true, apiOverrides: {
      getPermissionRules: vi.fn().mockResolvedValue({ rules: [] }),
      setPermissionRules: save,
    } });
    await user.click(await screen.findByLabelText('Permission mode: Default'));
    const emitPrompt = (id: string) => act(() => page.sse()!.emitMessage({ type, properties: {
      id, sessionID: 'sess_1', permission: 'bash', patterns: ['pwd'],
      questions: [{ header: 'Choice', question: 'Which approach?', options: [{ label: 'Small', description: 'Keep it small' }] }],
    } }));
    emitPrompt('first');
    expect(screen.getByRole('dialog', { name: 'Permission mode...' })).toBeInTheDocument();
    await user.click(screen.getByRole('option', { name: /YOLO/ }));
    emitPrompt('second');
    expect(screen.getByRole('button', { name: 'Confirm' })).toHaveFocus();
    await user.click(screen.getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(save).toHaveBeenCalledWith('sess_1', [{ permission: '*', pattern: '*', action: 'allow' }]));
    expect(within(screen.getByTestId('conversation-composer')).getByRole('textbox')).toBeDisabled();
  });

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
    expect(within(screen.getByTestId('conversation-composer')).getByRole('textbox')).toBeDisabled();
  });
});
