// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { api, type Routine } from '../lib/api';
import { Routines } from './Routines';
import type { WebhookInbox } from '../lib/api.types';

vi.mock('../lib/headerContext', () => ({ usePageTitle: vi.fn() }));
vi.mock('../lib/api', () => ({ api: { projects: vi.fn(), sessions: vi.fn(), agents: vi.fn(), sessionModels: vi.fn(), routines: { list: vi.fn(), create: vi.fn(), update: vi.fn(), remove: vi.fn(), run: vi.fn(), history: vi.fn() }, webhookInboxes: { list: vi.fn(), deliveries: vi.fn(), subscribe: vi.fn(), unsubscribe: vi.fn() } } }));

const routine: Routine = {
  id: 'routine-1', name: 'Morning check', prompt: 'Inspect the build', directory: '/repo', remoteId: 'local',
  agent: 'plan', model: 'anthropic/claude-sonnet-4',
  sessionMode: 'new', sessionId: '',
  scheduleKind: 'cron', scheduleConfigJSON: '{"cron":"0 9 * * *","timezone":"Europe/Brussels"}', permissionRulesJSON: '[]', nextDueAt: 2_000_000,
  enabled: true, deleted: false, deleteAfterSuccess: false, archiveSessionAfterSuccess: true, notifyOnSuccess: true, createdAt: 1_000, updatedAt: 1_000,
};

const inbox: WebhookInbox = {
  id: 'inbox-1', name: 'forgejo', relayUrl: 'https://relay', ingestionUrl: '/i/inbox/token', keyVersion: 2, createdAt: 1_000, secretHeader: '', counts: { terminal: 1, failure: 2 },
  subscriptions: [{ id: 'sub-1', inboxId: 'inbox-1', routineId: 'routine-1', headerPredicates: '{"x-forgejo-event":{"equals":"pull_request"}}', jsonPredicates: '{"/action":{"oneOf":["opened","synchronized"]}}', createdAt: 1 }],
};

describe('Routines', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.projects).mockResolvedValue([{ directory: '/repo', archived: false } as never]);
    vi.mocked(api.sessions).mockResolvedValue([{ id: 'catalog', title: 'Catalog source', directory: '/repo', remoteId: 'local', archived: false } as never]);
    vi.mocked(api.agents).mockResolvedValue([{ name: 'build' }, { name: 'plan' }]);
    vi.mocked(api.sessionModels).mockResolvedValue({ hasProviders: true, models: [{ provider: 'openai', model: 'gpt-5.4' }, { provider: 'anthropic', model: 'claude-sonnet-4' }] });
    vi.mocked(api.routines.list).mockResolvedValue([routine]);
    vi.mocked(api.routines.history).mockResolvedValue([{ id: 'run-1', routineId: routine.id, routineUpdatedAt: routine.updatedAt, routineName: routine.name, prompt: routine.prompt, directory: '/repo', remoteId: 'local', agent: routine.agent, model: routine.model, sessionMode: 'new', targetSessionId: '', trigger: 'manual', platform: 'opencode', sessionId: 'session-1', state: 'failure', error: 'agent stopped', occurrenceAt: 1_000, createdAt: 1_000 }]);
    vi.mocked(api.routines.create).mockResolvedValue(routine);
    vi.mocked(api.routines.update).mockResolvedValue(routine);
    vi.mocked(api.routines.remove).mockResolvedValue(undefined);
    vi.mocked(api.routines.run).mockResolvedValue({} as never);
    vi.mocked(api.webhookInboxes.list).mockResolvedValue([]);
    vi.mocked(api.webhookInboxes.deliveries).mockResolvedValue([]);
    vi.mocked(api.webhookInboxes.subscribe).mockResolvedValue({} as never);
    vi.mocked(api.webhookInboxes.unsubscribe).mockResolvedValue(undefined);
  });

  afterEach(() => vi.useRealTimers());

  it('creates a targeted timeout routine and exposes every schedule form', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);
    await screen.findByText('Morning check');
    await user.click(screen.getByRole('button', { name: 'New routine' }));
    expect(screen.getByRole('dialog', { name: 'New routine' })).toBeInTheDocument();
    await user.type(screen.getByLabelText('Name'), 'Deploy check');
    await user.type(screen.getByLabelText('Prompt'), 'Check production');
    await user.click(screen.getByRole('combobox', { name: 'Project' }));
    await user.click(screen.getByRole('option', { name: '/repo' }));
    await user.click(screen.getByText('Session and model'));
    await waitFor(() => expect(screen.getByRole('combobox', { name: 'Agent' })).toBeEnabled());
    await user.click(screen.getByRole('combobox', { name: 'Agent' }));
    await user.click(screen.getByRole('option', { name: 'build' }));
    await user.click(screen.getByRole('combobox', { name: 'Model' }));
    await user.click(screen.getByRole('option', { name: 'openai/gpt-5.4' }));
    await user.selectOptions(screen.getByLabelText('Trigger'), 'once');
    expect(screen.getByLabelText('Run at')).toHaveAttribute('type', 'datetime-local');
    await user.selectOptions(screen.getByLabelText('Trigger'), 'cron');
    expect(screen.getByLabelText('Timezone')).toBeInTheDocument();
    await user.selectOptions(screen.getByLabelText('Trigger'), 'timeout');
    await user.clear(screen.getByLabelText('Minutes from now'));
    await user.type(screen.getByLabelText('Minutes from now'), '15');
    await user.click(screen.getByText('After a run'));
    await user.click(screen.getByLabelText('Delete after a successful run'));
    expect(screen.getByLabelText('Archive session after a successful run')).not.toBeChecked();
    await user.click(screen.getByLabelText('Archive session after a successful run'));
    await user.click(screen.getByRole('button', { name: 'Create routine' }));

    await waitFor(() => expect(api.routines.create).toHaveBeenCalledWith(expect.objectContaining({
      name: 'Deploy check', directory: '/repo', remoteId: 'local', agent: 'build', model: 'openai/gpt-5.4', sessionMode: 'new', sessionId: '', deleteAfterSuccess: true,
      schedule: { kind: 'timeout', timeoutMs: 900_000 },
      archiveSessionAfterSuccess: true,
      notifyOnSuccess: false, // successes stay out of the Inbox unless opted in
    })));
  }, 15_000);

  it('selects an existing project session and its host', async () => {
    vi.mocked(api.projects).mockResolvedValue([{ directory: '/repo', remoteId: 'box', remoteName: 'Build box', archived: false } as never]);
    vi.mocked(api.sessions).mockResolvedValue([
      { id: 'chosen', title: 'Release review', directory: '/repo', remoteId: 'box', remoteName: 'Build box', platform: 'r-box:opencode', archived: false } as never,
      { id: 'child', title: 'Subagent', directory: '/repo', remoteId: 'box', remoteName: 'Build box', parentId: 'chosen', archived: false } as never,
      { id: 'other', title: 'Other project', directory: '/other', remoteId: 'local', archived: false } as never,
    ]);
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);
    await screen.findByText(routine.name);
    await user.click(screen.getByRole('button', { name: 'New routine' }));
    await user.type(screen.getByLabelText('Name'), 'Continue release');
    await user.type(screen.getByLabelText('Prompt'), 'Check release status');
    await user.click(screen.getByRole('combobox', { name: 'Project' }));
    await user.click(screen.getByRole('option', { name: '/repo · Build box' }));
    await user.click(screen.getByText('Session and model'));
    await user.selectOptions(screen.getByLabelText('Session'), 'existing');
    await waitFor(() => expect(api.sessions).toHaveBeenCalledWith({ dir: '/repo' }));
    await user.click(screen.getByRole('combobox', { name: 'Existing session' }));
    expect(screen.queryByRole('option', { name: 'Subagent · Build box' })).not.toBeInTheDocument();
    await user.click(await screen.findByRole('option', { name: 'Release review · Build box' }));
    await user.click(screen.getByRole('button', { name: 'Create routine' }));
    await waitFor(() => expect(api.routines.create).toHaveBeenCalledWith(expect.objectContaining({ sessionMode: 'existing', sessionId: 'chosen', remoteId: 'box' })));
    expect(api.agents).toHaveBeenCalledWith('chosen', undefined, 'r-box:opencode');
    expect(api.sessionModels).toHaveBeenCalledWith('chosen', 'r-box:opencode');
  }, 15_000);

  it('uses the shared controls and empty state', async () => {
    vi.mocked(api.routines.list).mockResolvedValue([]);
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);

    const create = await screen.findByRole('button', { name: 'New routine' });
    expect(create).toHaveClass('oc-button', 'oc-button--accent');
    expect(screen.getByText('No routines yet.')).toHaveClass('oc-empty');

    await user.click(create);
    expect(screen.getByRole('button', { name: 'Cancel' })).toHaveClass('oc-button');
  });

  it('keeps essentials visible and optional groups collapsed until opened', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);
    await user.click(await screen.findByRole('button', { name: 'New routine' }));
    for (const name of ['Name', 'Prompt', 'Project', 'Trigger', 'Enabled']) {
      expect(screen.getByLabelText(name)).toBeVisible();
    }
    for (const name of ['Session and model', 'After a run', 'Permissions']) {
      const summary = screen.getByText(name);
      expect(summary.closest('details')).not.toHaveAttribute('open');
      await user.click(summary);
      expect(summary.closest('details')).toHaveAttribute('open');
      await user.click(summary);
      expect(summary.closest('details')).not.toHaveAttribute('open');
    }
    expect(screen.getByLabelText('Session')).not.toBeVisible();
    expect(screen.getByLabelText('Archive session after a successful run')).not.toBeVisible();
    await user.click(screen.getByText('After a run'));
    await user.click(screen.getByLabelText('Archive session after a successful run'));
    await user.click(screen.getByText('After a run'));
    await user.click(screen.getByText('After a run'));
    expect(screen.getByLabelText('Archive session after a successful run')).toBeChecked();
  });

  it('keeps the form open while saving and restores focus after completion', async () => {
    let finish!: (value: Routine) => void;
    vi.mocked(api.routines.update).mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);
    const edit = await screen.findByRole('button', { name: 'Edit' });
    await user.click(edit);
    const actions = screen.getByRole('group', { name: 'Routine form actions' });
    await user.click(within(actions).getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(api.routines.update).toHaveBeenCalled());
    expect(screen.getByRole('button', { name: 'Close routine form' })).toBeDisabled();
    expect(within(actions).getByRole('button', { name: 'Cancel' })).toBeDisabled();
    await user.keyboard('{Escape}');
    await user.click(screen.getByTestId('routine-drawer-backdrop'));
    expect(screen.getByRole('dialog', { name: 'Edit routine' })).toBeInTheDocument();
    await act(async () => finish(routine));
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    expect(edit).toHaveFocus();
  });

  it('closes the form once saved even when the refresh fails', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);
    await user.click(await screen.findByRole('button', { name: 'Edit' }));
    vi.mocked(api.webhookInboxes.list).mockRejectedValueOnce(new Error('inboxes unavailable'));
    await user.click(within(screen.getByRole('group', { name: 'Routine form actions' })).getByRole('button', { name: 'Save changes' }));
    expect(await screen.findByText('inboxes unavailable')).toBeInTheDocument();
    expect(screen.queryByRole('dialog', { name: 'Edit routine' })).not.toBeInTheDocument();
  });

  it('shows missed timeout schedules as expired', async () => {
    vi.mocked(api.routines.list).mockResolvedValue([{ ...routine, enabled: false, expiredAt: Date.now() }]);
    render(<MemoryRouter><Routines /></MemoryRouter>);
    expect(await screen.findByText('expired')).toBeInTheDocument();
  });

  it.each(['/i/inbox/token', 'https://relay/i/inbox/token'])('lists inboxes and copies a full read-only webhook URL for %s', async (ingestionUrl) => {
    const user = userEvent.setup();
    const copy = vi.spyOn(navigator.clipboard, 'writeText').mockResolvedValue(undefined);
    vi.mocked(api.webhookInboxes.list).mockResolvedValue([{ ...inbox, ingestionUrl }]);
    render(<MemoryRouter><Routines /></MemoryRouter>);

    const routineRow = await screen.findByRole('row', { name: 'View Morning check history' });
    expect(screen.getByRole('columnheader', { name: 'Trigger' })).toBeInTheDocument();
    expect(within(routineRow).getByText('cron + webhook: forgejo')).toBeInTheDocument();
    await user.click(screen.getByRole('tab', { name: 'Webhook inboxes' }));
    const row = await screen.findByRole('row', { name: 'Manage forgejo inbox' });
    expect(within(row).getByText('Morning check')).toBeInTheDocument();
    expect(within(row).getByText('terminal: 1 · failure: 2')).toBeInTheDocument();
    await user.click(row);
    expect(screen.getByLabelText('Ingestion URL')).toHaveValue('https://relay/i/inbox/token');
    expect(screen.getByLabelText('Ingestion URL')).toHaveAttribute('readonly');
    await user.click(screen.getByRole('button', { name: 'Copy URL' }));
    expect(copy).toHaveBeenCalledWith('https://relay/i/inbox/token');
    // A subscriber jumps to the routine form.
    await user.click(screen.getByRole('button', { name: 'Morning check' }));
    expect(screen.getByRole('dialog', { name: 'Edit routine' })).toBeInTheDocument();
  });

  it('opens the webhook inboxes tab from the URL', async () => {
    render(<MemoryRouter initialEntries={['/routines?tab=inboxes']}><Routines /></MemoryRouter>);

    expect(await screen.findByText('No webhook inboxes yet.')).toBeInTheDocument();
    expect(screen.getByRole('tab', { name: 'Webhook inboxes' })).toHaveAttribute('aria-selected', 'true');
    expect(screen.queryByRole('button', { name: 'New routine' })).not.toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'New inbox' })).toBeInTheDocument();
  });

  it('subscribes a routine to an inbox with filters and moves it between inboxes', async () => {
    const user = userEvent.setup();
    const other = { ...inbox, id: 'inbox-2', name: 'github', subscriptions: [] };
    vi.mocked(api.webhookInboxes.list).mockResolvedValue([inbox, other]);
    render(<MemoryRouter><Routines /></MemoryRouter>);
    await screen.findByText('Morning check');
    await user.click(screen.getByRole('button', { name: 'Edit' }));
    expect(screen.getByLabelText('Trigger')).toHaveValue('webhook:inbox-1');
    expect(screen.getByLabelText('Header conditions 1 header')).toHaveValue('x-forgejo-event');
    await user.selectOptions(screen.getByLabelText('Trigger'), 'webhook:inbox-2');
    await user.click(screen.getByRole('button', { name: 'Add JSON pointer' }));
    await user.type(screen.getByLabelText('Body conditions 2 JSON pointer'), '/draft');
    await user.selectOptions(screen.getByLabelText('Body conditions 2 operator'), 'missing');
    await user.click(screen.getByRole('button', { name: 'Remove header conditions 1' }));
    await user.click(screen.getByRole('button', { name: 'Save changes' }));

    await waitFor(() => expect(api.webhookInboxes.subscribe).toHaveBeenCalledWith('inbox-2', { routineId: routine.id, headerPredicates: '{}', jsonPredicates: '{"/action":{"oneOf":["opened","synchronized"]},"/draft":{"exists":false}}' }));
    expect(api.webhookInboxes.unsubscribe).toHaveBeenCalledWith('inbox-1', routine.id);
    // A webhook trigger replaces the schedule.
    expect(vi.mocked(api.routines.update).mock.calls[0][1].schedule).toEqual({ kind: 'none' });
  });

  it('retries a failed subscription on the routine it already created', async () => {
    const user = userEvent.setup();
    vi.mocked(api.webhookInboxes.list).mockResolvedValue([{ ...inbox, subscriptions: [] }]);
    vi.mocked(api.routines.create).mockResolvedValue({ ...routine, id: 'routine-new', name: 'Deploy hook' });
    vi.mocked(api.webhookInboxes.subscribe).mockRejectedValueOnce(new Error('subscription failed'));
    render(<MemoryRouter><Routines /></MemoryRouter>);
    await screen.findByText('Morning check');
    await user.click(screen.getByRole('button', { name: 'New routine' }));
    await user.type(screen.getByLabelText('Name'), 'Deploy hook');
    await user.type(screen.getByLabelText('Prompt'), 'Check it');
    await user.click(screen.getByRole('combobox', { name: 'Project' }));
    await user.click(screen.getByRole('option', { name: '/repo' }));
    await user.selectOptions(screen.getByLabelText('Trigger'), 'webhook:inbox-1');
    await user.click(screen.getByRole('button', { name: 'Create routine' }));
    expect(await screen.findByText('subscription failed')).toBeInTheDocument();

    // The routine exists now: the retry saves it instead of creating a second.
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(api.webhookInboxes.subscribe).toHaveBeenCalledTimes(2));
    expect(api.routines.create).toHaveBeenCalledTimes(1);
    expect(api.routines.update).toHaveBeenCalledWith('routine-new', expect.objectContaining({ name: 'Deploy hook' }));
    expect(vi.mocked(api.webhookInboxes.subscribe).mock.calls[1][0]).toBe('inbox-1');
    await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
  });

  it('removes the subscription when the trigger is cleared', async () => {
    const user = userEvent.setup();
    vi.mocked(api.webhookInboxes.list).mockResolvedValue([inbox]);
    render(<MemoryRouter><Routines /></MemoryRouter>);
    await screen.findByText('Morning check');
    await user.click(screen.getByRole('button', { name: 'Edit' }));
    await user.selectOptions(screen.getByLabelText('Trigger'), 'none');
    expect(screen.queryByLabelText('Header conditions 1 header')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(api.webhookInboxes.unsubscribe).toHaveBeenCalledWith('inbox-1', routine.id));
    expect(api.webhookInboxes.subscribe).not.toHaveBeenCalled();
  });

  it('shortens project paths in the table', async () => {
    vi.mocked(api.routines.list).mockResolvedValue([{ ...routine, directory: '/Users/dries/src/ocman' }]);
    render(<MemoryRouter><Routines /></MemoryRouter>);

    const project = await screen.findByText('src/ocman');
    expect(project).toHaveAttribute('title', '/Users/dries/src/ocman');
    expect(screen.queryByText('/Users/dries/src/ocman')).not.toBeInTheDocument();
  });

  it('clears project-specific agent and model selections when changing projects', async () => {
    vi.mocked(api.projects).mockResolvedValue([{ directory: '/repo', archived: false }, { directory: '/other', archived: false }] as never);
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);
    await user.click(await screen.findByRole('button', { name: 'Edit' }));
    await user.click(screen.getByRole('combobox', { name: 'Project' }));
    await user.click(screen.getByRole('option', { name: '/other' }));
    expect(screen.getByRole('combobox', { name: 'Agent' })).toHaveTextContent('Default agent');
    expect(screen.getByRole('combobox', { name: 'Model' })).toHaveTextContent('Default model');
  });

  it('opens history from the row and the form from Edit', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);
    const row = (await screen.findByText(routine.name)).closest('tr')!;
    expect(screen.getByRole('columnheader', { name: 'Actions' })).toBeInTheDocument();
    const runButton = within(row).getByRole('button', { name: 'Run' });
    expect(runButton.querySelector('i')).toHaveClass('bi-play-fill');
    expect(within(row).getByRole('button', { name: 'Edit' }).querySelector('i')).toHaveClass('bi-pencil');
    expect(within(row).getByRole('button', { name: 'Delete' }).querySelector('i')).toHaveClass('bi-trash');
    runButton.focus();
    await user.keyboard('{Enter}');
    await waitFor(() => expect(api.routines.run).toHaveBeenCalledWith(routine.id));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    await user.click(row);
    expect(screen.getByRole('dialog', { name: 'Morning check history' })).toBeInTheDocument();
    expect(screen.getByRole('link', { name: 'Open' })).toHaveAttribute('href', '/session/session-1?platform=opencode');
    expect(screen.queryByLabelText('Name')).not.toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Close routine history' }));
    await user.click(within(row).getByRole('button', { name: 'Edit' }));
    expect(screen.getByText('Session and model').closest('details')).not.toHaveAttribute('open');
    await user.click(screen.getByText('Session and model'));
    await user.click(screen.getByText('After a run'));
    expect(screen.getByRole('combobox', { name: 'Agent' })).toHaveTextContent('plan');
    expect(screen.getByRole('combobox', { name: 'Model' })).toHaveTextContent('anthropic/claude-sonnet-4');
    expect(screen.getByLabelText('Cron expression')).toHaveValue('0 9 * * *');
    expect(screen.getByLabelText('Archive session after a successful run')).toBeChecked();
    await user.click(screen.getByLabelText('Archive session after a successful run'));
    expect(screen.getByLabelText(/Notify in the Inbox after a successful run/)).toBeChecked();
    await user.click(screen.getByLabelText(/Notify in the Inbox after a successful run/));
    expect(screen.queryByRole('heading', { name: 'History' })).not.toBeInTheDocument();
    await user.clear(screen.getByLabelText('Name'));
    await user.type(screen.getByLabelText('Name'), 'Renamed');
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(api.routines.update).toHaveBeenCalledWith(routine.id, expect.objectContaining({ name: 'Renamed', archiveSessionAfterSuccess: false, notifyOnSuccess: false })));
  });

  it('confirms before deleting a routine', async () => {
    const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValueOnce(true);
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);
    const row = (await screen.findByText(routine.name)).closest('tr')!;

    await user.click(within(row).getByRole('button', { name: 'Delete' }));
    expect(confirm).toHaveBeenCalledWith('Delete "Morning check"?');
    expect(api.routines.remove).not.toHaveBeenCalled();
    await user.click(within(row).getByRole('button', { name: 'Delete' }));
    await waitFor(() => expect(api.routines.remove).toHaveBeenCalledWith(routine.id));
  });

  it('surfaces load and action errors', async () => {
    vi.mocked(api.routines.list).mockRejectedValueOnce(new Error('load failed'));
    const { unmount } = render(<MemoryRouter><Routines /></MemoryRouter>);
    expect(await screen.findByRole('alert')).toHaveTextContent('load failed');
    unmount();

    vi.mocked(api.routines.list).mockResolvedValue([routine]);
    vi.mocked(api.routines.run).mockRejectedValueOnce(new Error('run failed'));
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);
    await user.click(await screen.findByRole('button', { name: 'Run' }));
    expect(await screen.findByText('run failed')).toBeInTheDocument();
  });

  it('preserves an explicit local target', async () => {
    const user = userEvent.setup();
    render(<MemoryRouter><Routines /></MemoryRouter>);
    await screen.findByText(routine.name);
    await user.click(screen.getByRole('button', { name: 'Edit' }));
    await user.click(screen.getByRole('button', { name: 'Save changes' }));
    await waitFor(() => expect(api.routines.update).toHaveBeenCalledWith(routine.id, expect.objectContaining({ remoteId: 'local' })));
  });

  it('refreshes routines and history every five seconds while mounted', async () => {
    vi.useFakeTimers();
    const { unmount } = render(<MemoryRouter><Routines /></MemoryRouter>);
    await act(async () => {});
    expect(api.routines.list).toHaveBeenCalledTimes(1);

    vi.mocked(api.routines.history).mockResolvedValue([{ id: 'run-2', routineId: routine.id, routineUpdatedAt: routine.updatedAt, routineName: routine.name, prompt: routine.prompt, directory: '/repo', remoteId: 'local', agent: routine.agent, model: routine.model, sessionMode: 'new', targetSessionId: '', trigger: 'schedule', state: 'success', occurrenceAt: 2_000, createdAt: 2_000 }]);
    await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });

    expect(api.routines.list).toHaveBeenCalledTimes(2);
    expect(screen.getByText('success')).toBeInTheDocument();
    unmount();
    await act(async () => { await vi.advanceTimersByTimeAsync(5_000); });
    expect(api.routines.list).toHaveBeenCalledTimes(2);
  });
});
