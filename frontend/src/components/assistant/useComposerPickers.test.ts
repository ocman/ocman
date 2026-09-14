// @vitest-environment jsdom
import { act, renderHook } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { AgentInfo } from '../../lib/api';
import { useComposerPickers, type UseComposerPickersOptions } from './useComposerPickers';

function setup(over: Partial<UseComposerPickersOptions> = {}) {
  const el = document.createElement('textarea');
  document.body.appendChild(el);
  const o: UseComposerPickersOptions = {
    inputRef: { current: el },
    sessionIdRef: { current: 's1' },
    scheduleDraftSave: vi.fn(),
    models: ['anthropic/claude-opus', 'openai/gpt-5', 'other/gpt-5'],
    agents: [{ name: 'build' } as AgentInfo],
    agentOptions: ['build', 'plan'],
    onModelChange: vi.fn(),
    onAgentChange: vi.fn(),
    onRefreshModels: vi.fn(),
    ...over,
  };
  return { o, el, ...renderHook(() => useComposerPickers(o)) };
}

describe('useComposerPickers', () => {
  it('applies a unique model/agent arg directly, otherwise opens the picker', () => {
    const { o, result } = setup();
    act(() => result.current.openModelPicker('Claude-Opus'));
    expect(o.onModelChange).toHaveBeenCalledWith('anthropic/claude-opus');
    expect(result.current.model.open).toBe(false);

    act(() => result.current.openModelPicker('gpt-5')); // ambiguous
    expect(result.current.model.open).toBe(true);
    expect(result.current.model.query).toBe('gpt-5');
    expect(o.onRefreshModels).toHaveBeenCalled();
    act(() => result.current.model.close());
    expect(result.current.model.open).toBe(false);

    act(() => result.current.openAgentPicker('PLAN'));
    expect(o.onAgentChange).toHaveBeenCalledWith('plan');
    act(() => result.current.openAgentPicker('nope'));
    expect(result.current.agent.open).toBe(true);
    expect(result.current.agent.query).toBe('nope');
  });

  it('inserts skill/routine text into the textarea and saves the draft', () => {
    const { o, el, result } = setup();
    act(() => result.current.openSkillPicker('re'));
    expect(result.current.skill).toMatchObject({ open: true, query: 're' });
    act(() => result.current.insertSkill('research'));
    expect(el.value).toBe('/research ');
    expect(result.current.skill.open).toBe(false);
    expect(o.scheduleDraftSave).toHaveBeenCalledWith('s1', expect.any(Function));

    act(() => result.current.openRoutinePicker(''));
    act(() => result.current.insertRoutine({ prompt: 'do the thing' }));
    expect(el.value).toBe('do the thing');
    expect(result.current.routine.open).toBe(false);

    act(() => result.current.help.setOpen(true));
    act(() => result.current.reasoning.setOpen(true));
    expect(result.current.help.open && result.current.reasoning.open).toBe(true);
    act(() => { result.current.help.close(); result.current.reasoning.close(); });
    expect(result.current.help.open || result.current.reasoning.open).toBe(false);
    expect(document.activeElement).toBe(el);
  });
});
