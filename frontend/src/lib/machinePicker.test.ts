import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  resolveTargetForDir,
  resolveMachineChoice,
  subscribeMachinePicker,
  type MachinePickerState,
} from './machinePicker';

// Controllable api mock.
vi.mock('./api', () => ({
  api: { resolveTargets: vi.fn() },
}));
import { api } from './api';
const mockResolve = api.resolveTargets as ReturnType<typeof vi.fn>;

afterEach(() => {
  mockResolve.mockReset();
  // Ensure no modal stays open between tests.
  resolveMachineChoice(null);
});

describe('resolveTargetForDir', () => {
  it('auto-resolves without prompting when there is exactly one candidate', async () => {
    mockResolve.mockResolvedValue({
      candidates: [{ remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: '/p' }],
      remotes: [],
    });
    const platform = await resolveTargetForDir('/p');
    expect(platform).toBe('opencode');
  });

  it('returns the empty sentinel (local default) when the resolver errors', async () => {
    mockResolve.mockRejectedValue(new Error('no endpoint'));
    const platform = await resolveTargetForDir('/p');
    expect(platform).toBe('');
  });

  it('opens the modal when several candidates match, resolving on choice', async () => {
    mockResolve.mockResolvedValue({
      candidates: [
        { remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: '/p' },
        { remoteId: 'abc', remoteName: 'Box', platform: 'r-abc:opencode', dir: '/p' },
      ],
      remotes: [],
    });

    const seen: { state: MachinePickerState | null } = { state: null };
    const unsub = subscribeMachinePicker((s) => { seen.state = s; });

    const pending = resolveTargetForDir('/p');
    // The modal should be open with both candidates.
    await vi.waitFor(() => expect(seen.state?.open).toBe(true));
    expect(seen.state?.candidates).toHaveLength(2);

    // Operator picks the remote.
    resolveMachineChoice('r-abc:opencode');
    expect(await pending).toBe('r-abc:opencode');
    expect(seen.state?.open).toBe(false);
    unsub();
  });

  it('opens the modal on zero matches and resolves null on cancel', async () => {
    mockResolve.mockResolvedValue({
      candidates: [],
      remotes: [{ remoteId: 'abc', remoteName: 'Box', platform: 'r-abc:opencode', dir: '' }],
    });
    const seen: { state: MachinePickerState | null } = { state: null };
    const unsub = subscribeMachinePicker((s) => { seen.state = s; });

    const pending = resolveTargetForDir('/unknown');
    // Wait for the modal to actually open before cancelling, otherwise
    // the cancel races ahead of the resolver being installed.
    await vi.waitFor(() => expect(seen.state?.open).toBe(true));
    resolveMachineChoice(null);
    expect(await pending).toBeNull();
    unsub();
  });
});
