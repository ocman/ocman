// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { useLayoutEffect } from 'react';
import { act, render, screen, waitFor } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { DraftPermissionMode, PermissionModeLock } from './PermissionModeLock';
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

  it('applies YOLO immediately without an extra confirmation', async () => {
    getPermissionRules.mockResolvedValue({ rules: [] });
    render(<PermissionModeLock sessionId="s1" />);
    await userEvent.click(await screen.findByLabelText('Permission mode: Default'));

    await userEvent.click(screen.getByText('YOLO'));
    await waitFor(() => expect(setPermissionRules).toHaveBeenCalledWith('s1', PERMISSION_MODES.find((m) => m.id === 'yolo')!.rules));
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('renders the picker outside disabled composer styling and applies YOLO directly', async () => {
    getPermissionRules.mockResolvedValue({ rules: [] });
    const { container } = render(
      <div className="oc-composer-disabled"><div className="oc-composer-bar-left">
        <PermissionModeLock sessionId="s1" />
      </div></div>,
    );
    await userEvent.click(await screen.findByLabelText('Permission mode: Default'));
    expect(container).not.toContainElement(screen.getByRole('dialog', { name: 'Permission mode...' }));
    await userEvent.click(screen.getByText('YOLO'));
    expect(setPermissionRules).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('applies YOLO using the keyboard', async () => {
    getPermissionRules.mockResolvedValue({ rules: [] });
    setPermissionRules.mockResolvedValue(undefined);
    render(<PermissionModeLock sessionId="s1" />);
    await userEvent.click(await screen.findByLabelText('Permission mode: Default'));

    await userEvent.type(screen.getByRole('combobox'), 'YOLO');
    await userEvent.keyboard('{Enter}');
    await waitFor(() =>
      expect(setPermissionRules).toHaveBeenCalledWith(
        's1',
        PERMISSION_MODES.find((m) => m.id === 'yolo')!.rules,
      ),
    );
    expect(screen.getByLabelText('Permission mode: YOLO')).toBeInTheDocument();
  });

  it('retains the previous mode and permits retry if saving YOLO fails', async () => {
    getPermissionRules.mockResolvedValue({ rules: [] });
    setPermissionRules.mockRejectedValueOnce(new Error('save offline')).mockResolvedValueOnce(undefined);
    render(<PermissionModeLock sessionId="s1" />);
    await userEvent.click(await screen.findByLabelText('Permission mode: Default'));

    await userEvent.click(screen.getByText('YOLO'));
    const lock = await screen.findByTitle('save offline');
    expect(lock).toHaveAccessibleName('Permission mode: Default');
    expect(lock).toBeEnabled();
    await userEvent.click(lock);
    await userEvent.click(screen.getByText('YOLO'));
    await waitFor(() => expect(screen.getByLabelText('Permission mode: YOLO')).toBeInTheDocument());
  });

  it('disables the mode control until YOLO is saved', async () => {
    let finish!: () => void;
    getPermissionRules.mockResolvedValue({ rules: [] });
    setPermissionRules.mockReturnValue(new Promise<void>((resolve) => { finish = resolve; }));
    render(<PermissionModeLock sessionId="s1" />);
    await userEvent.click(await screen.findByLabelText('Permission mode: Default'));

    await userEvent.click(screen.getByText('YOLO'));
    expect(screen.getByLabelText('Permission mode: Default')).toBeDisabled();
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    expect(setPermissionRules).toHaveBeenCalledTimes(1);

    await act(async () => finish());
    expect(screen.getByLabelText('Permission mode: YOLO')).toBeEnabled();
    expect(screen.getByLabelText('Permission mode: YOLO')).toBeInTheDocument();
  });
});

describe('DraftPermissionMode', () => {
  it('reports a preset without touching any session', async () => {
    const onChange = vi.fn();
    render(<DraftPermissionMode value="default" onChange={onChange} />);
    await userEvent.click(screen.getByLabelText('Permission mode: Default'));
    await userEvent.click(screen.getByText('Plan only'));
    expect(onChange).toHaveBeenCalledWith('plan');
    expect(setPermissionRules).not.toHaveBeenCalled();
    expect(getPermissionRules).not.toHaveBeenCalled();
  });

  it('reports YOLO directly without an extra confirmation', async () => {
    const onChange = vi.fn();
    render(<DraftPermissionMode value="default" onChange={onChange} />);
    await userEvent.click(screen.getByLabelText('Permission mode: Default'));
    await userEvent.click(screen.getByText('YOLO'));
    expect(onChange).toHaveBeenCalledWith('yolo');
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
  });

  it('labels the selected mode and falls back to Default for unknown ids', () => {
    const { rerender } = render(<DraftPermissionMode value="auto-edit" onChange={vi.fn()} />);
    expect(screen.getByLabelText('Permission mode: Auto-accept edits')).toBeInTheDocument();
    rerender(<DraftPermissionMode value="bogus" onChange={vi.fn()} />);
    expect(screen.getByLabelText('Permission mode: Default')).toBeInTheDocument();
  });
});
