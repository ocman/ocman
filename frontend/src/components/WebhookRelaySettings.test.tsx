// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { api } from '../lib/api';
import { WebhookRelaySettings } from './WebhookRelaySettings';

vi.mock('../lib/api', () => ({ api: { getWebhookRelay: vi.fn(), setWebhookRelay: vi.fn() } }));

describe('WebhookRelaySettings', () => {
  beforeEach(() => vi.clearAllMocks());

  it('saves the relay and token without echoing the token', async () => {
    vi.mocked(api.getWebhookRelay).mockResolvedValue({ relayUrl: '', defaultRelayUrl: 'https://share', hasEnrollmentToken: false });
    vi.mocked(api.setWebhookRelay).mockImplementation(async (input) => ({ relayUrl: input.relayUrl ?? '', defaultRelayUrl: 'https://share', hasEnrollmentToken: input.enrollmentToken !== undefined ? Boolean(input.enrollmentToken) : false }));
    render(<WebhookRelaySettings />);

    const relay = await screen.findByLabelText('Webhook relay');
    expect(relay).toHaveAttribute('placeholder', 'https://share');
    fireEvent.change(relay, { target: { value: 'https://mine' } });
    fireEvent.blur(relay);
    await waitFor(() => expect(api.setWebhookRelay).toHaveBeenCalledWith({ relayUrl: 'https://mine' }));

    const token = screen.getByLabelText('Enrollment token');
    fireEvent.change(token, { target: { value: 'secret' } });
    fireEvent.blur(token);
    await waitFor(() => expect(api.setWebhookRelay).toHaveBeenCalledWith({ enrollmentToken: 'secret' }));
    expect(await screen.findByPlaceholderText('Saved; type to replace')).toHaveValue('');
    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    await waitFor(() => expect(api.setWebhookRelay).toHaveBeenCalledWith({ enrollmentToken: '' }));
  });

  it('shows load and save errors', async () => {
    vi.mocked(api.getWebhookRelay).mockRejectedValueOnce(new Error('offline'));
    const { unmount } = render(<WebhookRelaySettings />);
    expect(await screen.findByRole('alert')).toHaveTextContent('offline');
    unmount();

    vi.mocked(api.getWebhookRelay).mockResolvedValue({ relayUrl: '', defaultRelayUrl: '', hasEnrollmentToken: false });
    vi.mocked(api.setWebhookRelay).mockRejectedValue(new Error('relay URL must be an http(s) URL'));
    render(<WebhookRelaySettings />);
    const relay = await screen.findByLabelText('Webhook relay');
    fireEvent.change(relay, { target: { value: 'ftp://x' } });
    fireEvent.blur(relay);
    expect(await screen.findByRole('alert')).toHaveTextContent('http(s)');
  });
});
