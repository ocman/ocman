import { describe, expect, it } from 'vitest';
import type { SessionModelEntry } from '../../lib/api';
import { describeModel } from './composerModel';

const entries = [
  { provider: 'anthropic', model: 'claude', modelName: 'Claude', isAvailable: true, reasoning: ['low', 'high'] },
  { provider: 'openai', model: 'gpt', isAvailable: false },
] as SessionModelEntry[];

describe('describeModel', () => {
  it('describes known, unavailable, and unknown models', () => {
    expect(describeModel('', entries)).toEqual({ label: '', unavailable: false, reasoningOptions: [] });
    expect(describeModel('anthropic/claude', entries)).toEqual({ label: 'Claude', unavailable: false, reasoningOptions: ['low', 'high'] });
    expect(describeModel('openai/gpt', entries)).toEqual({ label: 'gpt', unavailable: true, reasoningOptions: [] });
    expect(describeModel('x/unknown-model', entries)).toEqual({ label: 'unknown-model', unavailable: false, reasoningOptions: [] });
    expect(describeModel('bare', undefined).label).toBe('bare');
  });
});
