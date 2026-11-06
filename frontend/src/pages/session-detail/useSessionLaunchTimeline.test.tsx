// @vitest-environment jsdom
import { renderHook } from '@testing-library/react';
import { beforeEach, expect, it, vi } from 'vitest';
import { useSessionLaunchTimeline } from './useSessionLaunchTimeline';
import { useBackendConversationDrafts } from '../../lib/backendConversationDrafts';
import type { Message, Part } from '../../lib/api.types';
import type { ReactElement } from 'react';

vi.mock('../../lib/api.requests', () => ({ fetchJSON: vi.fn(async () => []), postJSON: vi.fn() }));
const id = 'ocm_01234567-89ab-4cde-8fab-0123456789ab';
const messages: Message[] = [{ id: 'first', sessionId: id, timeCreated: 200, data: { role: 'user' } }];
const parts: Part[] = [{ id: 'text', sessionId: id, messageId: 'first', data: { type: 'text', text: 'Prompt' } }];

beforeEach(() => useBackendConversationDrafts.setState({ drafts: { [id]: { id,
  metadata: { draftId: id, directory: '/repo' }, phase: 'ready', createdAt: 50, submittedAt: 100,
  steps: { prompt: 'done' }, request: { directory: '/repo', prompt: 'Prompt' },
} } }));

it('attaches completed setup to the initial native user message with the submitted timestamp', () => {
  const { result } = renderHook(() => useSessionLaunchTimeline(id, messages, parts, false));
  expect(result.current?.context?.messageId).toBe('first');
  expect(result.current?.context?.timestamp).toBe(100);
  expect((result.current?.context?.setup as ReactElement<{ complete: boolean }>).props.complete).toBe(true);
  expect(result.current?.empty).toBeNull();
});

it('shows the saved prompt until any native messages arrive', () => {
  const { result } = renderHook(() => useSessionLaunchTimeline(id, [], [], false));
  expect(result.current?.context).toBeNull();
  expect((result.current?.empty as ReactElement<{ prompt: string }>).props.prompt).toBe('Prompt');
});

it('never attaches setup to a later page of history or an empty user header', () => {
  const { result, rerender } = renderHook(({ hasMore, visibleParts }) => useSessionLaunchTimeline(id, messages, visibleParts, hasMore),
    { initialProps: { hasMore: true, visibleParts: parts } });
  expect(result.current?.context).toBeNull();
  expect(result.current?.empty).toBeNull();
  rerender({ hasMore: false, visibleParts: [] });
  expect(result.current?.context).toBeNull();
  expect(result.current?.empty).not.toBeNull();
  rerender({ hasMore: false, visibleParts: parts });
  expect(result.current?.empty).toBeNull();
  expect(result.current?.context?.messageId).toBe('first');
});
