// @vitest-environment jsdom
import { render, screen } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import { AutoApproveSection } from './AutoApproveSection';

// An older or remote backend can answer the best-effort model lookups with a
// body that lacks the expected fields. That must not take down Settings.
const store = vi.hoisted(() => ({
  getJudgeModel: vi.fn(),
  getJudgeModelOptions: vi.fn(),
}));

vi.mock('../../lib/uiStore', () => ({
  useUiStore: (selector: (state: Record<string, unknown>) => unknown) => selector({
    autoApproveDefault: false,
    setAutoApproveDefault: vi.fn(),
    autoApproveDelayMs: 0,
    setAutoApproveDelayMs: vi.fn(),
    promptSections: [],
    setPromptSections: vi.fn(),
  }),
}));
vi.mock('../../lib/apiStore', () => ({
  useApiStore: (selector: (state: Record<string, unknown>) => unknown) => selector({
    setPromptSectionsApi: vi.fn(),
    setJudgeDelayApi: vi.fn(),
    setJudgeModelApi: vi.fn(),
    ...store,
  }),
}));

describe('AutoApproveSection reviewer model', () => {
  it('survives model lookups that return empty bodies', async () => {
    store.getJudgeModel.mockResolvedValue(undefined);
    store.getJudgeModelOptions.mockResolvedValue({});
    render(<AutoApproveSection />);
    await vi.waitFor(() => expect(store.getJudgeModelOptions).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.getByRole('combobox', { name: 'Auto-approve reviewer model' })).toHaveTextContent('Default');
  });

  it('lists the catalog and labels the built-in default', async () => {
    store.getJudgeModel.mockResolvedValue('');
    store.getJudgeModelOptions.mockResolvedValue({ models: ['anthropic/claude-haiku-4-5'], default: 'anthropic/claude-haiku-4-5' });
    render(<AutoApproveSection />);
    expect(await screen.findByRole('combobox', { name: 'Auto-approve reviewer model' })).toHaveTextContent('Default (anthropic/claude-haiku-4-5)');
  });
});
