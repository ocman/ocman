// @vitest-environment jsdom

import { act, fireEvent, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { useUiStore } from '../../../lib/uiStore';
import type { SessionInfo } from '../../../lib/api';
import { makeSession, makeSessionDetail, renderSessionPage } from './harness';

function info(messageId = 'old-message', partId = 'commit-part', callId = 'commit-call'): SessionInfo {
  return {
    sessionId: 'sess_1', supported: false,
    context: { tokens: 0, cost: 0, estCost: 0 },
    tokens: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
    messages: { user: 0, assistant: 0 }, mcpServers: [], lspServers: [],
    commitCaptureSupported: true,
    commits: [{ order: 1, sha: 'abc1234', branch: 'main', subject: 'Tracked commit', sourceMessageId: messageId, toolPartId: partId, toolCallId: callId, observedAt: 1 }],
  };
}

beforeEach(() => {
  useUiStore.setState({ changesSidebarOpenTabs: ['info'] });
});

describe('SessionDetail commit source jump', () => {
  it('reuses loaded history for repeated commits from one call', async () => {
    const session = makeSession();
    const detail = makeSessionDetail(session, {
      messages: [{ id: 'old-message', sessionId: session.id, timeCreated: 1, data: { role: 'assistant' } }],
      parts: [{ id: 'commit-part', messageId: 'old-message', sessionId: session.id, data: { type: 'tool', tool: 'bash', callID: 'commit-call', state: { status: 'completed' } } }],
    });
    const sessionInfo = info();
    sessionInfo.commits!.push({ ...sessionInfo.commits![0], order: 2, sha: 'def5678', subject: 'Second commit' });
    const fetchSession = vi.fn().mockResolvedValue(detail);
    renderSessionPage({ detail, sessionInfo, apiOverrides: { session: fetchSession } });
    await screen.findByTestId('assistant-thread');

    fireEvent.click(screen.getByRole('button', { name: 'Open source call for commit abc1234 on main: Tracked commit' }));
    expect(screen.getByTestId('assistant-thread-tool-target')).toHaveTextContent('old-message:commit-call:1');
    fireEvent.click(screen.getByRole('button', { name: 'Open source call for commit def5678 on main: Second commit' }));
    expect(screen.getByTestId('assistant-thread-tool-target')).toHaveTextContent('old-message:commit-call:2');
    fireEvent.click(screen.getByRole('button', { name: 'Open source call for commit def5678 on main: Second commit' }));
    expect(screen.getByTestId('assistant-thread-tool-target')).toHaveTextContent('old-message:commit-call:3');
    expect(fetchSession).toHaveBeenCalledOnce();
  });

  it('falls back to part identity when the source has no call ID', async () => {
    const session = makeSession();
    const detail = makeSessionDetail(session, {
      messages: [{ id: 'old-message', sessionId: session.id, timeCreated: 1, data: { role: 'assistant' } }],
      parts: [{ id: 'commit-part', messageId: 'old-message', sessionId: session.id, data: { type: 'tool', tool: 'bash', state: { status: 'completed' } } }],
    });
    renderSessionPage({ detail, sessionInfo: info('old-message', 'commit-part', '') });
    await screen.findByTestId('assistant-thread');

    fireEvent.click(screen.getByRole('button', { name: 'Open source call for commit abc1234 on main: Tracked commit' }));

    expect(screen.getByTestId('assistant-thread-tool-target')).toHaveTextContent('old-message:commit-part:1');
  });

  it('does not let an older history request override a later loaded selection', async () => {
    const session = makeSession();
    const detail = makeSessionDetail(session, {
      messages: [{ id: 'loaded-message', sessionId: session.id, timeCreated: 2, data: { role: 'assistant' } }],
      parts: [{ id: 'loaded-part', messageId: 'loaded-message', sessionId: session.id, data: { type: 'tool', tool: 'bash', callID: 'loaded-call', state: { status: 'completed' } } }],
    });
    const sessionInfo = info('missing-message', 'missing-part', 'missing-call');
    sessionInfo.commits!.push({ ...sessionInfo.commits![0], order: 2, sha: 'def5678', subject: 'Loaded commit', sourceMessageId: 'loaded-message', toolPartId: 'loaded-part', toolCallId: 'loaded-call' });
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const fetchSession = vi.fn()
      .mockResolvedValueOnce(detail)
      .mockImplementation(() => gate.then(() => detail));
    renderSessionPage({ detail, sessionInfo, apiOverrides: { session: fetchSession } });
    await screen.findByTestId('assistant-thread');

    fireEvent.click(screen.getByRole('button', { name: 'Open source call for commit abc1234 on main: Tracked commit' }));
    fireEvent.click(screen.getByRole('button', { name: 'Open source call for commit def5678 on main: Loaded commit' }));
    expect(screen.getByTestId('assistant-thread-tool-target')).toHaveTextContent('loaded-message:loaded-call:1');
    await act(async () => { release(); await gate; });
    expect(screen.getByTestId('assistant-thread-tool-target')).toHaveTextContent('loaded-message:loaded-call:1');
  });

  it.each(['opencode', 'r-owner:opencode'])('hydrates older %s history through the owner-aware session read', async (platform) => {
    const session = makeSession({ platform });
    const initial = makeSessionDetail(session, { totalMessages: 2 });
    const full = makeSessionDetail(session, {
      messages: [{ id: 'old-message', sessionId: session.id, timeCreated: 1, data: { role: 'assistant' } }],
      parts: [{ id: 'commit-part', messageId: 'old-message', sessionId: session.id, data: { type: 'tool', tool: 'bash', callID: 'commit-call', state: { status: 'completed', input: { command: 'git commit' }, output: '[main abc1234] Tracked commit' } } }],
      totalMessages: 2,
    });
    const fetchSession = vi.fn().mockResolvedValueOnce(initial).mockResolvedValue(full);
    renderSessionPage({ detail: initial, sessionInfo: info(), apiOverrides: { session: fetchSession } });

    await screen.findByTestId('assistant-thread');
    fireEvent.click(await screen.findByRole('button', { name: 'Open source call for commit abc1234 on main: Tracked commit' }));

    await waitFor(() => expect(fetchSession).toHaveBeenLastCalledWith(session.id, 2_147_483_647, 0, expect.any(AbortSignal), platform));
    await waitFor(() => expect(screen.getByTestId('assistant-thread-tool-target')).toHaveTextContent('old-message:commit-call:1'));
  });

  it('reports a deleted source without removing the commit', async () => {
    const session = makeSession();
    const detail = makeSessionDetail(session);
    renderSessionPage({ detail, sessionInfo: info(), apiOverrides: { session: vi.fn().mockResolvedValue(detail) } });

    await screen.findByTestId('assistant-thread');
    fireEvent.click(await screen.findByRole('button', { name: 'Open source call for commit abc1234 on main: Tracked commit' }));

    expect((await screen.findByText('Source call is no longer available.')).closest('[role="status"]')).not.toBeNull();
    expect(screen.getByRole('button', { name: 'Open source call for commit abc1234 on main: Tracked commit' })).toBeInTheDocument();
    expect(screen.getByTestId('assistant-thread-tool-target')).toHaveTextContent('');
  });

  it('ignores an outstanding history result after switching sessions', async () => {
    const first = makeSession();
    const second = makeSession({ id: 'sess_2', platform: 'r-other:opencode' });
    const firstDetail = makeSessionDetail(first);
    const secondDetail = makeSessionDetail(second);
    let release!: () => void;
    const gate = new Promise<void>((resolve) => { release = resolve; });
    const stale = makeSessionDetail(first, {
      messages: [{ id: 'old-message', sessionId: first.id, timeCreated: 1, data: { role: 'assistant' } }],
      parts: [{ id: 'commit-part', messageId: 'old-message', sessionId: first.id, data: { type: 'tool', tool: 'bash', callID: 'commit-call', state: { status: 'completed' } } }],
    });
    const fetchSession = vi.fn((id: string, _limit: number) => {
      if (id === second.id) return Promise.resolve(secondDetail);
      if (_limit === 2_147_483_647) return gate.then(() => stale);
      return Promise.resolve(firstDetail);
    });
    const page = renderSessionPage({ detail: firstDetail, sessions: [first, second], sessionInfo: info(), apiOverrides: { session: fetchSession } });

    await screen.findByTestId('assistant-thread');
    fireEvent.click(await screen.findByRole('button', { name: 'Open source call for commit abc1234 on main: Tracked commit' }));
    act(() => page.navigate(`/session/${second.id}`));
    await waitFor(() => expect(fetchSession).toHaveBeenCalledWith(second.id, expect.any(Number), 0, expect.any(AbortSignal), undefined));
    await act(async () => { release(); await gate; });

    expect(screen.getByTestId('assistant-thread-tool-target')).toHaveTextContent('');
  });

  it('does not replay a previous jump after leaving and returning to a session', async () => {
    const first = makeSession();
    const second = makeSession({ id: 'sess_2' });
    const firstDetail = makeSessionDetail(first, {
      messages: [{ id: 'old-message', sessionId: first.id, timeCreated: 1, data: { role: 'assistant' } }],
      parts: [{ id: 'commit-part', messageId: 'old-message', sessionId: first.id, data: { type: 'tool', tool: 'bash', callID: 'commit-call', state: { status: 'completed' } } }],
    });
    const secondDetail = makeSessionDetail(second);
    const fetchSession = vi.fn((id: string) => Promise.resolve(id === first.id ? firstDetail : secondDetail));
    const page = renderSessionPage({ detail: firstDetail, sessions: [first, second], sessionInfo: info(), apiOverrides: { session: fetchSession } });
    await screen.findByTestId('assistant-thread');
    fireEvent.click(screen.getByRole('button', { name: 'Open source call for commit abc1234 on main: Tracked commit' }));
    expect(screen.getByTestId('assistant-thread-tool-target')).toHaveTextContent('old-message:commit-call:1');

    act(() => page.navigate('/session/sess_2'));
    await waitFor(() => expect(fetchSession).toHaveBeenCalledWith('sess_2', expect.any(Number), 0, expect.any(AbortSignal), undefined));
    act(() => page.navigate('/session/sess_1'));
    await waitFor(() => expect(screen.getByTestId('assistant-thread-tool-target')).toHaveTextContent(''));
  });
});
