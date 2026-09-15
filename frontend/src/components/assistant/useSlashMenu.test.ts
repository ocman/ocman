// @vitest-environment jsdom
import { act, renderHook, waitFor } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { SlashCommand } from '../../lib/api';

const commands = vi.fn<(sid: string) => Promise<SlashCommand[]>>();
vi.mock('../../lib/api', () => ({ api: { commands: (sid: string) => commands(sid) } }));

import { useSlashMenu } from './useSlashMenu';

const vis = { hasModels: true, hasAgents: true, activeAgent: 'build', hasVariants: false };

describe('useSlashMenu', () => {
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
