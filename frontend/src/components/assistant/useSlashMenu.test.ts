// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { SlashCommand } from '../../lib/api';

const commands = vi.fn<(sid: string, signal?: AbortSignal, platform?: string) => Promise<SlashCommand[]>>();
vi.mock('../../lib/api', () => ({ api: { commands: (sid: string, signal?: AbortSignal, platform?: string) => commands(sid, signal, platform) } }));

import { useSlashMenu } from './useSlashMenu';

const vis = { hasModels: true, hasAgents: true, activeAgent: 'build', hasVariants: false };

describe('useSlashMenu', () => {
  it('fetches and reloads the right owner when session IDs are duplicated', async () => {
    commands.mockImplementation((_id, _signal, platform) => Promise.resolve([{ name: platform === 'opencode' ? 'local-skill' : 'remote-skill', source: 'skill' }]));
    const local = renderHook(() => useSlashMenu('duplicate', vis, undefined, 'opencode'));
    const remote = renderHook(() => useSlashMenu('duplicate', vis, undefined, 'r-owner:opencode'));
    await waitFor(() => expect(local.result.current.commands.some((c) => c.name === 'local-skill')).toBe(true));
    await waitFor(() => expect(remote.result.current.commands.some((c) => c.name === 'remote-skill')).toBe(true));
    commands.mockClear();
    act(() => window.dispatchEvent(new CustomEvent('oc-slash-commands-reload', { detail: { sessionId: 'duplicate', platform: 'r-owner:opencode' } })));
    await waitFor(() => expect(commands).toHaveBeenCalledTimes(1));
    expect(commands).toHaveBeenCalledWith('duplicate', undefined, 'r-owner:opencode');
  });

  it('refreshes skills and commands after reload without changing the session', async () => {
    commands.mockResolvedValue([{ name: 'old-skill', source: 'skill' } as SlashCommand]);
    const { result } = renderHook(() => useSlashMenu('s1', vis));
    await waitFor(() => expect(result.current.commands.some((c) => c.name === 'old-skill')).toBe(true));
    commands.mockResolvedValue([
      { name: 'new-skill', source: 'skill' } as SlashCommand,
      { name: 'new-command', source: 'command' } as SlashCommand,
    ]);
    act(() => window.dispatchEvent(new CustomEvent('oc-slash-commands-reload', { detail: { sessionId: 's1' } })));
    await waitFor(() => expect(result.current.commands.some((c) => c.name === 'new-skill')).toBe(true));
    expect(result.current.commands.some((c) => c.name === 'new-command')).toBe(true);
    expect(result.current.commands.some((c) => c.name === 'old-skill')).toBe(false);
  });

  it('merges platform commands over built-ins and filters on input', async () => {
    commands.mockResolvedValue([
      { name: 'model', description: 'platform model' } as SlashCommand,
      { name: 'my-skill', description: 'x', source: 'skill' } as SlashCommand,
    ]);
    const { result } = renderHook(() => useSlashMenu('s1', vis));
    await waitFor(() => expect(result.current.commands.some((c) => c.name === 'my-skill')).toBe(true));
    expect(result.current.commands.filter((c) => c.name === 'model')).toHaveLength(1);
    expect(result.current.commands.find((c) => c.name === 'model')?.description).toBe('platform model');
    expect(result.current.filtered.some((c) => c.name === 'skills')).toBe(true);
    expect(result.current.filtered.some((c) => c.name === 'variants')).toBe(false);

    act(() => result.current.syncToInput('/myskl'));
    expect(result.current.open).toBe(true);
    expect(result.current.filtered.map((c) => c.name)).toEqual(['my-skill']);

    act(() => result.current.moveIndex(-1));
    expect(result.current.index).toBe(result.current.filtered.length - 1);
    act(() => result.current.moveIndex(1));
    expect(result.current.index).toBe(0);

    act(() => result.current.syncToInput('/model x'));
    expect(result.current.open).toBe(false);
    act(() => result.current.syncToInput('/'));
    act(() => result.current.close());
    expect(result.current.open).toBe(false);
    expect(result.current.index).toBe(0);
  });

  it.each(['resolve', 'reject'])('ignores an older catalog %s after reload and ignores other sessions', async (outcome) => {
    let resolve!: (value: SlashCommand[]) => void;
    let reject!: (error: Error) => void;
    commands.mockImplementationOnce(() => new Promise<SlashCommand[]>((ok, fail) => { resolve = ok; reject = fail; }));
    commands.mockResolvedValue([{ name: 'fresh-skill', source: 'skill' }]);
    const { result, unmount } = renderHook(() => useSlashMenu('s1', vis));
    const count = commands.mock.calls.length;
    act(() => window.dispatchEvent(new CustomEvent('oc-slash-commands-reload', { detail: { sessionId: 'other' } })));
    expect(commands).toHaveBeenCalledTimes(count);
    act(() => window.dispatchEvent(new CustomEvent('oc-slash-commands-reload', { detail: { sessionId: 's1' } })));
    await waitFor(() => expect(result.current.commands.some((c) => c.name === 'fresh-skill')).toBe(true));
    await act(async () => {
      if (outcome === 'resolve') resolve([{ name: 'stale-skill', source: 'skill' }]);
      else reject(new Error('old request failed'));
    });
    expect(result.current.commands.some((c) => c.name === 'fresh-skill')).toBe(true);
    expect(result.current.commands.some((c) => c.name === 'stale-skill')).toBe(false);
    unmount();
    const finalCount = commands.mock.calls.length;
    act(() => window.dispatchEvent(new CustomEvent('oc-slash-commands-reload', { detail: { sessionId: 's1' } })));
    expect(commands).toHaveBeenCalledTimes(finalCount);
  });

  it('ranks name matches first, then description hits, and wires aria to the highlighted option', async () => {
    commands.mockResolvedValue([
      { name: 'ship-it', description: 'deploy the model' } as SlashCommand,
      { name: 'remodel', description: 'x' } as SlashCommand,
    ]);
    const { result } = renderHook(() => useSlashMenu('s1', vis));
    await waitFor(() => expect(result.current.commands.some((c) => c.name === 'remodel')).toBe(true));

    act(() => result.current.syncToInput('/model'));
    // exact built-in, then name substring, then description-only hit last
    expect(result.current.filtered.map((c) => c.name)).toEqual(['model', 'remodel', 'ship-it']);

    act(() => result.current.moveIndex(1));
    expect(result.current.inputAria['aria-activedescendant']).toBe(result.current.optionId(1));
    expect(result.current.inputAria['aria-controls']).toBe(result.current.listboxId);

    // typing re-ranks, so the highlight snaps back to the best match
    act(() => result.current.syncToInput('/mode'));
    expect(result.current.index).toBe(0);

    act(() => result.current.syncToInput('/zzz'));
    expect(result.current.inputAria['aria-activedescendant']).toBeUndefined();
  });

  it('hides feature commands that are unavailable and falls back on fetch failure', async () => {
    commands.mockRejectedValue(new Error('offline'));
    const { result } = renderHook(() => useSlashMenu('s1', { hasModels: false, hasAgents: false, activeAgent: undefined, hasVariants: true }));
    await waitFor(() => expect(commands).toHaveBeenCalled());
    const names = result.current.filtered.map((c) => c.name);
    expect(names).not.toContain('model');
    expect(names).not.toContain('agent');
    expect(names).not.toContain('skills');
    expect(names).toContain('variants');
  });
});

describe('useSlashMenu without a session', () => {
  it('uses the provided platform commands instead of fetching', () => {
    commands.mockClear();
    const provided = [{ name: 'review', description: 'Review', source: 'command' } as SlashCommand];
    const { result } = renderHook(() => useSlashMenu(undefined, vis, provided));
    expect(commands).not.toHaveBeenCalled();
    expect(result.current.commands.some((c) => c.name === 'review')).toBe(true);
    expect(result.current.commands.some((c) => c.name === 'new')).toBe(true);
  });
});
