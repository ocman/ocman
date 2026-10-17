// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReactNode } from 'react';
import { HookPluginSettings } from './HookPluginSettings';
import { HookPluginPrompt } from './HookPluginPrompt';

vi.mock('../lib/api', () => ({ api: { hookPlugin: vi.fn(), setHookPlugin: vi.fn() } }));
import { api } from '../lib/api';

const hookPlugin = vi.mocked(api.hookPlugin);
const setHookPlugin = vi.mocked(api.setHookPlugin);

function renderWith(ui: ReactNode) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={client}>{ui}</QueryClientProvider>);
}

beforeEach(() => window.localStorage.clear());
afterEach(() => vi.clearAllMocks());

describe('HookPluginSettings', () => {
  it('installs and uninstalls through the API', async () => {
    hookPlugin.mockResolvedValue({ available: true, installed: false });
    setHookPlugin.mockResolvedValueOnce({ available: true, installed: true });
    renderWith(<HookPluginSettings />);
    fireEvent.click(await screen.findByRole('button', { name: 'Install' }));
    expect(screen.getByRole('heading', { name: 'Hooks', level: 3 })).toBeVisible();
    expect(await screen.findByRole('button', { name: 'Uninstall' })).toBeInTheDocument();
    expect(setHookPlugin).toHaveBeenCalledWith(true);

    setHookPlugin.mockRejectedValueOnce(new Error('claude plugin uninstall: exit 1'));
    fireEvent.click(screen.getByRole('button', { name: 'Uninstall' }));
    expect(await screen.findByRole('alert')).toHaveTextContent('exit 1');
    expect(setHookPlugin).toHaveBeenLastCalledWith(false);
  });

  it('renders nothing when no platform offers the plugin', async () => {
    hookPlugin.mockResolvedValue({ available: false, installed: false });
    const { container } = renderWith(<HookPluginSettings />);
    await vi.waitFor(() => expect(hookPlugin).toHaveBeenCalled());
    expect(container).toBeEmptyDOMElement();
  });
});

describe('HookPluginPrompt', () => {
  it('offers the install once and remembers dismissal', async () => {
    hookPlugin.mockResolvedValue({ available: true, installed: false });
    const { unmount } = renderWith(<HookPluginPrompt />);
    expect(await screen.findByTestId('hook-plugin-prompt')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Not now' }));
    expect(screen.queryByTestId('hook-plugin-prompt')).not.toBeInTheDocument();
    unmount();
    renderWith(<HookPluginPrompt />);
    await vi.waitFor(() => expect(hookPlugin).toHaveBeenCalledTimes(2));
    expect(screen.queryByTestId('hook-plugin-prompt')).not.toBeInTheDocument();
  });

  it('installs from the toast and hides once installed', async () => {
    hookPlugin.mockResolvedValue({ available: true, installed: false });
    setHookPlugin.mockResolvedValue({ available: true, installed: true });
    renderWith(<HookPluginPrompt />);
    fireEvent.click(await screen.findByRole('button', { name: 'Install' }));
    await vi.waitFor(() => expect(screen.queryByTestId('hook-plugin-prompt')).not.toBeInTheDocument());
    expect(setHookPlugin).toHaveBeenCalledWith(true);
  });
});
