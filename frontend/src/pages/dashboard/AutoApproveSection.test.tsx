// @vitest-environment jsdom
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { AutoApproveSection } from './AutoApproveSection';

vi.mock('./JudgeEndpointSettings', () => ({ JudgeEndpointSettings: () => null }));

// An older or remote backend can answer the best-effort model lookups with a
// body that lacks the expected fields. That must not take down Settings.
const store = vi.hoisted(() => ({
  getJudgeModel: vi.fn(),
  getJudgeModelOptions: vi.fn(),
  getPromptSections: vi.fn().mockResolvedValue([]),
  getJudgeDelay: vi.fn().mockResolvedValue(0),
  setPromptSections: vi.fn(),
  setAutoApproveDelayMs: vi.fn(),
  setPromptSectionsApi: vi.fn().mockResolvedValue(undefined),
}));
const openCode = vi.hoisted(() => ({ value: true }));
vi.mock('../../lib/useCapabilities', () => ({ useLocalOpenCode: () => openCode.value }));

vi.mock('../../lib/uiStore', () => ({
  useUiStore: (selector: (state: Record<string, unknown>) => unknown) => selector({
    autoApproveDefault: false,
    setAutoApproveDefault: vi.fn(),
    autoApproveDelayMs: 0,
    setAutoApproveDelayMs: store.setAutoApproveDelayMs,
    promptSections: [],
    setPromptSections: store.setPromptSections,
  }),
}));
vi.mock('../../lib/api', () => ({
  api: {
    setJudgeDelay: vi.fn(),
    setJudgeModel: vi.fn(),
    ...store,
    setPromptSections: store.setPromptSectionsApi,
  },
}));

describe('AutoApproveSection reviewer model', () => {
  beforeEach(() => {
    store.setPromptSections.mockClear();
    store.setPromptSectionsApi.mockClear();
  });

  it('adds a section with the shared action and saves its complete payload', async () => {
    store.getJudgeModel.mockResolvedValue('');
    store.getJudgeModelOptions.mockResolvedValue({ models: [], default: '' });
    render(<AutoApproveSection />);
    const button = screen.getByRole('button', { name: '+ Add section' });
    expect(button).toHaveClass('oc-button');
    fireEvent.click(button);
    await vi.waitFor(() => expect(store.setPromptSectionsApi).toHaveBeenCalledWith([{ title: '', content: '' }]));
    expect(store.setPromptSections).toHaveBeenCalledWith([{ title: '', content: '' }]);
  });
  it('survives model lookups that return empty bodies', async () => {
    store.getJudgeModel.mockResolvedValue(undefined);
    store.getJudgeModelOptions.mockResolvedValue({});
    render(<AutoApproveSection />);
    await vi.waitFor(() => expect(store.getJudgeModelOptions).toHaveBeenCalled());
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.getByRole('combobox', { name: 'Auto-approve reviewer model' })).toHaveTextContent('Default');
  });

  it('loads the judge settings the backend uses on mount', async () => {
    store.getJudgeModel.mockResolvedValue('');
    store.getJudgeModelOptions.mockResolvedValue({ models: [], default: '' });
    store.getPromptSections.mockResolvedValueOnce([{ title: 'Repo', content: 'rules' }]);
    store.getJudgeDelay.mockResolvedValueOnce(2500);
    render(<AutoApproveSection />);
    await vi.waitFor(() => expect(store.setPromptSections).toHaveBeenCalledWith([{ title: 'Repo', content: 'rules' }]));
    await vi.waitFor(() => expect(store.setAutoApproveDelayMs).toHaveBeenCalledWith(2500));
  });

  it('lists the catalog and labels the built-in default', async () => {
    store.getJudgeModel.mockResolvedValue('');
    store.getJudgeModelOptions.mockImplementation(() => new Promise(resolve => {
      setTimeout(() => resolve({ models: ['anthropic/claude-haiku-4-5'], default: 'anthropic/claude-haiku-4-5' }), 50);
    }));
    render(<AutoApproveSection />);
    expect(screen.getByRole('combobox', { name: 'Auto-approve reviewer model' })).toHaveTextContent(/^Default$/);
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Auto-approve reviewer model' })).toHaveTextContent('Default (anthropic/claude-haiku-4-5)'));
  });

  it('hides the OpenCode reviewer model without OpenCode', async () => {
    openCode.value = false;
    try {
      store.getJudgeModel.mockResolvedValue('');
      store.getJudgeModelOptions.mockResolvedValue({ models: [], default: '' });
      render(<AutoApproveSection />);
      await vi.waitFor(() => expect(store.getJudgeModelOptions).toHaveBeenCalled());
      expect(screen.queryByRole('combobox', { name: 'Auto-approve reviewer model' })).not.toBeInTheDocument();
    } finally {
      openCode.value = true;
    }
  });
});
