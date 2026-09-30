// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { MemoryRouter } from 'react-router-dom';
import { api, type Routine } from '../lib/api';
import type { WebhookDelivery, WebhookInbox } from '../lib/api.types';
import { WebhookInboxDrawer } from './WebhookInboxDrawer';
import { decodeFilters, deliveryHint, describeFilters, encodeFilters, outcomes, triggerFor, triggerLabel } from '../lib/webhookFilters';

vi.mock('../lib/api', () => ({ api: { getWebhookRelay: vi.fn(), webhookInboxes: { create: vi.fn(), update: vi.fn(), rotate: vi.fn(), revoke: vi.fn(), deliveries: vi.fn(), redeliver: vi.fn() } } }));

const routine = { id: 'r1', name: 'Review', enabled: true } as Routine;
const inbox: WebhookInbox = {
  id: 'inbox-1', name: 'forgejo', relayUrl: 'https://relay', ingestionUrl: '/i/x', keyVersion: 1, createdAt: 1, secretHeader: '', counts: {},
  subscriptions: [{ id: 'sub-1', inboxId: 'inbox-1', routineId: 'r1', headerPredicates: '{"x-forgejo-event":{"equals":"pull_request"}}', jsonPredicates: '', createdAt: 1 }],
};
const delivery: WebhookDelivery = {
  deliveryId: 'd1', accepted: true, acceptedAt: 1_000, attempts: 0, lastError: '', headers: '{"X-Forgejo-Event":["pull_request"]}', body: '{"action":"opened","number":7}',
  dispatches: [{ routineId: 'r1', state: 'terminal', error: '', platform: 'opencode', sessionId: 'ses-1' }, { routineId: 'gone', state: 'failure', error: 'no session', platform: '', sessionId: '' }],
};

describe('webhook filters', () => {
  it('round-trips every operator', () => {
    const rows = [
      { key: 'x-event', op: 'equals' as const, value: 'push' },
      { key: '/action', op: 'oneOf' as const, value: 'opened, synchronized,' },
      { key: '/draft', op: 'missing' as const, value: '' },
      { key: '/number', op: 'exists' as const, value: '' },
      { key: ' ', op: 'equals' as const, value: 'dropped' },
    ];
    const json = encodeFilters(rows);
    expect(JSON.parse(json)).toEqual({ 'x-event': { equals: 'push' }, '/action': { oneOf: ['opened', 'synchronized'] }, '/draft': { exists: false }, '/number': { exists: true } });
    expect(decodeFilters(json)).toMatchObject([
      { key: 'x-event', op: 'equals', value: 'push' },
      { key: '/action', op: 'oneOf', value: 'opened, synchronized' },
      { key: '/draft', op: 'missing', value: '' },
      { key: '/number', op: 'exists', value: '' },
    ]);
  });

  it('keeps an empty oneOf list instead of widening it to exists', () => {
    const json = encodeFilters([{ key: '/action', op: 'oneOf', value: ' , ' }]);
    expect(JSON.parse(json)).toEqual({ '/action': { oneOf: [] } });
    const rows = decodeFilters(json);
    expect(rows).toMatchObject([{ key: '/action', op: 'oneOf', value: '' }]);
    expect(encodeFilters(rows)).toBe(json);
  });

  it('saves untouched conditions exactly as stored', () => {
    // Commas, empty strings, surrounding spaces and non-string values have no
    // lossless comma-list form; an unedited row must not be rewritten.
    const stored = '{"/value":{"oneOf":["a,b",""," x "]},"/n":{"equals":5},"/legacy":{"op":"equals","value":true}}';
    const rows = decodeFilters(stored);
    expect(JSON.parse(encodeFilters(rows))).toEqual(JSON.parse(stored));
    // A renamed key keeps its predicate; an edited value is re-encoded.
    const edited = rows.map((row) => (row.key === '/value' ? { ...row, key: '/v' } : row.key === '/n' ? { ...row, value: '6' } : row));
    expect(JSON.parse(encodeFilters(edited))).toEqual({ '/v': { oneOf: ['a,b', '', ' x '] }, '/n': { equals: '6' }, '/legacy': { op: 'equals', value: true } });
  });

  it('reads the legacy op/value form and tolerates bad JSON', () => {
    expect(decodeFilters('{"/a":{"op":"equals","value":3}}')).toMatchObject([{ key: '/a', op: 'equals', value: '3' }]);
    expect(decodeFilters('not json')).toEqual([]);
    expect(decodeFilters('{"/a":null,"/b":5,"/c":{"exists":true}}')).toMatchObject([{ key: '/c', op: 'exists', value: '' }]);
    expect(decodeFilters('')).toEqual([]);
  });

  it('finds a routine trigger', () => {
    expect(triggerFor('r1', [inbox])).toMatchObject({ inboxId: 'inbox-1', headers: [{ key: 'x-forgejo-event', op: 'equals', value: 'pull_request' }], fields: [] });
    expect(triggerFor('other', [inbox]).inboxId).toBe('');
    expect(triggerFor(undefined, [inbox]).inboxId).toBe('');
  });

  it('describes conditions in one line', () => {
    expect(describeFilters('', '{}')).toBe('every delivery');
    expect(describeFilters('{"x-forgejo-event":{"equals":"pull_request"}}', '{"/action":{"oneOf":["opened","synchronized"]},"/draft":{"exists":false}}'))
      .toBe('x-forgejo-event = pull_request and /action in opened, synchronized and /draft is missing');
  });

  it('labels a routine trigger', () => {
    expect(triggerLabel({ id: 'r1', scheduleKind: 'none' }, [inbox])).toBe('webhook: forgejo');
    expect(triggerLabel({ id: 'r1', scheduleKind: 'cron' }, [inbox])).toBe('cron + webhook: forgejo');
    expect(triggerLabel({ id: 'r2', scheduleKind: 'cron' }, [inbox])).toBe('cron');
    expect(triggerLabel({ id: 'r2', scheduleKind: 'none' }, [inbox])).toBe('manual');
  });
});

describe('delivery log helpers', () => {
  it('summarises event and per-routine outcomes', () => {
    const name = (id: string) => (id === 'r1' ? 'Review' : id);
    expect(deliveryHint(delivery)).toBe('pull_request · opened');
    expect(deliveryHint({ ...delivery, headers: 'x', body: 'y' })).toBe('');
    expect(outcomes(delivery, name)).toEqual([{ label: 'ran: Review', tone: 'success', href: '/session/ses-1?platform=opencode' }, { label: 'failed: gone', tone: 'failure' }]);
    expect(outcomes({ ...delivery, dispatches: [{ routineId: '', state: 'ignored', error: '', platform: '', sessionId: '' }] }, name)).toEqual([{ label: 'no routine matched', tone: '' }]);
    expect(outcomes({ ...delivery, dispatches: [{ routineId: 'r1', state: 'ignored', error: '', platform: '', sessionId: '' }, { routineId: 'r1', state: 'ignored', error: 'routine disabled', platform: '', sessionId: '' }] }, name).map((o) => o.label)).toEqual(['no match: Review', 'skipped: Review']);
    expect(outcomes({ ...delivery, dispatches: [], lastError: 'boom' }, name)).toEqual([{ label: 'error', tone: 'failure' }]);
    expect(outcomes({ ...delivery, dispatches: [] }, name)).toEqual([{ label: 'received', tone: '' }]);
  });
});

function renderDrawer(props: Partial<Parameters<typeof WebhookInboxDrawer>[0]> = {}) {
  const all = { inbox, routines: [routine], onClose: vi.fn(), onChange: vi.fn(), onEditRoutine: vi.fn(), ...props };
  render(<MemoryRouter><WebhookInboxDrawer {...all} /></MemoryRouter>);
  return all;
}

describe('WebhookInboxDrawer', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    vi.mocked(api.webhookInboxes.deliveries).mockResolvedValue([delivery]);
    vi.mocked(api.getWebhookRelay).mockResolvedValue({ relayUrl: '', defaultRelayUrl: 'https://relay', hasEnrollmentToken: false });
  });

  it('creates a named inbox from the form', async () => {
    const user = userEvent.setup();
    vi.mocked(api.webhookInboxes.create).mockResolvedValue(inbox);
    const props = renderDrawer({ inbox: null });

    expect(screen.getByRole('button', { name: 'Create inbox' })).toBeDisabled();
    await user.type(screen.getByLabelText('Name'), 'forgejo');
    await user.type(screen.getByLabelText('Relay enrollment token'), 'enroll');
    await user.type(screen.getByLabelText(/Shared secret/), 'Bearer s');
    await user.clear(screen.getByLabelText(/Secret header/));
    await user.type(screen.getByLabelText(/Secret header/), 'Authorization');
    await user.click(screen.getByRole('button', { name: 'Create inbox' }));

    expect(api.webhookInboxes.create).toHaveBeenCalledWith({ name: 'forgejo', enrollmentToken: 'enroll', secret: 'Bearer s', secretHeader: 'Authorization' });
    expect(props.onChange).toHaveBeenCalled();
    expect(props.onClose).toHaveBeenCalled();
  });

  it('uses the enrollment token saved in Settings', async () => {
    const user = userEvent.setup();
    vi.mocked(api.getWebhookRelay).mockResolvedValue({ relayUrl: 'https://mine', defaultRelayUrl: 'https://relay', hasEnrollmentToken: true });
    vi.mocked(api.webhookInboxes.create).mockResolvedValue(inbox);
    renderDrawer({ inbox: null });

    expect(await screen.findByText('https://mine')).toBeInTheDocument();
    expect(screen.queryByLabelText('Relay enrollment token')).not.toBeInTheDocument();
    await user.type(screen.getByLabelText('Name'), 'gh');
    await user.click(screen.getByRole('button', { name: 'Create inbox' }));
    expect(api.webhookInboxes.create).toHaveBeenCalledWith({ name: 'gh', enrollmentToken: '', secret: '', secretHeader: '' });
  });

  it('lists subscribers and recent deliveries with per-routine outcomes', async () => {
    const user = userEvent.setup();
    const props = renderDrawer();

    expect(await screen.findByText('pull_request · opened')).toBeInTheDocument();
    expect(screen.getByRole('link', { name: /ran: Review/ })).toHaveAttribute('href', '/session/ses-1?platform=opencode');
    expect(screen.getByText('gone: no session')).toBeInTheDocument();
    expect(screen.getByText(/"number": 7/)).toBeInTheDocument();
    const linked = screen.getByRole('table', { name: 'Linked routines' });
    expect(within(linked).getByText('x-forgejo-event = pull_request')).toBeInTheDocument();
    expect(within(linked).getByText('enabled')).toBeInTheDocument();
    await user.click(within(linked).getByRole('button', { name: 'Review' }));
    expect(props.onEditRoutine).toHaveBeenCalledWith(routine);
  });

  it('redelivers an accepted delivery after confirmation and reloads the log', async () => {
    const user = userEvent.setup();
    const confirm = vi.spyOn(window, 'confirm').mockReturnValueOnce(false).mockReturnValue(true);
    vi.mocked(api.webhookInboxes.deliveries).mockResolvedValue([delivery, { ...delivery, deliveryId: 'd0', accepted: false, body: '', dispatches: [], lastError: 'bad key', attempts: 8 }]);
    vi.mocked(api.webhookInboxes.redeliver).mockResolvedValueOnce({ deliveryId: 'd1-redelivery-1' }).mockRejectedValueOnce(new Error('relay gone'));
    renderDrawer();

    const buttons = await screen.findAllByRole('button', { name: 'Redeliver' });
    expect(buttons).toHaveLength(1); // the undecrypted delivery has nothing to replay
    expect(screen.getByText('(not decrypted)')).toBeInTheDocument();
    await user.click(buttons[0]);
    expect(api.webhookInboxes.redeliver).not.toHaveBeenCalled();
    await user.click(buttons[0]);
    expect(api.webhookInboxes.redeliver).toHaveBeenCalledWith('inbox-1', 'd1');
    await waitFor(() => expect(api.webhookInboxes.deliveries).toHaveBeenCalledTimes(2));
    await user.click(await screen.findByRole('button', { name: 'Redeliver' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('relay gone');
    confirm.mockRestore();
  });

  it('shows an empty subscriber list, action errors, and revokes after confirmation', async () => {
    const user = userEvent.setup();
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    vi.mocked(api.webhookInboxes.deliveries).mockResolvedValue([]);
    vi.mocked(api.webhookInboxes.rotate).mockRejectedValue(new Error('relay down'));
    vi.mocked(api.webhookInboxes.revoke).mockResolvedValue(undefined);
    const props = renderDrawer({ inbox: { ...inbox, subscriptions: [] } });

    expect(screen.getByText(/No routine uses this inbox yet/)).toBeInTheDocument();
    expect(await screen.findByText('No deliveries received yet.')).toBeInTheDocument();
    await user.click(screen.getByRole('button', { name: 'Reset key' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('relay down');
    await user.click(screen.getByRole('button', { name: 'Revoke' }));
    expect(api.webhookInboxes.revoke).toHaveBeenCalledWith('inbox-1');
    expect(props.onClose).toHaveBeenCalled();
  });

  it('renames the inbox and replaces or removes its secret', async () => {
    const user = userEvent.setup();
    vi.spyOn(window, 'confirm').mockReturnValue(true);
    vi.mocked(api.webhookInboxes.update).mockResolvedValue(inbox);
    const props = renderDrawer();

    expect(screen.getByText(/No shared secret recorded/)).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Rename' })).toBeDisabled();
    await user.clear(screen.getByLabelText('Name'));
    await user.type(screen.getByLabelText('Name'), 'forgejo-prs');
    await user.click(screen.getByRole('button', { name: 'Rename' }));
    expect(api.webhookInboxes.update).toHaveBeenCalledWith('inbox-1', { name: 'forgejo-prs' });
    expect(await screen.findByText('Name saved.')).toBeInTheDocument();

    expect(screen.getByLabelText('Secret header')).toHaveValue('Authorization');
    expect(screen.getByRole('button', { name: 'Update secret' })).toBeDisabled();
    await user.type(screen.getByLabelText('New shared secret'), 'Bearer abc');
    await user.click(screen.getByRole('button', { name: 'Update secret' }));
    expect(api.webhookInboxes.update).toHaveBeenCalledWith('inbox-1', { secret: 'Bearer abc', secretHeader: 'Authorization' });
    expect(await screen.findByText('Secret updated.')).toBeInTheDocument();
    expect(screen.getByLabelText('New shared secret')).toHaveValue('');

    await user.click(screen.getByRole('button', { name: 'Remove secret' }));
    expect(api.webhookInboxes.update).toHaveBeenCalledWith('inbox-1', { secret: '' });
    expect(props.onChange).toHaveBeenCalledTimes(3);
  });

  it('shows the recorded secret header', () => {
    renderDrawer({ inbox: { ...inbox, secretHeader: 'X-Hook' } });
    expect(screen.getByText('X-Hook')).toBeInTheDocument();
    expect(screen.getByLabelText('Secret header')).toHaveValue('X-Hook');
  });
});
