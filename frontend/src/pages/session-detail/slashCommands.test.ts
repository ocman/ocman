// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { runSlashCommand, SLASH_COMMANDS, type CommandContext } from './slashCommands';
import { visibleSidebarSessions } from '../../lib/sidebarHelpers';
import type { Session } from '../../lib/api';
import { useUiStore } from '../../lib/uiStore';
import { act, renderHook } from '@testing-library/react';
import { usePendingSend } from './usePendingSend';
import type { OceEnvelope } from '../../lib/conversation/events';
import { reduceConversation } from '../../lib/conversation/conversationReducer';
import { seedView } from '../../test/oceFixtures';
// @ts-expect-error Vitest runs in Node; application types intentionally exclude Node globals.
import { readFileSync } from 'node:fs';

vi.mock('../../lib/api', () => ({ api: {} }));
vi.mock('../../lib/remoteLog', () => ({ remoteLog: { error: vi.fn() } }));

function ctx(over: Partial<CommandContext> = {}): CommandContext {
  return {
    session: { id: 's1', platform: 'opencode', directory: '/repo', timeUpdated: 1 },
    portAvailable: true,
    caps: { fork: true, move: false, revert: true, runtimeRestart: true },
    pending: { pending: null, begin: vi.fn().mockReturnValue('command-id'), fail: vi.fn(), clear: vi.fn(), observeMessages: vi.fn() },
    recentSessionsRef: { current: [] },
    messagesRef: { current: [] },
    partsRef: { current: [] },
    archiveSession: vi.fn(),
    navigate: vi.fn(),
    navigateToSession: vi.fn(),
    openWorktreeForm: vi.fn(),
    handleCompact: vi.fn(async () => undefined),
    handleNewSession: vi.fn(async () => undefined),
    handleTmuxShortcut: vi.fn(),
    handleVSCodeShortcut: vi.fn(),
    openModal: vi.fn(),
    setShowRenameToast: vi.fn(),
    setShowDisconnectedToast: vi.fn(),
    setRestartToastMessage: vi.fn(),
    setCopyToastMessage: vi.fn(),
    ...over,
  };
}

describe('runSlashCommand', () => {
  it('/compact acknowledges its successful assistant-only lifecycle', async () => {
    const { result } = renderHook(() => usePendingSend('s1'));
    let finish!: () => void;
    const c = ctx({ pending: result.current, handleCompact: () => new Promise<void>((resolve) => { finish = resolve; }) });
    let running!: Promise<boolean>;
    act(() => { running = runSlashCommand(c, 'compact', ''); });
    const compaction = readFileSync('../internal/convevents/v1map/testdata/ocv2/compaction.oce.jsonl', 'utf8');
    const events = (compaction as string).trim().split('\n').map((line) => JSON.parse(line) as OceEnvelope);
    const view = reduceConversation(seedView('s1'), events);
    act(() => { result.current.observeMessages(view.messages, view.parts); });
    expect(result.current.pending?.text).toBe('/compact');
    await act(async () => { finish(); await running; });
    expect(result.current.pending).toBeNull();
  });
  it('/compact cannot acknowledge a newer submission or another session', async () => {
    const { result } = renderHook(() => usePendingSend('s1'));
    let finish!: () => void;
    const c = ctx({ pending: result.current, handleCompact: () => new Promise<void>((resolve) => { finish = resolve; }) });
    let running!: Promise<boolean>;
    act(() => { running = runSlashCommand(c, 'compact', ''); });
    act(() => { result.current.begin('newer prompt'); });
    await act(async () => { finish(); await running; });
    expect(result.current.pending?.text).toBe('newer prompt');
    const moved = ctx({ isCurrent: () => false });
    await runSlashCommand(moved, 'compact', '');
    expect(moved.pending.clear).not.toHaveBeenCalled();
  });
  it('/permissions opens Ocman session rules without requiring a live agent', async () => {
    const open = vi.fn();
    window.addEventListener('oc-session-permissions-open', open);
    try {
      expect(await runSlashCommand(ctx({ portAvailable: false }), 'permissions', '')).toBe(true);
      expect(open).toHaveBeenCalledOnce();
    } finally { window.removeEventListener('oc-session-permissions-open', open); }
  });

  it('/compact shows the submitted command before compaction finishes', async () => {
    let finish!: () => void;
    const c = ctx({ handleCompact: vi.fn(() => new Promise<void>((resolve) => { finish = resolve; })) });
    const running = runSlashCommand(c, 'compact', '');
    expect(c.pending.begin).toHaveBeenCalledWith('/compact');
    finish();
    await running;
  });

  it('/compact keeps failures visible instead of only logging them', async () => {
    const c = ctx({ handleCompact: vi.fn().mockRejectedValue(new Error('Session is busy')) });
    await runSlashCommand(c, 'compact', '');
    expect(c.pending.fail).toHaveBeenCalledWith('Session is busy', 'command-id');
  });

  it('/clear archives the session and opens a draft route on the same owner', async () => {
    const c = ctx({ session: { id: 's1', platform: 'r-box:opencode', remoteId: 'box', directory: '/repo', timeUpdated: 1 } });
    await runSlashCommand(c, 'clear', 'Next task');
    expect(c.archiveSession).toHaveBeenCalledWith('r-box:opencode', 's1', 1, true);
    expect(c.navigate).toHaveBeenCalledWith(expect.stringMatching(/^\/session\/new\?dir=%2Frepo&remoteId=box&platform=r-box%3Aopencode&title=Next\+task&draftId=.+$/));
  });

  it('/clear still opens the draft when archival fails', async () => {
    const c = ctx({ archiveSession: vi.fn().mockRejectedValue(new Error('offline')) });
    await runSlashCommand(c, 'clear', '');
    expect(c.navigate).toHaveBeenCalledWith(expect.stringMatching(/^\/session\/new\?dir=%2Frepo&platform=opencode&draftId=.+$/));
  });

  it.each([
    ['recent', 's2'],
    // Grouped view: no sibling in /a, so the newest visible session wins.
    ['projects', 's3'],
  ] as const)('/archive picks among the rows the sidebar shows in the %s view', async (view, expected) => {
    useUiStore.setState({ sidebarView: view });
    visibleSidebarSessions.current = [
      { id: 's1', directory: '/a', timeUpdated: 1000, lastUserPromptAt: 1000 },
      { id: 's2', directory: '/b', timeUpdated: 100, lastHaltAt: 100 },
      { id: 's3', directory: '/c', timeUpdated: 900, lastTurnCompletedAt: 900 },
    ] as Session[];
    try {
      const c = ctx({ recentSessionsRef: { current: [{ id: 's1' }, { id: 'hidden' }] as Session[] } });
      await runSlashCommand(c, 'archive', '');
      expect(c.navigateToSession).toHaveBeenCalledWith(expected);
    } finally {
      visibleSidebarSessions.current = null;
    }
  });

  it('/archive opens a visible session when a filter hides the archived one', async () => {
    useUiStore.setState({ sidebarView: 'projects' });
    visibleSidebarSessions.current = [{ id: 'other', directory: '/b', timeUpdated: 5 }] as Session[];
    try {
      const c = ctx();
      await runSlashCommand(c, 'archive', '');
      expect(c.navigateToSession).toHaveBeenCalledWith('other');
    } finally {
      visibleSidebarSessions.current = null;
    }
  });

  it('/archive opens an empty conversation, not the unfiltered root, after the last visible row', async () => {
    visibleSidebarSessions.current = [{ id: 's1', directory: '/repo', timeUpdated: 1 }] as Session[];
    try {
      const c = ctx({ recentSessionsRef: { current: [{ id: 's1' }, { id: 'hidden' }] as Session[] } });
      await runSlashCommand(c, 'archive', '');
      expect(c.navigateToSession).not.toHaveBeenCalled();
      expect(c.navigate).toHaveBeenCalledWith('/session/new');
    } finally {
      visibleSidebarSessions.current = null;
    }
  });

  it('/archive falls back to the recent list while the sidebar is not mounted', async () => {
    useUiStore.setState({ sidebarView: 'recent' });
    const c = ctx({ recentSessionsRef: { current: [{ id: 's1' }, { id: 's2' }] as Session[] } });
    await runSlashCommand(c, 'archive', '');
    expect(c.navigateToSession).toHaveBeenCalledWith('s2');
  });

  it('returns false for commands the platform should handle', async () => {
    expect(await runSlashCommand(ctx(), 'unknown', '')).toBe(false);
  });

  it('/wt creates the worktree on the session owner, not by path', async () => {
    const c = ctx({ session: { id: 's1', platform: 'r-B:opencode', remoteId: 'B', directory: '/repo', timeUpdated: 1 } });
    await runSlashCommand(c, 'wt', '');
    expect(c.openWorktreeForm).toHaveBeenCalledWith(expect.objectContaining({ projectDir: '/repo', remoteId: 'B' }));
  });

  it('silently swallows live commands when the port is down', async () => {
    const c = ctx({ portAvailable: false });
    expect(await runSlashCommand(c, 'compact', '')).toBe(true);
    expect(c.handleCompact).not.toHaveBeenCalled();
  });

  it('gates fork/move on capabilities and resolves aliases', async () => {
    const c = ctx();
    await runSlashCommand(c, 'fork', '');
    await runSlashCommand(c, 'move', '');
    expect(c.openModal).toHaveBeenCalledExactlyOnceWith('fork');

    await runSlashCommand(c, 'wt', ' feat/x ');
    expect(c.openWorktreeForm).toHaveBeenCalledWith({ projectDir: '/repo', branch: 'feat/x', parentSessionId: 's1', remoteId: 'local' });
    expect(SLASH_COMMANDS.resume).toBe(SLASH_COMMANDS.sessions);
    expect(SLASH_COMMANDS.redo).toBe(SLASH_COMMANDS.undo);
  });

  it('shows the disconnected toast for undo/redo instead of going silent', async () => {
    const c = ctx({ portAvailable: false });
    await runSlashCommand(c, 'undo', '');
    expect(c.setShowDisconnectedToast).toHaveBeenCalledWith(true);
  });

  it('refuses commands the platform does not support, visibly', async () => {
    const c = ctx({ caps: { fork: true, move: false, revert: false, runtimeRestart: false } });
    for (const command of ['undo', 'redo', 'restart-opencode']) {
      expect(await runSlashCommand(c, command, '')).toBe(true);
      expect(c.pending.fail).toHaveBeenLastCalledWith(`/${command} is not available for this session`);
    }
    expect(c.setShowDisconnectedToast).not.toHaveBeenCalled();
    expect(c.setRestartToastMessage).not.toHaveBeenCalled();
  });
});
