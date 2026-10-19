// @vitest-environment jsdom
import { act, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { renderSessionPage } from './harness';
import type { OceEvent } from '../../../lib/conversation/events';
import userEvent from '@testing-library/user-event';

beforeEach(() => {
  HTMLElement.prototype.scrollTo = vi.fn();
  HTMLElement.prototype.scrollIntoView = vi.fn();
});

describe('conversation prompts', () => {
  const prompt = (kind: 'permission' | 'question', id: string): OceEvent => ({ type: 'prompt.asked', prompt: {
    id, kind, sessionId: 'sess_1', askedAt: 1,
    ...(kind === 'permission'
      ? { permission: { permission: 'bash', patterns: ['pwd'] } }
      : { question: { questions: [{ header: 'Choice', question: 'Which approach?', options: [{ label: 'Small', description: 'Keep it small' }] }] } }),
  } });

  it.each(['permission', 'question'] as const)('keeps the permission picker and YOLO confirmation open during a %s prompt', async (kind) => {
    const user = userEvent.setup();
    const save = vi.fn().mockResolvedValue(undefined);
    const page = renderSessionPage({ sessionId: 'sess_1', realAssistantThread: true, apiOverrides: {
      getPermissionRules: vi.fn().mockResolvedValue({ rules: [] }),
      setPermissionRules: save,
    } });
    await user.click(await screen.findByLabelText('Permission mode: Default'));
    const emitPrompt = (id: string) => act(() => page.sse()!.emitOce(prompt(kind, id)));
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
    ['permission', 'Allow once'],
    ['question', 'Small'],
  ] as const)('places a %s prompt at the bottom of the conversation viewport', async (kind, label) => {
    const page = renderSessionPage({ sessionId: 'sess_1', realAssistantThread: true });
    await screen.findByRole('textbox');
    await waitFor(() => expect(page.sse()).toBeDefined());
    act(() => page.sse()!.emitOce(prompt(kind, `${kind}-1`)));
    const shown = await screen.findByRole(kind === 'question' ? 'radio' : 'button', { name: new RegExp(label, 'i') });
    const viewport = screen.getByTestId('conversation-viewport');
    expect(viewport).toContainElement(shown);
    expect(viewport.lastElementChild).toContainElement(shown);
    expect(screen.getByTestId('conversation-composer')).not.toContainElement(shown);
    expect(within(screen.getByTestId('conversation-composer')).getByRole('textbox')).toBeDisabled();
  });
});
