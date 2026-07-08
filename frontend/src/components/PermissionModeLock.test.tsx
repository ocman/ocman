// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { PermissionModeLock } from './PermissionModeLock';
import { PERMISSION_MODES } from '../lib/permissionModes';

const getPermissionRules = vi.fn();
const setPermissionRules = vi.fn();
vi.mock('../lib/api', () => ({
  api: {
    getPermissionRules: (...args: unknown[]) => getPermissionRules(...args),
    setPermissionRules: (...args: unknown[]) => setPermissionRules(...args),
  },
}));

const planRules = PERMISSION_MODES.find((m) => m.id === 'plan')!.rules;

afterEach(() => {
  vi.restoreAllMocks();
  getPermissionRules.mockReset();
  setPermissionRules.mockReset();
});

beforeEach(() => {
  Element.prototype.scrollIntoView = vi.fn();
});

describe('PermissionModeLock', () => {
  it('renders nothing while loading and after a failed read', async () => {
    getPermissionRules.mockRejectedValue(new Error('no live instance'));
    const { container } = render(<PermissionModeLock sessionId="s1" />);
    await waitFor(() => expect(getPermissionRules).toHaveBeenCalledWith('s1'));
    expect(container.innerHTML).toBe('');
  });

  it('shows the classified mode label', async () => {
    getPermissionRules.mockResolvedValue({ rules: planRules });
    render(<PermissionModeLock sessionId="s1" />);
    expect(await screen.findByLabelText('Permission mode: Plan only')).toBeInTheDocument();
  });

  it('shows Custom for unknown rulesets', async () => {
    getPermissionRules.mockResolvedValue({
      rules: [{ permission: 'edit', pattern: 'src/*', action: 'deny' }],
    });
    render(<PermissionModeLock sessionId="s1" />);
    expect(await screen.findByLabelText('Permission mode: Custom')).toBeInTheDocument();
  });

  it('applies a preset and updates the label', async () => {
    getPermissionRules.mockResolvedValue({ rules: [] });
    setPermissionRules.mockResolvedValue(undefined);
    render(<PermissionModeLock sessionId="s1" />);
    await userEvent.click(await screen.findByLabelText('Permission mode: Default'));

    await userEvent.click(screen.getByText('Plan only'));
    await waitFor(() => expect(setPermissionRules).toHaveBeenCalledWith('s1', planRules));
    expect(screen.getByLabelText('Permission mode: Plan only')).toBeInTheDocument();
  });

  it('asks for confirmation before yolo and aborts on cancel', async () => {
    getPermissionRules.mockResolvedValue({ rules: [] });
    render(<PermissionModeLock sessionId="s1" />);
    await userEvent.click(await screen.findByLabelText('Permission mode: Default'));

    await userEvent.click(screen.getByText('YOLO'));
    expect(await screen.findByRole('dialog', { name: 'Confirm permission mode' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Cancel' }));
    expect(setPermissionRules).not.toHaveBeenCalled();
  });

  it('applies yolo when confirmed', async () => {
    getPermissionRules.mockResolvedValue({ rules: [] });
    setPermissionRules.mockResolvedValue(undefined);
    render(<PermissionModeLock sessionId="s1" />);
    await userEvent.click(await screen.findByLabelText('Permission mode: Default'));

    await userEvent.click(screen.getByText('YOLO'));
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm' }));
    await waitFor(() =>
      expect(setPermissionRules).toHaveBeenCalledWith(
        's1',
        PERMISSION_MODES.find((m) => m.id === 'yolo')!.rules,
      ),
    );
    expect(screen.getByLabelText('Permission mode: YOLO')).toBeInTheDocument();
  });
});
