// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useLayoutEffect } from 'react';
import { render, screen, waitFor, within } from '@testing-library/react';
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

  it("never commits the previous session's mode after switching sessions", async () => {
    getPermissionRules.mockResolvedValueOnce({ rules: planRules }).mockReturnValue(new Promise(() => {}));
    // Runs after commit, before passive effects: sees what the browser would paint.
    const committed: string[] = [];
    function Probe() {
      useLayoutEffect(() => { committed.push(document.body.innerHTML); });
      return null;
    }
    const { rerender } = render(<><PermissionModeLock sessionId="s1" /><Probe /></>);
    await screen.findByLabelText('Permission mode: Plan only');
    committed.length = 0;
    rerender(<><PermissionModeLock sessionId="s2" /><Probe /></>);
    expect(committed.some((html) => html.includes('Plan only'))).toBe(false);
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

  it('renders the confirmation outside disabled composer styling, like the picker', async () => {
    getPermissionRules.mockResolvedValue({ rules: [] });
    const { container } = render(
      <div className="oc-composer-disabled"><div className="oc-composer-bar-left">
        <PermissionModeLock sessionId="s1" />
      </div></div>,
    );
    await userEvent.click(await screen.findByLabelText('Permission mode: Default'));
    expect(container).not.toContainElement(screen.getByRole('dialog', { name: 'Permission mode...' }));
    await userEvent.click(screen.getByText('YOLO'));
    const confirmation = await screen.findByRole('dialog', { name: 'Confirm permission mode' });
    expect(container).not.toContainElement(confirmation);
    expect(confirmation.parentElement?.parentElement).toBe(document.body);
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

  it('focuses Confirm so Enter applies yolo', async () => {
    getPermissionRules.mockResolvedValue({ rules: [] });
    setPermissionRules.mockResolvedValue(undefined);
    render(<PermissionModeLock sessionId="s1" />);
    await userEvent.click(await screen.findByLabelText('Permission mode: Default'));

    await userEvent.click(screen.getByText('YOLO'));
    await waitFor(() => expect(screen.getByRole('button', { name: 'Confirm' })).toHaveFocus());
    await userEvent.keyboard('{Enter}');
    await waitFor(() => expect(screen.getByLabelText('Permission mode: YOLO')).toBeInTheDocument());
  });

  it('keeps the confirmation open and busy until yolo is saved', async () => {
    let finish!: () => void;
    getPermissionRules.mockResolvedValue({ rules: [] });
    setPermissionRules.mockReturnValue(new Promise<void>((resolve) => { finish = resolve; }));
    render(<PermissionModeLock sessionId="s1" />);
    await userEvent.click(await screen.findByLabelText('Permission mode: Default'));

    await userEvent.click(screen.getByText('YOLO'));
    await userEvent.click(await screen.findByRole('button', { name: 'Confirm' }));
    const dialog = screen.getByRole('dialog', { name: 'Confirm permission mode' });
    expect(within(dialog).getByRole('button', { name: 'Applying…' })).toHaveAttribute('aria-busy', 'true');
    await userEvent.keyboard('{Escape}');
    expect(screen.getByRole('dialog', { name: 'Confirm permission mode' })).toBeInTheDocument();

    finish();
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Confirm permission mode' })).not.toBeInTheDocument());
    expect(screen.getByLabelText('Permission mode: YOLO')).toBeInTheDocument();
  });
});
