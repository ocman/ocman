// @vitest-environment jsdom
import { act, fireEvent, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { renderWithQuery } from '../test/renderWithQuery';
import { api } from '../lib/api';
import { WebhookRelayConnect } from './WebhookRelayConnect';

vi.mock('../lib/api', () => ({ api: { connectWebhookRelay: vi.fn(), pollWebhookRelay: vi.fn(), cancelWebhookRelayConnect: vi.fn() } }));
const settings = { relayUrl: 'https://relay', defaultRelayUrl: '', hasEnrollmentToken: true };

describe('WebhookRelayConnect', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.resetAllMocks();
    vi.mocked(api.connectWebhookRelay).mockResolvedValue({ authorizationUrl: 'https://relay/auth/connect/abc', expiresAt: Date.now() + 600_000 });
    vi.mocked(api.pollWebhookRelay).mockResolvedValue({ pending: true });
  });
  afterEach(() => vi.useRealTimers());

  const start = async () => { await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Connect relay' }))); };
  const tick = async () => { await act(async () => { await vi.advanceTimersByTimeAsync(1500); }); };

  it('signs in on the relay and caches only the connected settings', async () => {
    const onConnected = vi.fn();
    const { client } = renderWithQuery(<WebhookRelayConnect configured connected={false} onConnected={onConnected} />);
    await start();
    const link = screen.getByRole('link', { name: 'Sign in to relay' });
    expect(link).toHaveAttribute('href', 'https://relay/auth/connect/abc');
    expect(link).toHaveAttribute('rel', 'noopener noreferrer');
    await tick();
    expect(screen.getByRole('status')).toHaveTextContent('Waiting for relay sign-in');
    vi.mocked(api.pollWebhookRelay).mockResolvedValue({ pending: false, settings });
    await tick();
    expect(client.getQueryData(['webhook-relay'])).toEqual(settings);
    expect(onConnected).toHaveBeenCalledWith(settings);
    expect(screen.queryByRole('link')).not.toBeInTheDocument();
  });

  it('disables connecting without a relay and supports reconnecting', async () => {
    const { rerender } = renderWithQuery(<WebhookRelayConnect configured={false} connected={false} />);
    expect(screen.getByRole('button', { name: 'Connect relay' })).toBeDisabled();
    rerender(<WebhookRelayConnect configured connected />);
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Reconnect relay' })));
    expect(api.connectWebhookRelay).toHaveBeenCalledTimes(1);
  });

  it('cancels a pending connection', async () => {
    vi.mocked(api.cancelWebhookRelayConnect).mockResolvedValue(undefined);
    renderWithQuery(<WebhookRelayConnect configured connected={false} />);
    await start();
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Cancel connection' })));
    expect(screen.queryByRole('status')).not.toBeInTheDocument();
    await tick();
    expect(api.pollWebhookRelay).not.toHaveBeenCalled();
  });

  it('shows sign-in, polling and cancel errors near the action', async () => {
    renderWithQuery(<WebhookRelayConnect configured connected={false} />);
    vi.mocked(api.connectWebhookRelay).mockRejectedValueOnce(new Error('SSO unavailable'));
    await start();
    expect(screen.getByRole('alert')).toHaveTextContent('SSO unavailable');
    await start();
    vi.mocked(api.cancelWebhookRelayConnect).mockRejectedValueOnce(new Error('Cancel failed'));
    await act(async () => fireEvent.click(screen.getByRole('button', { name: 'Cancel connection' })));
    expect(screen.getByRole('alert')).toHaveTextContent('Cancel failed');
    vi.mocked(api.pollWebhookRelay).mockRejectedValueOnce(new Error('Relay offline'));
    await tick();
    expect(screen.getByRole('alert')).toHaveTextContent('Relay offline');
    expect(screen.getByRole('button', { name: 'Connect relay' })).toBeEnabled();
  });

  it('expires connections and stops polling on unmount', async () => {
    vi.mocked(api.connectWebhookRelay).mockResolvedValueOnce({ authorizationUrl: 'https://relay/auth/connect/abc', expiresAt: Date.now() + 100 });
    const { unmount } = renderWithQuery(<WebhookRelayConnect configured connected={false} />);
    await start();
    await tick();
    expect(screen.getByRole('alert')).toHaveTextContent('Connection expired');
    await start();
    unmount();
    await tick();
    expect(api.pollWebhookRelay).not.toHaveBeenCalled();
  });
});
