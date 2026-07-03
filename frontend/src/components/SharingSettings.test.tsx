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

function seed(enabled: boolean, links: unknown[] = []) {
  m.getSharingEnabled.mockResolvedValue({ enabled });
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
    expect(screen.getByText('No shared sessions.')).toBeInTheDocument();
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
