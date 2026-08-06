// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { McpConfigPrompt } from './McpConfigPrompt';
import type { McpConfigStatus, McpConfigInstallResult } from '../lib/api.types';

const mocks = vi.hoisted(() => ({
  getMcpConfig: vi.fn(),
  installMcpConfig: vi.fn(),
}));

vi.mock('../lib/api', () => ({
  api: {
    getMcpConfig: mocks.getMcpConfig,
    installMcpConfig: mocks.installMcpConfig,
  },
}));
vi.mock('../lib/remoteLog', () => ({ remoteLog: { error: vi.fn(), info: vi.fn() } }));

const WANT = 'http://127.0.0.1:8227/mcp';
const PATH = '/home/u/.config/opencode/opencode.json';

function status(over: Partial<McpConfigStatus> = {}): McpConfigStatus {
  return { path: PATH, configured: false, wantUrl: WANT, editable: true, ...over };
}

function installResult(over: Partial<McpConfigInstallResult> = {}): McpConfigInstallResult {
  return { installed: true, path: PATH, backupPath: '', url: WANT, ...over };
}

// jsdom's localStorage lacks working methods in this test env — stub an
// in-memory Storage so the persisted-dismissal path is exercised.
const store = vi.hoisted(() => new Map<string, string>());
beforeEach(() => {
  vi.stubGlobal('localStorage', {
    getItem: (k: string) => store.get(k) ?? null,
    setItem: (k: string, v: string) => { store.set(k, String(v)); },
    removeItem: (k: string) => { store.delete(k); },
    clear: () => { store.clear(); },
  });
});

afterEach(() => {
  vi.unstubAllGlobals();
  vi.clearAllMocks();
  store.clear();
});

describe('McpConfigPrompt', () => {
  it('renders nothing when already configured', async () => {
    mocks.getMcpConfig.mockResolvedValue(status({ configured: true, currentUrl: WANT }));
    render(<McpConfigPrompt />);
    await waitFor(() => expect(mocks.getMcpConfig).toHaveBeenCalled());
    expect(screen.queryByTestId('mcp-config-prompt')).toBeNull();
  });

  it('renders nothing when the status request fails', async () => {
    mocks.getMcpConfig.mockRejectedValue(new Error('HTTP 500'));
    render(<McpConfigPrompt />);
    await waitFor(() => expect(mocks.getMcpConfig).toHaveBeenCalled());
    expect(screen.queryByTestId('mcp-config-prompt')).toBeNull();
  });

  it('offers to install when the entry is missing', async () => {
    mocks.getMcpConfig.mockResolvedValue(status());
    render(<McpConfigPrompt />);
    const prompt = await screen.findByTestId('mcp-config-prompt');
    expect(prompt).toHaveTextContent(WANT);
    expect(prompt).toHaveTextContent(PATH);
    expect(screen.getByText('ocman MCP not configured')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Install' })).toBeEnabled();
  });

  it('flags a stale entry differently', async () => {
    mocks.getMcpConfig.mockResolvedValue(status({ currentUrl: 'http://localhost:8228/mcp' }));
    render(<McpConfigPrompt />);
    await screen.findByTestId('mcp-config-prompt');
    expect(screen.getByText('ocman MCP is out of date')).toBeInTheDocument();
  });

  it('installs and reports the backup path', async () => {
    mocks.getMcpConfig.mockResolvedValue(status());
    mocks.installMcpConfig.mockResolvedValue(
      installResult({ backupPath: '/home/u/.config/opencode/opencode.2026-08-06T101500-backup.json' }),
    );
    render(<McpConfigPrompt />);
    await screen.findByTestId('mcp-config-prompt');

    fireEvent.click(screen.getByRole('button', { name: 'Install' }));

    const done = await screen.findByTestId('mcp-config-installed');
    expect(mocks.installMcpConfig).toHaveBeenCalledTimes(1);
    expect(done).toHaveTextContent('Restart OpenCode');
    expect(done).toHaveTextContent('opencode.2026-08-06T101500-backup.json');
    expect(screen.queryByTestId('mcp-config-prompt')).toBeNull();
  });

  it('shows the error and keeps the Install button on failure', async () => {
    mocks.getMcpConfig.mockResolvedValue(status());
    mocks.installMcpConfig.mockRejectedValue(new Error('HTTP 409: config is JSONC'));
    render(<McpConfigPrompt />);
    await screen.findByTestId('mcp-config-prompt');

    fireEvent.click(screen.getByRole('button', { name: 'Install' }));

    expect(await screen.findByTestId('mcp-config-error')).toHaveTextContent('HTTP 409');
    expect(screen.getByRole('button', { name: 'Install' })).toBeEnabled();
  });

  it('tells the user to edit by hand when the config is not editable', async () => {
    mocks.getMcpConfig.mockResolvedValue(status({
      path: '/home/u/.config/opencode/opencode.jsonc',
      editable: false,
      reason: 'config is JSONC; comments would be lost',
    }));
    render(<McpConfigPrompt />);
    const prompt = await screen.findByTestId('mcp-config-prompt');
    expect(prompt).toHaveTextContent('by hand');
    expect(prompt).toHaveTextContent('comments would be lost');
    expect(screen.queryByRole('button', { name: 'Install' })).toBeNull();
  });

  it('stays dismissed for the same URL across remounts, but re-prompts for a new one', async () => {
    mocks.getMcpConfig.mockResolvedValue(status());
    const { unmount } = render(<McpConfigPrompt />);
    await screen.findByTestId('mcp-config-prompt');

    fireEvent.click(screen.getByRole('button', { name: 'Not now' }));
    await waitFor(() => expect(screen.queryByTestId('mcp-config-prompt')).toBeNull());
    unmount();

    render(<McpConfigPrompt />);
    await waitFor(() => expect(mocks.getMcpConfig).toHaveBeenCalledTimes(2));
    expect(screen.queryByTestId('mcp-config-prompt')).toBeNull();

    // A different endpoint (e.g. the port moved) prompts again.
    mocks.getMcpConfig.mockResolvedValue(status({ wantUrl: 'http://127.0.0.1:9999/mcp' }));
    render(<McpConfigPrompt />);
    expect(await screen.findByTestId('mcp-config-prompt')).toHaveTextContent('9999');
  });
});
