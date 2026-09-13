// @vitest-environment jsdom

import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AgentInfo, Message, Part } from '../../lib/api';
import type { SessionMetadata } from '../../lib/sessionReducer';
import { getProjectModel, saveProjectModel } from '../../lib/projectModel';
import { useComposerModel, type UseComposerModelOptions } from './useComposerModel';

const session = { id: 's1', directory: '/repo', defaultModel: 'prov/default' } as SessionMetadata;
const turnOn = (model: string): Message[] => [
  { id: 'u1', sessionId: 's1', timeCreated: 1, data: { role: 'user' } } as Message,
  {
    id: 'a1', sessionId: 's1', timeCreated: 2,
    data: { role: 'assistant', providerID: model.split('/')[0], modelID: model.split('/')[1] },
  } as unknown as Message,
];

function opts(over: Partial<UseComposerModelOptions> = {}): UseComposerModelOptions {
  return {
    id: 's1',
    session,
    messages: [],
    parts: [] as Part[],
    modelOptions: ['prov/default', 'prov/other'],
    agents: [{ name: 'coder', model: 'prov/agent-model' } as AgentInfo],
    setSelectedModel: vi.fn(),
    setSelectedAgent: vi.fn(),
    setSelectedReasoning: vi.fn(),
    ...over,
  };
}

describe('useComposerModel', () => {
  beforeEach(() => localStorage.clear());

  it('seeds the composer once from the session default and dedupes the model list', () => {
    const o = opts();
    const { result, rerender } = renderHook((p: UseComposerModelOptions) => useComposerModel(p), { initialProps: o });
    expect(result.current.activeModel).toBe('prov/default');
    expect(result.current.composerModels).toEqual(['prov/default', 'prov/other']);
    expect(o.setSelectedModel).toHaveBeenCalledTimes(1);
    expect(o.setSelectedModel).toHaveBeenCalledWith('prov/default');

    // A later assistant turn on another model must not move the selection.
    rerender({ ...o, messages: turnOn('prov/other') });
    expect(result.current.activeModel).toBe('prov/other');
    expect(o.setSelectedModel).toHaveBeenCalledTimes(1);
  });

  it('does not seed while session still belongs to the previous route id', () => {
    const o = opts({ id: 's2' });
    renderHook(() => useComposerModel(o));
    expect(o.setSelectedModel).not.toHaveBeenCalled();
  });

  it('prefers the remembered project model for an empty conversation', () => {
    saveProjectModel('/repo', 'prov/remembered');
    const { result } = renderHook(() => useComposerModel(opts()));
    expect(result.current.activeModel).toBe('prov/remembered');
  });

  it('persists a manual model change and resets reasoning', () => {
    const o = opts();
    const { result } = renderHook(() => useComposerModel(o));
    act(() => result.current.handleModelChange('prov/other'));
    expect(o.setSelectedModel).toHaveBeenLastCalledWith('prov/other');
    expect(o.setSelectedReasoning).toHaveBeenCalledWith('');
    expect(getProjectModel('/repo')).toBe('prov/other');
  });

  it('applies the agent model when the agent defines one', () => {
    const o = opts();
    const { result } = renderHook(() => useComposerModel(o));
    act(() => result.current.handleAgentChange('coder'));
    expect(o.setSelectedAgent).toHaveBeenCalledWith('coder');
    expect(o.setSelectedModel).toHaveBeenLastCalledWith('prov/agent-model');
    vi.mocked(o.setSelectedModel).mockClear();
    act(() => result.current.handleAgentChange('unknown'));
    expect(o.setSelectedModel).not.toHaveBeenCalled();
  });
});
