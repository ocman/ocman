// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest';
import { runSlashCommand, SLASH_COMMANDS, type CommandContext } from './slashCommands';

vi.mock('../../lib/api', () => ({ api: {} }));

function ctx(over: Partial<CommandContext> = {}): CommandContext {
  return {
    session: { id: 's1', platform: 'opencode', directory: '/repo', timeUpdated: 1 },
    portAvailable: true,
    caps: { fork: true, move: false },
    tmuxAvailable: false,
    pending: { pending: null, begin: vi.fn(), fail: vi.fn(), clear: vi.fn(), observeMessages: vi.fn() },
    recentSessionsRef: { current: [] },
    messagesRef: { current: [] },
    partsRef: { current: [] },
    archiveSession: vi.fn(),
    createSession: vi.fn(),
    launchOpencodeInTmux: vi.fn(),
    seedNewSession: vi.fn(),
    navigate: vi.fn(),
    navigateToSession: vi.fn(),
    openWorktreeForm: vi.fn(),
    handleCompact: vi.fn(async () => undefined),
    handleNewSession: vi.fn(async () => undefined),
    handleTmuxShortcut: vi.fn(),
    handleVSCodeShortcut: vi.fn(),
    setShowRenameModal: vi.fn(),
    setShowForkPicker: vi.fn(),
    setShowMovePicker: vi.fn(),
    setShowRenameToast: vi.fn(),
    setShowDisconnectedToast: vi.fn(),
    setRestartToastMessage: vi.fn(),
    setCopyToastMessage: vi.fn(),
    ...over,
  };
}

describe('runSlashCommand', () => {
  it('returns false for commands the platform should handle', async () => {
    expect(await runSlashCommand(ctx(), 'unknown', '')).toBe(false);
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
    expect(c.setShowForkPicker).toHaveBeenCalledWith(true);
    expect(c.setShowMovePicker).not.toHaveBeenCalled();

    await runSlashCommand(c, 'wt', ' feat/x ');
    expect(c.openWorktreeForm).toHaveBeenCalledWith({ projectDir: '/repo', branch: 'feat/x', parentSessionId: 's1' });
    expect(SLASH_COMMANDS.resume).toBe(SLASH_COMMANDS.sessions);
    expect(SLASH_COMMANDS.redo).toBe(SLASH_COMMANDS.undo);
  });

  it('shows the disconnected toast for undo/redo instead of going silent', async () => {
    const c = ctx({ portAvailable: false });
    await runSlashCommand(c, 'undo', '');
    expect(c.setShowDisconnectedToast).toHaveBeenCalledWith(true);
  });
});
