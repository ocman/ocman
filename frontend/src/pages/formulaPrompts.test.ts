import { describe, expect, it } from 'vitest';
import { getFormulaPrompt, setFormulaPrompt } from './formulaPrompts';

describe('Formula prompt source editing', () => {
  it('round-trips multiline prompts with quotes without changing the graph', () => {
    const source = 'version = 1\nname = "Example"\n\n[[issue]]\nkey = "plan"\nkind = "plan"\n';
    const text = 'Ask "why".\nThen test C:\\project. $&';
    const edited = setFormulaPrompt(source, 'planning', text);
    expect(edited).toContain(source);
    expect(getFormulaPrompt(edited, 'planning', 'fallback')).toBe(text);
    expect(getFormulaPrompt(edited, 'delivery', 'fallback')).toBe('fallback');
    const changed = setFormulaPrompt(edited, 'planning', 'Changed $&');
    expect(changed.match(/prompt_planning/g)).toHaveLength(1);
    expect(getFormulaPrompt(changed, 'planning', '')).toBe('Changed $&');
  });

  it('supports a header without tables and leaves malformed input visible', () => {
    expect(getFormulaPrompt(setFormulaPrompt('version = 1', 'delivery', 'Ship'), 'delivery', '')).toBe('Ship');
    expect(getFormulaPrompt('prompt_delivery = "unfinished', 'delivery', '')).toBe('"unfinished');
    expect(getFormulaPrompt('prompt_delivery = 123', 'delivery', '')).toBe('123');
    expect(getFormulaPrompt('[[issue]]\nprompt_delivery = "invalid table key"', 'delivery', 'default')).toBe('default');
  });
});
