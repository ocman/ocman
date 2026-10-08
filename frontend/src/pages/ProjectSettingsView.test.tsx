// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useNavigate } from 'react-router-dom';
import { ProjectSettingsView } from './ProjectSettingsView';
import styles from './ProjectSettingsView.module.css';
import { clearSettingsCache } from '../lib/projectSettingsCache';
import { act } from '@testing-library/react';

vi.mock('../lib/headerContext', () => ({ usePageTitle: vi.fn() }));
vi.mock('../lib/api', () => ({
  api: {
    projectSettings: vi.fn(),
    setProjectSettings: vi.fn(),
    prepareSession: vi.fn(),
  },
}));
import { api } from '../lib/api';

const m = api as unknown as Record<string, ReturnType<typeof vi.fn>>;
const DIR = '/src/proj';

afterEach(() => vi.clearAllMocks());

function seed(models: string[], off = false) {
  m.projectSettings.mockResolvedValue({ models, off });
  m.setProjectSettings.mockImplementation(async (_directory, nextModels, nextOff) => {
    m.projectSettings.mockResolvedValue({ models: nextModels, off: nextOff });
    return { ok: true };
  });
  m.prepareSession.mockResolvedValue({ agents: [], commands: [], models: {
    hasProviders: true, models: [
      { provider: 'anthropic', model: 'claude', modelName: 'Claude' },
      { provider: 'openai', model: 'gpt', modelName: 'GPT' },
    ],
  } });
}

function ChangeOwner() {
  const navigate = useNavigate();
  return <button onClick={() => navigate(`/project/${encodeURIComponent(DIR)}/settings?remoteId=B`)}>Switch owner</button>;
}

function renderUI(remoteId = 'local') {
  return render(
    <MemoryRouter initialEntries={[`/project/${encodeURIComponent(DIR)}/settings?remoteId=${remoteId}`]}>
      <Routes><Route path="/project/:dir/settings" element={<ProjectSettingsView />} /></Routes>
      <ChangeOwner />
    </MemoryRouter>,
  );
}

const items = () => within(screen.getByRole('list', { name: 'Project models' }))
  .getAllByRole('listitem').map((li) => li.querySelector(`.${styles.name}`)?.textContent);

describe('ProjectSettingsView', () => {
  it('accepts external fallback revisions while idle and preserves them in the next write', async () => {
    seed(['a/one']);
    renderUI();
    await screen.findByRole('button', { name: 'Remove a/one' });
    m.projectSettings.mockResolvedValue({ models: ['a/one', 'external/model'], off: true });
    act(() => clearSettingsCache());
    await screen.findByRole('button', { name: 'Remove external/model' });
    expect(screen.getByTestId('project-fallthrough-off')).toBeChecked();
    fireEvent.click(screen.getByRole('button', { name: 'Remove a/one' }));
    await waitFor(() => expect(m.setProjectSettings).toHaveBeenCalledWith(DIR, ['external/model'], true, 'local'));
  });

  it('makes every remote fallback mutation read-only while leaving startup defaults editable', async () => {
    seed(['a/one', 'b/two']);
    renderUI('B');
    await screen.findByRole('table', { name: 'Project settings' });
    expect(screen.getByRole('note')).toHaveTextContent('Fallback model lists are shared by project path');
    for (const button of screen.getAllByRole('button', { name: /Move |Remove |Clear list/ })) expect(button).toBeDisabled();
    expect(screen.getByRole('combobox', { name: 'Add model' })).toBeDisabled();
    expect(screen.getByTestId('project-fallthrough-off')).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Edit project defaults' })).toBeEnabled();
    fireEvent.click(screen.getByRole('button', { name: 'Clear list' }));
    expect(m.setProjectSettings).not.toHaveBeenCalled();
    expect(m.prepareSession).not.toHaveBeenCalled();
  });

  it('reconciles after a failed save without hiding the failure', async () => {
    seed(['a/one']);
    m.setProjectSettings.mockRejectedValue(new Error('Save failed'));
    renderUI();
    fireEvent.click(await screen.findByRole('button', { name: 'Clear list' }));
    await waitFor(() => expect(m.projectSettings).toHaveBeenCalledTimes(2));
    expect(screen.getByRole('alert')).toHaveTextContent('Save failed');
    expect(items()).toEqual(['a/one']);
  });
  it('shows explicit and inherited defaults in the shared settings table and updates after a defaults save', async () => {
    seed([]);
    m.projectSettings.mockResolvedValue({ models: [], off: false, defaults: { model: 'custom/model', agent: '', worktree: 'current', permissionMode: 'plan' } });
    renderUI('B');
    const table = await screen.findByRole('table', { name: 'Project settings' });
    expect(within(table).getByRole('row', { name: /Default model/ })).toHaveTextContent('custom/model');
    expect(within(table).getByRole('row', { name: /Default agent/ })).toHaveTextContent('Use inherited default');
    expect(within(table).getByRole('row', { name: /Default worktree behavior/ })).toHaveTextContent('Current checkout');
    expect(within(table).getByRole('row', { name: /Default permission mode/ })).toHaveTextContent('Plan only');
    expect(screen.getByRole('button', { name: 'Edit project defaults' })).toBeEnabled();
    m.projectSettings.mockResolvedValue({ models: [], off: false, defaults: { model: 'new/model', agent: 'build', worktree: 'worktree', permissionMode: '' } });
    act(() => clearSettingsCache());
    await waitFor(() => expect(within(table).getByRole('row', { name: /Default model/ })).toHaveTextContent('new/model'));
    expect(within(table).getByRole('row', { name: /Default worktree behavior/ })).toHaveTextContent('New worktree when available');
  });

  it('reads catalogs and writes fallback models explicitly on the local owner', async () => {
    seed(['a/one']);
    renderUI();
    fireEvent.click(await screen.findByRole('button', { name: 'Clear list' }));
    await waitFor(() => expect(m.setProjectSettings).toHaveBeenCalledWith(DIR, [], false, 'local'));
    expect(m.projectSettings).toHaveBeenCalledWith(DIR, expect.any(AbortSignal), 'local');
    expect(m.prepareSession).toHaveBeenCalledWith({ directory: DIR, remoteId: 'local' }, expect.any(AbortSignal));
  });

  it('remounts rows and catalog state when the owner changes at the same path', async () => {
    seed(['local/model']);
    m.projectSettings.mockImplementation((_dir, _signal, owner) => Promise.resolve({ models: [owner === 'B' ? 'remote/model' : 'local/model'], off: false }));
    renderUI();
    await screen.findByRole('button', { name: 'Remove local/model' });
    fireEvent.click(screen.getByRole('button', { name: 'Switch owner' }));
    await screen.findByRole('button', { name: 'Remove remote/model' });
    expect(screen.queryByRole('button', { name: 'Remove local/model' })).not.toBeInTheDocument();
  });

  it('ignores old-owner settings and catalogs arriving after an owner switch', async () => {
    seed([]);
    let settings!: (value: unknown) => void;
    let catalog!: (value: unknown) => void;
    m.projectSettings.mockImplementation((_dir, _signal, owner) => owner === 'B'
      ? Promise.resolve({ models: ['remote/model'], off: false }) : new Promise((resolve) => { settings = resolve; }));
    m.prepareSession.mockImplementation(({ remoteId }) => remoteId === 'B'
      ? Promise.resolve({ models: { models: [{ provider: 'remote', model: 'choice' }] } }) : new Promise((resolve) => { catalog = resolve; }));
    renderUI();
    fireEvent.click(screen.getByRole('button', { name: 'Switch owner' }));
    await screen.findByRole('button', { name: 'Remove remote/model' });
    await act(async () => {
      settings({ models: ['local/model'], off: true });
      catalog({ models: { models: [{ provider: 'local', model: 'choice' }] } });
    });
    expect(items()).toEqual(['remote/model']);
    expect(screen.getByRole('combobox', { name: 'Add model' })).toBeDisabled();
    expect(m.prepareSession.mock.calls.every((call) => call[0].remoteId === 'local')).toBe(true);
    expect(screen.queryByRole('option', { name: 'local / choice' })).not.toBeInTheDocument();
  });

  it('preserves an optimistic fallback edit while defaults are refreshed', async () => {
    seed(['a/one', 'b/two']);
    let finish!: () => void;
    m.setProjectSettings.mockImplementation(() => new Promise<void>((resolve) => { finish = resolve; }));
    renderUI();
    fireEvent.click(await screen.findByRole('button', { name: 'Remove a/one' }));
    expect(items()).toEqual(['b/two']);
    act(() => clearSettingsCache());
    await waitFor(() => expect(m.projectSettings).toHaveBeenCalledTimes(2));
    expect(items()).toEqual(['b/two']);
    expect(screen.getByRole('button', { name: 'Edit project defaults' })).toBeDisabled();
    m.projectSettings.mockResolvedValue({ models: ['b/two'], off: false });
    await act(async () => finish());
    await waitFor(() => expect(screen.getByRole('button', { name: 'Edit project defaults' })).toBeEnabled());
  });

  it('fences a read started before a mutation and reconciles after it settles', async () => {
    seed(['a/one', 'b/two']);
    renderUI();
    await screen.findByRole('button', { name: 'Remove a/one' });
    let read!: (value: unknown) => void;
    let finish!: () => void;
    m.projectSettings.mockImplementationOnce(() => new Promise((resolve) => { read = resolve; }));
    m.setProjectSettings.mockImplementation(() => new Promise<void>((resolve) => { finish = resolve; }));
    act(() => clearSettingsCache());
    await waitFor(() => expect(read).toBeTypeOf('function'));
    fireEvent.click(screen.getByRole('button', { name: 'Remove a/one' }));
    await act(async () => read({ models: ['a/one', 'b/two'], off: true }));
    expect(items()).toEqual(['b/two']);
    expect(screen.getByTestId('project-fallthrough-off')).not.toBeChecked();
    m.projectSettings.mockResolvedValue({ models: ['b/two', 'external/model'], off: true });
    await act(async () => finish());
    await screen.findByRole('button', { name: 'Remove external/model' });
    expect(items()).toEqual(['b/two', 'external/model']);
    expect(screen.getByTestId('project-fallthrough-off')).toBeChecked();
  });

  it('retries a disconnected owner without reading or writing local settings', async () => {
    seed([]);
    m.projectSettings.mockRejectedValueOnce(new Error('Remote disconnected'));
    renderUI('B');
    expect(await screen.findByRole('alert')).toHaveTextContent('Remote disconnected');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await screen.findByRole('table', { name: 'Project settings' });
    expect(m.projectSettings.mock.calls.every((call) => call[2] === 'B')).toBe(true);
    expect(m.setProjectSettings).not.toHaveBeenCalled();
  });

  it('surfaces catalog errors and retries on the same owner', async () => {
    seed([]);
    m.prepareSession.mockRejectedValueOnce(new Error('Catalog unavailable'));
    renderUI();
    expect(await screen.findByRole('alert')).toHaveTextContent('Catalog unavailable');
    fireEvent.click(screen.getByRole('button', { name: 'Retry' }));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(m.prepareSession.mock.calls.every((call) => call[0].remoteId === 'local')).toBe(true);
  });
  it('explains the empty state and adds a model from the session catalogue', async () => {
    seed([]);
    renderUI();
    expect(await screen.findByTestId('project-models-empty')).toBeInTheDocument();
    expect((screen.getByTestId('project-fallthrough-off') as HTMLInputElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole('combobox', { name: 'Add model' }));
    fireEvent.click(await screen.findByRole('option', { name: 'anthropic / Claude' }));
    await waitFor(() => expect(m.setProjectSettings).toHaveBeenCalledWith(DIR, ['anthropic/claude'], false, 'local'));
    expect(items()).toEqual(['anthropic/claude']);
    expect(within(screen.getAllByRole('listitem')[0]).getByTestId('project-default-badge')).toHaveTextContent('Project default');
  });

  it('uses owner-prepared historical models when no provider is running', async () => {
    seed([]);
    m.prepareSession.mockResolvedValue({ models: { hasProviders: false, models: [{ provider: 'hist', model: 'old' }] } });
    renderUI();
    fireEvent.click(await screen.findByRole('combobox', { name: 'Add model' }));
    expect(await screen.findByRole('option', { name: 'hist / old' })).toBeInTheDocument();
  });

  it('reorders, moving the project default label with the first entry', async () => {
    seed(['a/one', 'b/two']);
    renderUI();
    const up = await screen.findByRole('button', { name: 'Move b/two up' });
    expect(up).toHaveClass('oc-icon-button');
    fireEvent.click(up);
    await waitFor(() => expect(m.setProjectSettings).toHaveBeenCalledWith(DIR, ['b/two', 'a/one'], false, 'local'));
    expect(items()).toEqual(['b/two', 'a/one']);
    expect(within(screen.getAllByRole('listitem')[0]).getByTestId('project-default-badge')).toBeInTheDocument();
  });

  it('blocks overlapping model edits while a save is pending', async () => {
    seed(['a/one', 'b/two']);
    let finish!: () => void;
    m.setProjectSettings.mockImplementation(() => new Promise<void>(resolve => { finish = resolve; }));
    renderUI();
    fireEvent.click(await screen.findByRole('button', { name: 'Move b/two up' }));
    expect(screen.getByRole('button', { name: 'Clear list' })).toBeDisabled();
    expect(screen.getByRole('button', { name: 'Remove a/one' })).toBeDisabled();
    expect(screen.getByRole('combobox', { name: 'Add model' })).toBeDisabled();
    expect(screen.getByTestId('project-fallthrough-off')).toBeDisabled();
    fireEvent.click(screen.getByRole('button', { name: 'Clear list' }));
    expect(m.setProjectSettings).toHaveBeenCalledOnce();
    m.projectSettings.mockResolvedValue({ models: ['b/two', 'a/one'], off: false });
    finish();
    await waitFor(() => expect(screen.getByRole('button', { name: 'Clear list' })).toBeEnabled());
  });

  it('clears the list', async () => {
    seed(['a/one', 'b/two'], true);
    renderUI();
    fireEvent.click(await screen.findByRole('button', { name: 'Clear list' }));
    await waitFor(() => expect(m.setProjectSettings).toHaveBeenCalledWith(DIR, [], true, 'local'));
    expect(await screen.findByTestId('project-models-empty')).toBeInTheDocument();
  });

  it('removes a single model', async () => {
    seed(['a/one', 'b/two']);
    renderUI();
    fireEvent.click(await screen.findByRole('button', { name: 'Remove a/one' }));
    await waitFor(() => expect(m.setProjectSettings).toHaveBeenCalledWith(DIR, ['b/two'], false, 'local'));
  });

  it('toggles fallthrough off without touching the list', async () => {
    seed(['a/one', 'b/two']);
    renderUI();
    const toggle = await screen.findByTestId('project-fallthrough-off');
    fireEvent.click(toggle);
    await waitFor(() => expect(m.setProjectSettings).toHaveBeenCalledWith(DIR, ['a/one', 'b/two'], true, 'local'));
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
