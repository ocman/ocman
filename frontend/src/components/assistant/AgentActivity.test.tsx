// @vitest-environment jsdom

import { act, fireEvent, render, screen } from '@testing-library/react';
import { useEffect } from 'react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import type { Message, Part, SessionStatus } from '../../lib/api';
import { useUiStore } from '../../lib/uiStore';
import { usePendingSend } from '../../pages/session-detail/usePendingSend';
import { AssistantThread } from '../AssistantThread';
import { OcmanRuntimeProvider } from '../OcmanRuntimeProvider';

beforeEach(() => {
  vi.stubGlobal('speechSynthesis', { cancel: vi.fn() });
  vi.stubGlobal('SpeechSynthesisUtterance', class {});
});

afterEach(() => {
  useUiStore.setState({ collapseAgentActivity: false });
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

const user: Message = { id: 'u', sessionId: 's', timeCreated: 1000, data: { role: 'user' } };
const step: Message = { id: 'step', sessionId: 's', timeCreated: 2000, data: { role: 'assistant', finish: 'tool-calls' } };
const reply: Message = { id: 'reply', sessionId: 's', timeCreated: 3000, data: { role: 'assistant', finish: 'stop', time: { created: 3000, completed: 5000 } } };
const part = (messageId: string, id: string, data: Part['data']): Part => ({ sessionId: 's', messageId, id, data });
const prompt = part('u', 'p', { type: 'text', text: 'Check the result' });

function conversation(messages: Message[], parts: Part[], status: SessionStatus) {
  return <OcmanRuntimeProvider messages={messages} parts={parts} sessionId="s" sessionStatus={status} canSend={false}>
    <AssistantThread />
  </OcmanRuntimeProvider>;
}

function PendingConversation({ messages, parts, status }: { messages: Message[]; parts: Part[]; status: SessionStatus }) {
  const pending = usePendingSend('s');
  const { observeMessages } = pending;
  useEffect(() => observeMessages(messages, parts), [messages, parts, observeMessages]);
  return <>
    <button onClick={() => pending.begin('Check the result', undefined, { id: 'pending-local' })}>Send</button>
    <OcmanRuntimeProvider messages={messages} parts={parts} sessionId="s" sessionStatus={status} canSend={false} pendingSend={pending.pending}>
      <AssistantThread activityPromptAliases={pending.acknowledgedIds} />
    </OcmanRuntimeProvider>
  </>;
}

it('shows one ticking row before any agent events and keeps it open as work arrives', () => {
  vi.useFakeTimers();
  vi.setSystemTime(1000);
  useUiStore.setState({ collapseAgentActivity: true });
  const { rerender } = render(<PendingConversation messages={[]} parts={[]} status="done" />);
  screen.getByTestId('conversation-viewport').scrollTo = vi.fn();
  fireEvent.click(screen.getByRole('button', { name: 'Send' }));
  const row = screen.getByText('Working… 0:00');
  fireEvent.click(row);
  act(() => vi.advanceTimersByTime(2000));
  expect(row).toHaveTextContent('Working… 0:02');
  rerender(<PendingConversation messages={[user]} parts={[prompt]} status="busy" />);
  expect(screen.getByText('Working… 0:02')).toBe(row);
  rerender(<PendingConversation messages={[user, step]} parts={[prompt, part('step', 'progress', { type: 'text', text: 'Checking files' })]} status="busy" />);
  expect(screen.getByText('Checking files').closest('details')).toHaveAttribute('open');
  expect(screen.getAllByText(/Working…/)).toHaveLength(1);
  rerender(<PendingConversation messages={[user, step, reply]} parts={[prompt, part('step', 'progress', { type: 'text', text: 'Checking files' })]} status="done" />);
  act(() => vi.advanceTimersByTime(10000));
  expect(row).toHaveTextContent('Worked for 0:04');
});

it('shows a finish-only error without opening the work details', () => {
  useUiStore.setState({ collapseAgentActivity: true });
  const failed: Message = { ...reply, data: { ...reply.data, finish: 'error' } };
  render(conversation([user, failed], [prompt, part('reply', 'progress', { type: 'text', text: 'Checking files' })], 'error'));
  expect(screen.getByText('Session ended with an error')).toBeVisible();
  expect(screen.getByText('Checking files')).not.toBeVisible();
});

it('keeps only formatted final prose outside the work, and leaves it available to read aloud', () => {
  useUiStore.setState({ collapseAgentActivity: true });
  const { rerender } = render(conversation([user, step, reply], [
    prompt,
    part('step', 'progress', { type: 'text', text: 'Checking files' }),
    part('step', 'tool', { type: 'tool', tool: 'read', state: { status: 'completed', input: { filePath: 'README.md' } } }),
    part('reply', 'thought', { type: 'reasoning', text: 'Consider the timing' }),
    part('reply', 'answer', { type: 'text', text: '**All done.**' }),
  ], 'done'));
  const summary = screen.getByText('Worked for 0:04');
  const details = summary.closest('details');
  expect(details).not.toHaveAttribute('open');
  expect(details).toContainElement(screen.getByText('Checking files'));
  expect(screen.getByText('Checking files')).not.toBeVisible();
  expect(details).toHaveTextContent('README.md');
  expect(details).toHaveTextContent('Consider the timing');
  expect(details).not.toContainElement(screen.getByText('All done.'));
  expect(screen.getByText('All done.').tagName).toBe('STRONG');
  expect(screen.getByRole('button', { name: 'Read aloud' })).toBeVisible();
  fireEvent.click(summary);
  expect(details).toHaveAttribute('open');

  const failed: Message = { ...reply, data: { ...reply.data, finish: 'error', error: { name: 'ProviderError', data: { message: 'Request failed' } } } };
  rerender(conversation([user, failed], [prompt], 'error'));
  expect(screen.getByText(/Request failed/)).toBeVisible();
  expect(screen.queryByRole('button', { name: 'Read aloud' })).not.toBeInTheDocument();
});

it('keeps questions inline while hiding other activity from the same message', () => {
  useUiStore.setState({ collapseAgentActivity: true });
  render(conversation([user, step], [prompt,
    part('step', 'progress', { type: 'text', text: 'Checking files' }),
    part('step', 'question', { type: 'tool', tool: 'question', state: { status: 'running', input: {
      questions: [{ header: 'Tests', question: 'Which tests?', options: [{ label: 'Unit', description: 'Run unit tests' }] }],
    } } }),
  ], 'waiting'));
  expect(screen.getByText('Asked 1 question')).toBeVisible();
  expect(screen.getByText('Checking files')).not.toBeVisible();
});

it('reveals collapsed work when jumping to a tool call', () => {
  useUiStore.setState({ collapseAgentActivity: true });
  const messages = [user, step, reply];
  const parts = [prompt, part('reply', 'tool', { type: 'tool', tool: 'read', state: { status: 'completed', input: { filePath: 'README.md' } } }),
    part('reply', 'answer', { type: 'text', text: 'All done.' })];
  const view = (jump: boolean) => <OcmanRuntimeProvider messages={messages} parts={parts} sessionId="s" sessionStatus="done" canSend={false}>
    <AssistantThread scrollToToolCall={jump ? { messageId: 'reply', toolCallId: 'tool', tick: 1 } : null} />
  </OcmanRuntimeProvider>;
  const { rerender } = render(view(false));
  const work = screen.getByText('Read README.md');
  expect(work).not.toBeVisible();
  const viewport = screen.getByTestId('conversation-viewport');
  viewport.scrollTo = vi.fn();
  rerender(view(true));
  expect(work).toBeVisible();
});
