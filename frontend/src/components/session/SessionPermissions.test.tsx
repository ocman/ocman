// @vitest-environment jsdom
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { beforeEach, expect, it, vi } from 'vitest';
import { SessionPermissions, SessionPermissionsDialog } from './SessionPermissions';
import { sessionPermissionPolicy, type SessionPermissionPolicy } from '../../lib/sessionPermissionPolicy';

vi.mock('../../lib/sessionPermissionPolicy', () => ({ sessionPermissionPolicy: { get: vi.fn(), save: vi.fn() } }));

const policy: SessionPermissionPolicy = {
  rules: [],
  requests: [{ platform: 'r-box:agent', sessionId: 's1', permissionId: 'p1', permission: 'bash', patterns: ['tea do something'], requestedAt: 1000, resolution: 'user-once' }],
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.mocked(sessionPermissionPolicy.get).mockResolvedValue(structuredClone(policy));
  vi.mocked(sessionPermissionPolicy.save).mockImplementation(async (_id, _platform, rules) => ({ ...policy, rules }));
});

it('opens from the command and saves an editable wildcard rule for the current owner', async () => {
  const user = userEvent.setup();
  render(<SessionPermissions sessionId="s1" platform="r-box:agent" />);
  expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  act(() => window.dispatchEvent(new Event('oc-session-permissions-open')));
  await screen.findByText('tea do something');
  expect(sessionPermissionPolicy.get).toHaveBeenCalledWith('s1', 'r-box:agent');
  expect(screen.queryByRole('group', { name: 'Permission presets' })).not.toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'allow tea do something' }));
  const pattern = screen.getByLabelText('Rule 1 pattern');
  await user.clear(pattern);
  await user.type(pattern, 'tea do *');
  await user.click(screen.getByRole('button', { name: 'Save session rules' }));
  await waitFor(() => expect(sessionPermissionPolicy.save).toHaveBeenCalledWith('s1', 'r-box:agent', [{ permission: 'bash', pattern: 'tea do *', action: 'allow' }]));
  await waitFor(() => expect(screen.getByRole('button', { name: 'Save session rules' })).toBeDisabled());
});

it('supports ask/deny, ordering, manual rules, validation and removal', async () => {
  const user = userEvent.setup();
  render(<SessionPermissionsDialog sessionId="s1" platform="agent" onClose={vi.fn()} />);
  await screen.findByText('tea do something');
  await user.click(screen.getByRole('button', { name: 'ask tea do something' }));
  await user.click(screen.getByRole('button', { name: 'deny tea do something' }));
  await user.click(screen.getByRole('button', { name: 'Move rule 2 up' }));
  expect(screen.getByLabelText('Rule 1 action')).toHaveValue('deny');
  await user.click(screen.getByRole('button', { name: 'Move rule 1 down' }));
  expect(screen.getByLabelText('Rule 1 action')).toHaveValue('ask');
  await user.click(screen.getByRole('button', { name: 'Add rule' }));
  await user.clear(screen.getByLabelText('Rule 3 permission'));
  expect(screen.getByRole('button', { name: 'Save session rules' })).toBeDisabled();
  await user.click(screen.getByRole('button', { name: 'Remove rule 3' }));
  await user.selectOptions(screen.getByLabelText('Rule 1 action'), 'allow');
  await user.click(screen.getByRole('button', { name: 'Save session rules' }));
  await waitFor(() => expect(sessionPermissionPolicy.save).toHaveBeenCalledWith('s1', 'agent', [expect.objectContaining({ action: 'allow' }), expect.objectContaining({ action: 'deny' })]));
});

it('shows load errors, retries, searches and retains edits after a failed save', async () => {
  const user = userEvent.setup();
  vi.mocked(sessionPermissionPolicy.get).mockRejectedValueOnce(new Error('Owner is disconnected'));
  vi.mocked(sessionPermissionPolicy.save).mockRejectedValueOnce(new Error('Could not save'));
  render(<SessionPermissionsDialog sessionId="s1" platform="agent" onClose={vi.fn()} />);
  expect(await screen.findByRole('alert')).toHaveTextContent('Owner is disconnected');
  await user.click(screen.getByRole('button', { name: 'Retry loading permissions' }));
  await screen.findByText('tea do something');
  await user.type(screen.getByLabelText('Search requested permissions'), 'unrelated');
  expect(screen.getByText('No recorded requests match.')).toBeInTheDocument();
  await user.clear(screen.getByLabelText('Search requested permissions'));
  await user.click(screen.getByRole('button', { name: 'deny tea do something' }));
  expect(screen.getByRole('button', { name: 'Refresh requests' })).toBeDisabled();
  await user.click(screen.getByRole('button', { name: 'Save session rules' }));
  expect(await screen.findByRole('alert')).toHaveTextContent('Could not save');
  expect(screen.getByLabelText('Rule 1 action')).toHaveValue('deny');
});

it('escapes literal wildcard characters in recorded requests and confirms unsaved close', async () => {
  const user = userEvent.setup();
  const onClose = vi.fn();
  vi.mocked(sessionPermissionPolicy.get).mockResolvedValue({ ...policy, requests: [{ ...policy.requests[0], sessionId: 'child', patterns: ['cat *'] }] });
  const confirm = vi.spyOn(window, 'confirm').mockReturnValue(false);
  render(<SessionPermissionsDialog sessionId="s1" platform="agent" onClose={onClose} />);
  await screen.findByText('cat *');
  expect(screen.getByText(/Subagent/)).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: 'allow cat *' }));
  expect(screen.getByLabelText('Rule 1 pattern')).toHaveValue('cat \\*');
  await user.click(screen.getByRole('button', { name: 'Close' }));
  expect(onClose).not.toHaveBeenCalled();
  confirm.mockReturnValue(true);
  await user.click(screen.getByRole('button', { name: 'Close' }));
  expect(onClose).toHaveBeenCalledOnce();
  confirm.mockRestore();
});

it('ignores a fetch that resolves after switching sessions', async () => {
  let resolveOld!: (value: SessionPermissionPolicy) => void;
  vi.mocked(sessionPermissionPolicy.get).mockReturnValueOnce(new Promise((resolve) => { resolveOld = resolve; }));
  const { rerender } = render(<SessionPermissionsDialog sessionId="old" platform="agent" onClose={vi.fn()} />);
  rerender(<SessionPermissionsDialog sessionId="new" platform="agent" onClose={vi.fn()} />);
  await screen.findByText('tea do something');
  await act(async () => resolveOld({ rules: [{ permission: '*', pattern: '*', action: 'deny' }], requests: [] }));
  expect(screen.queryByLabelText('Rule 1 action')).not.toBeInTheDocument();
});

it('locks editing during refresh and closes cleanly when nothing changed', async () => {
  const user = userEvent.setup();
  const onClose = vi.fn();
  render(<SessionPermissionsDialog sessionId="s1" platform="agent" onClose={onClose} />);
  await screen.findByText('tea do something');
  let finish!: (value: SessionPermissionPolicy) => void;
  vi.mocked(sessionPermissionPolicy.get).mockReturnValueOnce(new Promise((resolve) => { finish = resolve; }));
  await user.click(screen.getByRole('button', { name: 'Refresh requests' }));
  expect(screen.getByRole('button', { name: 'allow tea do something' })).toBeDisabled();
  expect(screen.getByRole('button', { name: 'Add rule' })).toBeDisabled();
  await act(async () => finish({ rules: [], requests: [] }));
  expect(screen.getByRole('button', { name: 'Add rule' })).toBeEnabled();
  await user.click(screen.getByRole('button', { name: 'Close session permissions' }));
  expect(onClose).toHaveBeenCalledOnce();
});
