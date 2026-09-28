// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router-dom';
import { ProjectSettingsView } from './ProjectSettingsView';

vi.mock('../lib/headerContext', () => ({ usePageTitle: vi.fn() }));
vi.mock('../lib/api', () => ({
  api: {
    projectSettings: vi.fn(),
    setProjectSettings: vi.fn(),
    sessions: vi.fn(),
    sessionModels: vi.fn(),
    models: vi.fn(),
  },
}));
import { api } from '../lib/api';

const m = api as unknown as Record<string, ReturnType<typeof vi.fn>>;
const DIR = '/src/proj';

afterEach(() => vi.clearAllMocks());

function seed(models: string[], off = false) {
  m.projectSettings.mockResolvedValue({ models, off });
  m.setProjectSettings.mockResolvedValue({ ok: true });
  m.sessions.mockResolvedValue([{ id: 'ses_a', platform: 'opencode' }]);
  m.sessionModels.mockResolvedValue({
    hasProviders: true,
    models: [
      { provider: 'anthropic', model: 'claude', modelName: 'Claude' },
      { provider: 'openai', model: 'gpt', modelName: 'GPT' },
    ],
  });
  m.models.mockResolvedValue([{ provider: 'hist', model: 'old' }]);
}

function renderUI() {
  return render(
    <MemoryRouter initialEntries={[`/project/${encodeURIComponent(DIR)}/settings`]}>
      <Routes><Route path="/project/:dir/settings" element={<ProjectSettingsView />} /></Routes>
    </MemoryRouter>,
  );
}

const items = () => within(screen.getByRole('list', { name: 'Project models' }))
  .getAllByRole('listitem').map((li) => li.querySelector('.mono')?.textContent);

describe('ProjectSettingsView', () => {
  it('explains the empty state and adds a model from the session catalogue', async () => {
    seed([]);
    renderUI();
    expect(await screen.findByTestId('project-models-empty')).toBeInTheDocument();
    expect((screen.getByTestId('project-fallthrough-off') as HTMLInputElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole('combobox', { name: 'Add model' }));
    fireEvent.click(await screen.findByRole('option', { name: 'anthropic / Claude' }));
    await waitFor(() => expect(m.setProjectSettings).toHaveBeenCalledWith(DIR, ['anthropic/claude'], false));
    expect(items()).toEqual(['anthropic/claude']);
    expect(within(screen.getAllByRole('listitem')[0]).getByTestId('project-default-badge')).toHaveTextContent('Project default');
  });

  it('falls back to historical models when the project has no session', async () => {
    seed([]);
    m.sessions.mockResolvedValue([]);
    renderUI();
    fireEvent.click(await screen.findByRole('combobox', { name: 'Add model' }));
    expect(await screen.findByRole('option', { name: 'hist/old' })).toBeInTheDocument();
    expect(m.sessionModels).not.toHaveBeenCalled();
  });

  it('reorders, moving the project default label with the first entry', async () => {
    seed(['a/one', 'b/two']);
    renderUI();
    fireEvent.click(await screen.findByRole('button', { name: 'Move b/two up' }));
    await waitFor(() => expect(m.setProjectSettings).toHaveBeenCalledWith(DIR, ['b/two', 'a/one'], false));
    expect(items()).toEqual(['b/two', 'a/one']);
    expect(within(screen.getAllByRole('listitem')[0]).getByTestId('project-default-badge')).toBeInTheDocument();
  });

  it('clears the list', async () => {
    seed(['a/one', 'b/two'], true);
    renderUI();
    fireEvent.click(await screen.findByRole('button', { name: 'Clear list' }));
    await waitFor(() => expect(m.setProjectSettings).toHaveBeenCalledWith(DIR, [], true));
    expect(await screen.findByTestId('project-models-empty')).toBeInTheDocument();
  });

  it('removes a single model', async () => {
    seed(['a/one', 'b/two']);
    renderUI();
    fireEvent.click(await screen.findByRole('button', { name: 'Remove a/one' }));
    await waitFor(() => expect(m.setProjectSettings).toHaveBeenCalledWith(DIR, ['b/two'], false));
  });

  it('toggles fallthrough off without touching the list', async () => {
    seed(['a/one', 'b/two']);
    renderUI();
    const toggle = await screen.findByTestId('project-fallthrough-off');
    fireEvent.click(toggle);
    await waitFor(() => expect(m.setProjectSettings).toHaveBeenCalledWith(DIR, ['a/one', 'b/two'], true));
    expect((toggle as HTMLInputElement).checked).toBe(true);
    expect(items()).toEqual(['a/one', 'b/two']);
  });

  it('reverts and reports a rejected save', async () => {
    seed(['a/one', 'b/two']);
    m.setProjectSettings.mockRejectedValue(new Error('invalid'));
    renderUI();
    fireEvent.click(await screen.findByRole('button', { name: 'Move b/two up' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('invalid');
    expect(items()).toEqual(['a/one', 'b/two']);
  });
});
