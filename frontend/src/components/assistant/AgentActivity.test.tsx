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

it('ticks before work arrives and keeps an expanded work row open through completion', () => {
  vi.useFakeTimers();
  vi.setSystemTime(1000);
  useUiStore.setState({ collapseAgentActivity: true });
  const { rerender } = render(<PendingConversation messages={[]} parts={[]} status="done" />);
  screen.getByTestId('conversation-viewport').scrollTo = vi.fn();
  fireEvent.click(screen.getByRole('button', { name: 'Send' }));
  const timer = screen.getByText('Working… < 1s');
  expect(timer.closest('details')).toBeNull();
  act(() => vi.advanceTimersByTime(2000));
  expect(timer).toHaveTextContent('Working… 2s');
  rerender(<PendingConversation messages={[user]} parts={[prompt]} status="busy" />);
  expect(screen.getByText('Working… 2s')).toBe(timer);
  rerender(<PendingConversation messages={[user, step]} parts={[prompt, part('step', 'progress', { type: 'text', synthetic: true, text: 'Checking files' })]} status="busy" />);
  const row = screen.getByText('Working… 2s');
  fireEvent.click(row);
  expect(screen.getByText('Checking files').closest('details')).toHaveAttribute('open');
  expect(screen.getAllByText(/Working…/)).toHaveLength(1);
  rerender(<PendingConversation messages={[user, step, reply]} parts={[prompt, part('step', 'progress', { type: 'text', synthetic: true, text: 'Checking files' })]} status="done" />);
  act(() => vi.advanceTimersByTime(10000));
  expect(row).toHaveTextContent('Worked for 4s');
});

it('does not show a work disclosure for a completed reply without hidden work', () => {
  useUiStore.setState({ collapseAgentActivity: true });
  render(conversation([user, reply], [prompt, part('reply', 'answer', { type: 'text', text: 'All done.' })], 'done'));
  expect(screen.getByText('All done.')).toBeVisible();
  expect(screen.queryByText(/Worked for/)).not.toBeInTheDocument();
});

it('keeps the compaction marker visible with agent activity collapsed', () => {
  useUiStore.setState({ collapseAgentActivity: true });
  const compaction: Message = { ...step, data: { role: 'assistant', agent: 'compaction' } };
  const { rerender } = render(conversation([user, compaction], [prompt], 'busy'));
  const marker = screen.getByRole('separator', { name: 'Context compaction' });
  expect(marker).toBeVisible();

  rerender(conversation([user, compaction], [prompt, part('step', 'summary', { type: 'text', text: 'Earlier work summarized' })], 'busy'));
  expect(screen.getByRole('separator', { name: 'Context compaction' })).toBeVisible();
  expect(screen.getByText('Earlier work summarized')).toBeVisible();
});

it('keeps progress prose inline before the subsequent tool work in the same message', () => {
  useUiStore.setState({ collapseAgentActivity: true });
  render(conversation([user, step], [prompt,
    part('step', 'comment', { type: 'text', text: 'I found the cause. I will check the fix.' }),
    part('step', 'read', { type: 'tool', tool: 'read', state: { status: 'completed', input: { filePath: 'README.md' } } }),
  ], 'busy'));
  const prose = screen.getByText('I found the cause. I will check the fix.');
  const work = screen.getByText('Read README.md');
  expect(prose).toBeVisible();
  expect(prose.closest('details')).toBeNull();
  expect(work).not.toBeVisible();
  expect(prose.compareDocumentPosition(work)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
});

it('keeps both completed replies inline and groups the background shell with its follow-up', () => {
  useUiStore.setState({ collapseAgentActivity: true });
  const wake: Message = { id: 'wake', sessionId: 's', timeCreated: 7000, data: { role: 'user', synthetic: true } };
  const followup: Message = { ...reply, id: 'followup', timeCreated: 9000, data: { ...reply.data, time: { created: 9000, completed: 11000 } } };
  render(conversation([user, step, reply, wake, followup], [prompt,
    part('step', 'progress', { type: 'text', synthetic: true, text: 'Preparing pull request' }),
    part('reply', 'initial', { type: 'text', text: 'Opened the pull request.' }),
    part('wake', 'shell', { type: 'text', synthetic: true, text: '<shell id="checks" state="completed" command="run test suite">\nTests passed\n</shell>' }),
    part('followup', 'final', { type: 'text', text: 'Tests passed.' }),
  ], 'done'));
  const first = screen.getByText('Opened the pull request.');
  const shell = screen.getByText(/run test suite/);
  const followupWork = shell.closest('details');
  expect(first).toBeVisible();
  expect(screen.getByText('Tests passed.')).toBeVisible();
  expect(screen.getAllByRole('button', { name: 'Read aloud' })).toHaveLength(2);
  expect(followupWork).toHaveTextContent('Worked for 4s');
  expect(shell).not.toBeVisible();
  expect(first.compareDocumentPosition(followupWork!)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  fireEvent.click(followupWork!.querySelector('summary')!);
  expect(shell).toBeVisible();
});

it('folds a background shell completion that arrived before the turn finished', () => {
  useUiStore.setState({ collapseAgentActivity: true });
  const notice: Message = { id: 'notice', sessionId: 's', timeCreated: 2500, data: { role: 'user', synthetic: true } };
  render(conversation([user, step, notice, reply], [prompt,
    part('step', 'read', { type: 'tool', tool: 'read', state: { status: 'completed', input: { filePath: 'README.md' } } }),
    part('notice', 'shell', { type: 'text', synthetic: true, text: '<shell id="lint" state="completed" command="run lint">\nOK\n</shell>' }),
    part('reply', 'final', { type: 'text', text: 'All done.' }),
  ], 'done'));
  const work = screen.getByText('Worked for 4s');
  const command = screen.getByText(/run lint/);
  expect(command).not.toBeVisible();
  fireEvent.click(work);
  expect(command).toBeVisible();
  expect(screen.getByText('Read README.md').compareDocumentPosition(command)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
});

it('shows a finish-only error without opening the work details', () => {
  useUiStore.setState({ collapseAgentActivity: true });
  const failed: Message = { ...reply, data: { ...reply.data, finish: 'error' } };
  render(conversation([user, failed], [prompt, part('reply', 'progress', { type: 'text', synthetic: true, text: 'Checking files' })], 'error'));
  expect(screen.getByText('Session ended with an error')).toBeVisible();
  expect(screen.getByText('Checking files')).not.toBeVisible();
});

it('keeps only formatted final prose outside the work, and leaves it available to read aloud', () => {
  useUiStore.setState({ collapseAgentActivity: true });
  const { rerender } = render(conversation([user, step, reply], [
    prompt,
    part('step', 'progress', { type: 'text', synthetic: true, text: 'Checking files' }),
    part('step', 'tool', { type: 'tool', tool: 'read', state: { status: 'completed', input: { filePath: 'README.md' } } }),
    part('reply', 'thought', { type: 'reasoning', text: 'Consider the timing' }),
    part('reply', 'answer', { type: 'text', text: '**All done.**' }),
  ], 'done'));
  const summary = screen.getByText('Worked for 4s');
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
    part('step', 'progress', { type: 'text', synthetic: true, text: 'Checking files' }),
    part('step', 'question', { type: 'tool', tool: 'question', state: { status: 'running', input: {
      questions: [{ header: 'Tests', question: 'Which tests?', options: [{ label: 'Unit', description: 'Run unit tests' }] }],
    } } }),
  ], 'waiting'));
  expect(screen.getByText('Asked 1 question')).toBeVisible();
  expect(screen.getByText('Checking files')).not.toBeVisible();
});

it('stops the work timer at a question and starts a separate disclosure after the answer', () => {
  vi.useFakeTimers();
  vi.setSystemTime(6000);
  useUiStore.setState({ collapseAgentActivity: true });
  const question = { ...step, id: 'question', timeCreated: 3000 };
  const later = { ...step, id: 'later', timeCreated: 11000 };
  const earlier = part('step', 'before', { type: 'text', synthetic: true, text: 'Before the question' });
  const after = part('later', 'after', { type: 'text', synthetic: true, text: 'After the question' });
  const asks = (answered: boolean) => part('question', 'ask', { type: 'tool', tool: 'question', time: { start: 4000 }, state: {
    status: answered ? 'completed' : 'running', time: { start: 4000, ...(answered ? { end: 10000 } : {}) },
    input: { questions: [{ header: 'Tests', question: 'Which tests?', options: [{ label: 'Unit', description: 'Run unit tests' }] }] },
  } });
  const { rerender } = render(conversation([user, step, question], [prompt, earlier, asks(false)], 'busy'));
  screen.getByTestId('conversation-viewport').scrollTo = vi.fn();
  const frozen = screen.getByText('Worked for 3s');
  expect(screen.queryByText(/Working…/)).not.toBeInTheDocument();
  act(() => vi.advanceTimersByTime(6000));
  expect(frozen).toHaveTextContent('Worked for 3s');
  rerender(conversation([user, step, question, later], [prompt, earlier, asks(true), after], 'busy'));
  const resumed = screen.getByText('Working… 2s');
  const firstDetails = frozen.closest('details');
  const secondDetails = resumed.closest('details');
  const asked = screen.getByText('Asked 1 question');
  expect(frozen.compareDocumentPosition(asked)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  expect(asked.compareDocumentPosition(resumed)).toBe(Node.DOCUMENT_POSITION_FOLLOWING);
  fireEvent.click(resumed);
  expect(secondDetails).toContainElement(screen.getByText('After the question'));
  expect(firstDetails).not.toContainElement(screen.getByText('After the question'));
  expect(screen.getByText('Before the question')).not.toBeVisible();
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
