// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, act, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import type { TermWindow } from '../lib/api';

// Mock the heavy TerminalPane (xterm + WebSocket) so the dock can be
// tested in jsdom. We render a stub that records the window it was asked
// to attach to.
vi.mock('./TerminalPane', () => ({
  TerminalPane: ({ dir, window }: { dir: string; window?: string }) => (
    <div data-testid="terminal-pane-stub" data-dir={dir} data-window={window} />
  ),
}));

vi.mock('../lib/remoteLog', () => ({
  remoteLog: { error: vi.fn(), info: vi.fn(), warn: vi.fn(), debug: vi.fn() },
}));

// API mock — controllable per test.
const listWindows = vi.fn<(dir: string) => Promise<{ windows: TermWindow[] }>>();
const createWindow = vi.fn<(dir: string) => Promise<{ window: string }>>();
const killWindow = vi.fn<(dir: string, window: string) => Promise<void>>();

vi.mock('../lib/api', () => ({
  api: {
    term: {
      listWindows: (dir: string) => listWindows(dir),
      createWindow: (dir: string) => createWindow(dir),
      killWindow: (dir: string, window: string) => killWindow(dir, window),
    },
  },
}));

import { SessionTerminalDock } from './SessionTerminalDock';

const DIR = '/home/u/proj';

beforeEach(() => {
  listWindows.mockReset();
  createWindow.mockReset();
  killWindow.mockReset();
  listWindows.mockResolvedValue({ windows: [] });
  createWindow.mockResolvedValue({ window: 'ocman-aaaaaaaaaa-1' });
  killWindow.mockResolvedValue();
});

describe('SessionTerminalDock gating', () => {
  it('discovers existing tabs after hidden startup without opening the dock', async () => {
    let hidden = true;
    const visibility = vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
    listWindows.mockResolvedValue({ windows: [{ name: 'existing', title: 'Existing shell' }] });
    const view = render(<SessionTerminalDock tmuxAvailable directory={DIR} />);
    try {
      await act(async () => {});
      expect(listWindows).not.toHaveBeenCalled();
      act(() => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
      await screen.findByRole('tab', { name: 'Existing shell' });
      fireEvent.click(screen.getByTitle('Show terminal'));
      await act(async () => {});
      expect(createWindow).not.toHaveBeenCalled();
    } finally { view.unmount(); visibility.mockRestore(); }
  });

  it('waits for existing-window discovery before auto-creating a terminal', async () => {
    let finish!: (value: { windows: TermWindow[] }) => void;
    listWindows.mockReturnValue(new Promise((resolve) => { finish = resolve; }));
    const view = render(<SessionTerminalDock tmuxAvailable directory={DIR} />);
    try {
      fireEvent.click(screen.getByTitle('Show terminal'));
      await act(async () => {});
      expect(createWindow).not.toHaveBeenCalled();
      await act(async () => { finish({ windows: [{ name: 'existing', title: 'Existing shell' }] }); });
      expect(createWindow).not.toHaveBeenCalled();
    } finally { view.unmount(); }
  });
  it('pauses title requests when hidden, resumes immediately, and stops when closed', async () => {
    vi.useFakeTimers();
    let hidden = false;
    const visibility = vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden);
    listWindows.mockResolvedValue({ windows: [{ name: 'term-1', title: 'shell' }] });
    const view = render(<SessionTerminalDock tmuxAvailable directory={DIR} />);
    try {
      await act(async () => {});
      fireEvent.click(screen.getByTitle('Show terminal'));
      await act(async () => {});
      listWindows.mockClear();
      act(() => { hidden = true; document.dispatchEvent(new Event('visibilitychange')); });
      await act(async () => { await vi.advanceTimersByTimeAsync(12_000); });
      expect(listWindows).not.toHaveBeenCalled();
      await act(async () => { hidden = false; document.dispatchEvent(new Event('visibilitychange')); });
      expect(listWindows).toHaveBeenCalledTimes(1);
      fireEvent.click(screen.getByTitle('Hide terminal'));
      await act(async () => {});
      listWindows.mockClear();
      await act(async () => { await vi.advanceTimersByTimeAsync(12_000); });
      expect(listWindows).not.toHaveBeenCalled();
    } finally {
      view.unmount();
      visibility.mockRestore();
      vi.useRealTimers();
    }
  });
  it('renders nothing when tmux is unavailable', () => {
    const { container } = render(
      <SessionTerminalDock tmuxAvailable={false} directory={DIR} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('does not list windows when tmux is unavailable', async () => {
    render(<SessionTerminalDock tmuxAvailable={false} directory={DIR} />);
    await act(async () => {});
    expect(listWindows).not.toHaveBeenCalled();
  });

  it('renders nothing without a directory', () => {
    const { container } = render(
      <SessionTerminalDock tmuxAvailable={true} directory={undefined} />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it('renders the Terminal toggle when available', async () => {
    render(<SessionTerminalDock tmuxAvailable={true} directory={DIR} />);
    expect(await screen.findByTitle('Show terminal')).toBeInTheDocument();
  });
});

describe('SessionTerminalDock tabs', () => {
  it('shows existing windows as tabs in the strip even while collapsed', async () => {
    listWindows.mockResolvedValue({
      windows: [
        { name: 'ocman-aaaaaaaaaa-1', title: '' },
        { name: 'ocman-aaaaaaaaaa-2', title: 'vim' },
      ],
    });
    render(<SessionTerminalDock tmuxAvailable={true} directory={DIR} />);

    // Tab labels: index for the untitled one, command for the titled one.
    expect(await screen.findByText('1')).toBeInTheDocument();
    expect(screen.getByText('vim')).toBeInTheDocument();
    // Panel is collapsed: no terminal pane mounted yet.
    expect(screen.queryByTestId('terminal-pane-stub')).not.toBeInTheDocument();
  });

  it('creates the first terminal when opened with none existing', async () => {
    const user = userEvent.setup();
    render(<SessionTerminalDock tmuxAvailable={true} directory={DIR} />);

    const toggle = await screen.findByTitle('Show terminal');
    await user.click(toggle);

    await waitFor(() => expect(createWindow).toHaveBeenCalledWith(DIR));
    const pane = await screen.findByTestId('terminal-pane-stub');
    expect(pane.getAttribute('data-dir')).toBe(DIR);
    expect(pane.getAttribute('data-window')).toBe('ocman-aaaaaaaaaa-1');
  });

  it('adds a new terminal via the + button', async () => {
    const user = userEvent.setup();
    // listWindows reflects the created window so the post-open discovery
    // refresh keeps the new tab active rather than resetting it.
    let windows: TermWindow[] = [{ name: 'ocman-aaaaaaaaaa-1', title: '' }];
    listWindows.mockImplementation(async () => ({ windows }));
    createWindow.mockImplementation(async () => {
      const win = 'ocman-aaaaaaaaaa-2';
      windows = [...windows, { name: win, title: '' }];
      return { window: win };
    });
    render(<SessionTerminalDock tmuxAvailable={true} directory={DIR} />);

    await screen.findByText('1');
    await user.click(screen.getByLabelText('New terminal'));

    await waitFor(() => expect(createWindow).toHaveBeenCalledWith(DIR));
    // Both tabs are present after adding.
    await waitFor(() => expect(screen.getByText('2')).toBeInTheDocument());
    const pane = await screen.findByTestId('terminal-pane-stub');
    expect(pane.getAttribute('data-window')).toBe('ocman-aaaaaaaaaa-2');
  });

  it('collapses after closing the last terminal without creating a replacement', async () => {
    const user = userEvent.setup();
    let windows: TermWindow[] = [{ name: 'ocman-aaaaaaaaaa-1', title: '' }];
    listWindows.mockImplementation(async () => ({ windows }));
    killWindow.mockImplementation(async () => { windows = []; });
    render(<SessionTerminalDock tmuxAvailable={true} directory={DIR} />);

    await user.click(await screen.findByRole('tab', { name: '1' }));
    expect(await screen.findByTestId('terminal-pane-stub')).toBeInTheDocument();
    await user.click(screen.getByLabelText('Close terminal 1'));

    expect(killWindow).toHaveBeenCalledWith(DIR, 'ocman-aaaaaaaaaa-1');
    expect(screen.getByTitle('Show terminal')).toBeInTheDocument();
    expect(screen.queryByTestId('terminal-pane-stub')).not.toBeInTheDocument();
    expect(screen.queryByRole('separator', { name: 'Resize terminal' })).not.toBeInTheDocument();
    expect(screen.queryByRole('tab')).not.toBeInTheDocument();
    expect(createWindow).not.toHaveBeenCalled();
  });

  it('closes a terminal via its × button and calls killWindow', async () => {
    const user = userEvent.setup();
    listWindows.mockResolvedValue({
      windows: [
        { name: 'ocman-aaaaaaaaaa-1', title: '' },
        { name: 'ocman-aaaaaaaaaa-2', title: '' },
      ],
    });
    render(<SessionTerminalDock tmuxAvailable={true} directory={DIR} />);

    await user.click(await screen.findByRole('tab', { name: '1' }));
    await user.click(screen.getByLabelText('Close terminal 1'));

    await waitFor(() =>
      expect(killWindow).toHaveBeenCalledWith(DIR, 'ocman-aaaaaaaaaa-1'),
    );
    // Optimistically removed from the strip.
    await waitFor(() => expect(screen.queryByText('1')).not.toBeInTheDocument());
    expect(screen.getByText('2')).toBeInTheDocument();
    expect(screen.getByTitle('Hide terminal')).toBeInTheDocument();
    expect(screen.getByTestId('terminal-pane-stub')).toHaveAttribute('data-window', 'ocman-aaaaaaaaaa-2');
  });
});
