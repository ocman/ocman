import { describe, expect, it } from 'vitest';
import { draftDefaultModel, prepareDraftCatalog } from './draftCatalog';
import type { PrepareSessionResponse } from '../../lib/api';

const catalog: PrepareSessionResponse = {
  platform: 'claudecode', liveConnection: true, defaultAgent: '', defaultModel: 'anthropic/sonnet',
  agents: [{ name: 'reviewer', mode: 'subagent' }, { name: 'custom', mode: 'primary' }], commands: [],
  models: { hasProviders: true, models: [{ provider: 'anthropic', model: 'sonnet' }] },
};

describe('draft catalog defaults', () => {
  it('allows supported custom defaults beyond catalog aliases and supported primary agents', () => {
    const result = prepareDraftCatalog(catalog, { models: [], off: false, defaultAgent: 'custom',
      defaults: { model: 'anthropic/custom-deployment', agent: 'custom', worktree: 'current' } });
    expect(result.projectDefaultModel).toBe('anthropic/custom-deployment');
    expect(result.defaultAgent).toBe('custom');
    expect(draftDefaultModel(result, 'anthropic/sonnet')).toBe('anthropic/custom-deployment');
  });

  it('rejects implicit subagent and foreign-provider defaults and falls back to the prepared project default', () => {
    const result = prepareDraftCatalog({ ...catalog, projectDefaultModel: 'anthropic/other' }, { models: [], off: false,
      defaultAgent: 'reviewer', defaults: { model: 'openai/gpt-4.1', agent: 'reviewer', worktree: '' } });
    expect(result.projectDefaultModel).toBe('anthropic/other');
    expect(result.defaultAgent).toBe('');
  });

  it('rejects an unavailable provider and retains unknown availability when there is no live provider catalog', () => {
    const result = prepareDraftCatalog({ ...catalog, models: { hasProviders: false, models: [{ provider: 'anthropic', model: 'sonnet', isAvailable: false }] } },
      { models: [], off: false, defaultAgent: '' });
    expect(result.models.models[0].isAvailable).toBe(true);
    expect(prepareDraftCatalog(catalog, { models: [], off: false, defaultAgent: '' }).projectDefaultModel).toBe('');
    expect(prepareDraftCatalog({ ...catalog, projectDefaultModel: 'anthropic/sonnet', models: { hasProviders: true, models: [{ provider: 'anthropic', model: 'sonnet', isAvailable: false }] } },
      { models: [], off: false, defaultAgent: '' }).projectDefaultModel).toBe('');
  });

  it('uses remembered models only from this catalog, then the prepared default, then empty', () => {
    expect(draftDefaultModel(catalog, 'anthropic/sonnet')).toBe('anthropic/sonnet');
    expect(draftDefaultModel(catalog, 'openai/gpt-4.1')).toBe('anthropic/sonnet');
    expect(draftDefaultModel(undefined, undefined)).toBe('');
    expect(draftDefaultModel({ ...catalog, defaultModel: undefined }, '')).toBe('');
  });

  it('does not inherit an unavailable remembered model from another machine', () => {
    expect(draftDefaultModel({ ...catalog, models: { hasProviders: true, models: [
      { provider: 'anthropic', model: 'sonnet', isAvailable: true },
      { provider: 'openai', model: 'gpt-4.1', isAvailable: false },
    ] } }, 'openai/gpt-4.1')).toBe('anthropic/sonnet');
  });
});
