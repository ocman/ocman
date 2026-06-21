// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import { MachinePickerModal } from './MachinePickerModal';
import { resolveTargetForDir, resolveMachineChoice } from '../lib/machinePicker';

vi.mock('../lib/api', () => ({
  api: { resolveTargets: vi.fn() },
}));
import { api } from '../lib/api';
const mockResolve = api.resolveTargets as ReturnType<typeof vi.fn>;

afterEach(() => {
  resolveMachineChoice(null);
  mockResolve.mockReset();
});

describe('MachinePickerModal', () => {
  it('renders nothing when closed', () => {
    const { container } = render(<MachinePickerModal />);
    expect(container.innerHTML).toBe('');
  });

  it('lists candidates and resolves the pending promise on click', async () => {
    mockResolve.mockResolvedValue({
      candidates: [
        { remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: '/p' },
        { remoteId: 'abc', remoteName: 'Box', platform: 'r-abc:opencode', dir: '/p' },
      ],
      remotes: [],
    });
    render(<MachinePickerModal />);

    let pending!: Promise<string | null>;
    act(() => { pending = resolveTargetForDir('/p'); });

    expect(await screen.findByText('Choose a machine')).toBeInTheDocument();
    const box = await screen.findByText('Box');
    act(() => { box.click(); });

    expect(await pending).toBe('r-abc:opencode');
    // Modal closes after the choice.
    expect(screen.queryByText('Choose a machine')).not.toBeInTheDocument();
  });

  it('on zero matches offers This machine plus enabled remotes', async () => {
    mockResolve.mockResolvedValue({
      candidates: [],
      remotes: [{ remoteId: 'abc', remoteName: 'Box', platform: 'r-abc:opencode', dir: '' }],
    });
    render(<MachinePickerModal />);

    act(() => { void resolveTargetForDir('/unknown'); });

    expect(await screen.findByText('Project not found on any machine')).toBeInTheDocument();
    expect(screen.getByText('This machine')).toBeInTheDocument();
    expect(screen.getByText('Box')).toBeInTheDocument();
  });

  it('cancels via the backdrop, resolving null', async () => {
    mockResolve.mockResolvedValue({
      candidates: [
        { remoteId: 'local', remoteName: 'This machine', platform: 'opencode', dir: '/p' },
        { remoteId: 'abc', remoteName: 'Box', platform: 'r-abc:opencode', dir: '/p' },
      ],
      remotes: [],
    });
    render(<MachinePickerModal />);

    let pending!: Promise<string | null>;
    act(() => { pending = resolveTargetForDir('/p'); });

    const backdrop = await screen.findByTestId('machine-picker-backdrop');
    act(() => { backdrop.click(); });
    expect(await pending).toBeNull();
  });
});
