// @vitest-environment jsdom
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import { api, fetchJSON, postJSON } from '../lib/api';
import { clearSettingsCache, loadProjectSettings } from '../lib/projectSettingsCache';
import { ProjectQuickSettings } from './ProjectQuickSettings';

vi.mock('../lib/api', () => ({ api: { prepareSession: vi.fn() }, fetchJSON: vi.fn(), postJSON: vi.fn() }));
beforeEach(() => {
  vi.resetAllMocks();
  clearSettingsCache();
  vi.mocked(fetchJSON).mockResolvedValue({ models: ['p/fallback'], off: true, defaultAgent: 'build' });
  vi.mocked(api.prepareSession).mockResolvedValue({ platform: 'opencode', agents: [{ name: 'plan' }, { name: 'helper', mode: 'subagent' }], commands: [],
    models: { models: [{ provider: 'p', model: 'm' }] } } as never);
  vi.mocked(postJSON).mockResolvedValue({ ok: true });
});
async function open() {
  await userEvent.click(screen.getByRole('button', { name: 'Project quick settings' }));
  await screen.findByRole('combobox', { name: 'Default model' });
}
it('loads on demand, saves only defaults, and invalidates cached settings', async () => {
  render(<ProjectQuickSettings directory="/src/.worktrees/repo/wt" remoteId="machine">repo</ProjectQuickSettings>);
  expect(api.prepareSession).not.toHaveBeenCalled();
  await open();
  expect(api.prepareSession).toHaveBeenCalledWith({ directory: '/src/.worktrees/repo/wt', remoteId: 'machine' }, expect.any(AbortSignal));
  expect(fetchJSON).toHaveBeenCalledWith('/api/project/settings?dir=%2Fsrc%2Frepo&remoteId=machine');
  await userEvent.click(screen.getByRole('combobox', { name: 'Default model' }));
  await userEvent.click(screen.getByRole('option', { name: 'p / m' }));
  await userEvent.click(screen.getByRole('combobox', { name: 'Default agent' }));
  expect(screen.queryByRole('option', { name: 'helper' })).not.toBeInTheDocument();
  await userEvent.click(screen.getByRole('option', { name: 'plan' }));
  await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Default worktree behavior' }), 'current');
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(postJSON).toHaveBeenCalledWith('/api/project/settings', { directory: '/src/.worktrees/repo/wt', remoteId: 'machine', defaults: { model: 'p/m', agent: 'plan', worktree: 'current' } });
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  expect(screen.getByRole('button', { name: 'Project quick settings' })).toHaveFocus();
  await open();
  expect(fetchJSON).toHaveBeenCalledTimes(2);
});
it('preserves unavailable saved values, supports reset, and retains edits on save failure', async () => {
  vi.mocked(fetchJSON).mockResolvedValue({ defaults: { model: 'old/model', agent: 'custom', worktree: 'worktree' } });
  vi.mocked(postJSON).mockRejectedValueOnce(new Error('save offline'));
  render(<ProjectQuickSettings directory="/repo">repo</ProjectQuickSettings>);
  await open();
  expect(screen.getByRole('combobox', { name: 'Default model' })).toHaveTextContent('old/model');
  expect(screen.getByRole('combobox', { name: 'Default agent' })).toHaveTextContent('custom');
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('save offline');
  for (const name of ['Default model', 'Default agent']) {
    await userEvent.click(screen.getByRole('combobox', { name }));
    await userEvent.click(within(screen.getByRole('listbox')).getByRole('option', { name: 'Use inherited default' }));
  }
  await userEvent.selectOptions(screen.getByRole('combobox', { name: 'Default worktree behavior' }), '');
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(postJSON).toHaveBeenLastCalledWith('/api/project/settings', { directory: '/repo', remoteId: 'local', defaults: { model: '', agent: '', worktree: '' } });
});
it('retries a failed load and cancels without saving', async () => {
  vi.mocked(fetchJSON).mockRejectedValueOnce(new Error('load offline'));
  render(<ProjectQuickSettings directory="/repo">repo</ProjectQuickSettings>);
  await userEvent.click(screen.getByRole('button', { name: 'Project quick settings' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('load offline');
  await userEvent.click(screen.getByRole('button', { name: 'Retry' }));
  await screen.findByRole('combobox', { name: 'Default model' });
  await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
  expect(postJSON).not.toHaveBeenCalled();
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});
it('dismisses on Escape or outside click and cancels obsolete preparation', async () => {
  const view = render(<ProjectQuickSettings directory="/repo">repo</ProjectQuickSettings>);
  await open();
  await userEvent.keyboard('{Escape}');
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  await open();
  await userEvent.click(document.body);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  let finish!: (value: unknown) => void;
  vi.mocked(api.prepareSession).mockReturnValueOnce(new Promise((resolve) => { finish = (value) => resolve(value as never); }));
  await userEvent.click(screen.getByRole('button', { name: 'Project quick settings' }));
  expect(screen.getByRole('status')).toHaveTextContent('Loading');
  const signal = vi.mocked(api.prepareSession).mock.calls.at(-1)![1]!;
  view.rerender(<ProjectQuickSettings directory="/other">other</ProjectQuickSettings>);
  await waitFor(() => expect(signal.aborted).toBe(true));
  await act(async () => finish({ agents: [], models: { models: [] } }));
  expect(screen.getByRole('combobox', { name: 'Default model' })).toHaveTextContent('Use inherited default');
});

it.each([10, 1000])('keeps the popover in the viewport when its header anchor is at %s', async (left) => {
  render(<ProjectQuickSettings directory="/repo">repo</ProjectQuickSettings>);
  vi.spyOn(screen.getByRole('button', { name: 'Project quick settings' }), 'getBoundingClientRect').mockReturnValue({ left, bottom: 50 } as DOMRect);
  await open();
  const dialog = screen.getByRole('dialog');
  expect(dialog.parentElement).toBe(document.body);
  expect(dialog).toHaveClass('oc-popover');
  expect(dialog).toHaveStyle({ top: '58px', left: `${Math.max(12, Math.min(left, window.innerWidth - 352))}px` });
  act(() => window.dispatchEvent(new Event('resize')));
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
});

it.each([true, false])('keeps a reopened form and its edits when an earlier dismissed save completes: success=%s', async (success) => {
  let finish!: () => void;
  vi.mocked(postJSON).mockReturnValueOnce(new Promise((resolve, reject) => {
    finish = () => success ? resolve({ ok: true }) : reject(new Error('Old save failed'));
  }));
  render(<ProjectQuickSettings directory="/repo">repo</ProjectQuickSettings>);
  await open();
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));
  await userEvent.keyboard('{Escape}');
  await open();
  await userEvent.click(screen.getByRole('combobox', { name: 'Default model' }));
  await userEvent.click(screen.getByRole('option', { name: 'p / m' }));
  await act(async () => finish());
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  expect(screen.getByRole('combobox', { name: 'Default model' })).toHaveTextContent('p / m');
  expect(screen.queryByRole('alert')).not.toBeInTheDocument();
  // The persisted mutation still invalidates cached preferences after dismissal.
  await loadProjectSettings('/repo');
  expect(fetchJSON).toHaveBeenCalledTimes(success ? 2 : 1);
});

it.each([
  ['Default model', 'Search models', 'p'],
  ['Default agent', 'Search agents', 'plan'],
])('does not submit on Enter in %s search, but lets Enter activate Save', async (picker, search, query) => {
  render(<ProjectQuickSettings directory="/repo">repo</ProjectQuickSettings>);
  await open();
  await userEvent.click(screen.getByRole('combobox', { name: picker }));
  await userEvent.type(screen.getByRole('textbox', { name: search }), query);
  await userEvent.keyboard('{Enter}');
  expect(postJSON).not.toHaveBeenCalled();
  expect(screen.getByRole('dialog')).toBeInTheDocument();
  await userEvent.click(screen.getByRole('combobox', { name: picker }));
  screen.getByRole('button', { name: 'Save' }).focus();
  await userEvent.keyboard('{Enter}');
  expect(postJSON).toHaveBeenCalledOnce();
});

it.each([false, true])('offers startup agents for an empty catalog and retains custom choices: saved=%s', async (saved) => {
  vi.mocked(api.prepareSession).mockResolvedValue({ platform: 'opencode', agents: [], commands: [], models: { models: [] } } as never);
  vi.mocked(fetchJSON).mockResolvedValue({ models: [], off: false, defaultAgent: 'default-custom',
    ...(saved ? { defaults: { model: '', agent: 'saved-custom', worktree: '' } } : {}),
  });
  render(<ProjectQuickSettings directory="/repo">repo</ProjectQuickSettings>);
  await open();
  await userEvent.click(screen.getByRole('combobox', { name: 'Default agent' }));
  expect(screen.getByRole('option', { name: 'build' })).toBeInTheDocument();
  expect(screen.getByRole('option', { name: 'default-custom' })).toBeInTheDocument();
  if (saved) expect(screen.getByRole('option', { name: 'saved-custom' })).toHaveAttribute('aria-selected', 'true');
  await userEvent.click(screen.getByRole('option', { name: 'plan' }));
  await userEvent.click(screen.getByRole('button', { name: 'Save' }));
  expect(postJSON).toHaveBeenCalledWith('/api/project/settings', { directory: '/repo', remoteId: 'local', defaults: { model: '', agent: 'plan', worktree: '' } });
});
