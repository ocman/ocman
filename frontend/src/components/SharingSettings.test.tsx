// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { render, screen, fireEvent, waitFor, act } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { SharingSettings } from './SharingSettings';

vi.mock('../lib/api', () => ({
  api: {
    getSharingEnabled: vi.fn(),
    setSharingEnabled: vi.fn(),
    listAllShares: vi.fn(),
    revokeShareLink: vi.fn(),
  },
}));
import { api } from '../lib/api';

const m = api as unknown as Record<string, ReturnType<typeof vi.fn>>;

afterEach(() => {
  vi.clearAllMocks();
});

function seed(
  enabled: boolean,
  links: unknown[] = [],
  relay: { relayUrl: string; relaySource: string } = { relayUrl: '', relaySource: '' },
) {
  m.getSharingEnabled.mockResolvedValue({ enabled, ...relay });
  m.listAllShares.mockResolvedValue(links);
}

function renderUI() {
  return render(
    <MemoryRouter>
      <SharingSettings />
    </MemoryRouter>,
  );
}

const link = {
  token: 'tok1',
  url: 'http://localhost:8228/share/tok1',
  createdAt: Date.now(),
  platform: 'fake',
  sessionId: 'ses_a',
};

describe('SharingSettings', () => {
  it('reflects the enabled state and shows an empty list', async () => {
    seed(true, []);
    renderUI();
    const toggle = await screen.findByTestId('sharing-toggle');
    expect((toggle as HTMLInputElement).checked).toBe(true);
    expect(await screen.findByText('No shared sessions.')).toBeInTheDocument();
  });

  // The relay is configured on the command line, so Settings is the
  // only place an operator can confirm which one this instance uses.
  it('shows the relay URL configured on the command line', async () => {
    seed(true, [], { relayUrl: 'http://localhost:8231', relaySource: 'flag' });
    renderUI();
    const value = await screen.findByTestId('sharing-relay-url');
    expect(value).toHaveTextContent('http://localhost:8231');
    expect(screen.getByText(/-relay-url flag/)).toBeInTheDocument();
  });

  it('names the environment variable when the relay came from the env', async () => {
    seed(true, [], { relayUrl: 'https://share.example.com', relaySource: 'env' });
    renderUI();
    const value = await screen.findByTestId('sharing-relay-url');
    expect(value).toHaveTextContent('https://share.example.com');
    expect(screen.getByText(/OCMAN_RELAY_URL environment variable/)).toBeInTheDocument();
  });

  it('explains that sharing is local-only when no relay is configured', async () => {
    seed(true, []);
    renderUI();
    const value = await screen.findByTestId('sharing-relay-url');
    expect(value).toHaveTextContent('Not configured');
    expect(screen.getByText(/only work on this machine/)).toBeInTheDocument();
  });

  it('keeps showing the relay after toggling sharing off', async () => {
    seed(true, [], { relayUrl: 'http://localhost:8231', relaySource: 'flag' });
    m.setSharingEnabled.mockResolvedValue({
      enabled: false,
      relayUrl: 'http://localhost:8231',
      relaySource: 'flag',
    });
    renderUI();
    const toggle = await screen.findByTestId('sharing-toggle');

    await act(async () => {
      fireEvent.click(toggle);
    });
    await waitFor(() => expect(m.setSharingEnabled).toHaveBeenCalledWith(false));
    expect(screen.getByTestId('sharing-relay-url')).toHaveTextContent('http://localhost:8231');
  });

  it('persists the toggle when switched off', async () => {
    seed(true, []);
    m.setSharingEnabled.mockResolvedValue({ enabled: false });
    renderUI();
    const toggle = await screen.findByTestId('sharing-toggle');

    await act(async () => {
      fireEvent.click(toggle);
    });
    await waitFor(() => expect(m.setSharingEnabled).toHaveBeenCalledWith(false));
    expect((toggle as HTMLInputElement).checked).toBe(false);
  });

  it('reverts the toggle when the save fails', async () => {
    seed(true, []);
    m.setSharingEnabled.mockRejectedValue(new Error('boom'));
    renderUI();
    const toggle = await screen.findByTestId('sharing-toggle');

    await act(async () => {
      fireEvent.click(toggle);
    });
    await waitFor(() => expect((toggle as HTMLInputElement).checked).toBe(true));
    expect(await screen.findByRole('alert')).toHaveTextContent('boom');
  });

  it('lists shared sessions and revokes via the per-session endpoint', async () => {
    seed(true, [link]);
    m.revokeShareLink.mockResolvedValue(undefined);
    renderUI();
    expect(await screen.findByDisplayValue(link.url)).toBeInTheDocument();

    await act(async () => {
      fireEvent.click(screen.getByRole('button', { name: 'Revoke' }));
    });
    expect(m.revokeShareLink).toHaveBeenCalledWith('ses_a', 'tok1');
    await waitFor(() => expect(screen.queryByDisplayValue(link.url)).not.toBeInTheDocument());
  });
});
