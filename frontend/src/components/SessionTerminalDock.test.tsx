// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, waitFor, act } from '@testing-library/react';
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
  it('renders nothing when tmux is unavailable', () => {
    const { container } = render(
      <SessionTerminalDock tmuxAvailable={false} directory={DIR} />,
    );
    expect(container).toBeEmptyDOMElement();
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
    await act(async () => {
      await user.click(toggle);
    });

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
    await act(async () => {
      await user.click(screen.getByLabelText('New terminal'));
    });

    await waitFor(() => expect(createWindow).toHaveBeenCalledWith(DIR));
    // Both tabs are present after adding.
    await waitFor(() => expect(screen.getByText('2')).toBeInTheDocument());
    const pane = await screen.findByTestId('terminal-pane-stub');
    expect(pane.getAttribute('data-window')).toBe('ocman-aaaaaaaaaa-2');
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

    await screen.findByText('1');
    await user.click(screen.getByLabelText('Close terminal 1'));

    await waitFor(() =>
      expect(killWindow).toHaveBeenCalledWith(DIR, 'ocman-aaaaaaaaaa-1'),
    );
    // Optimistically removed from the strip.
    await waitFor(() => expect(screen.queryByText('1')).not.toBeInTheDocument());
    expect(screen.getByText('2')).toBeInTheDocument();
  });
});
